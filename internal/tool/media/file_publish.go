package media

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/uvwt/agentdock/internal/httpx/requestmeta"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

const (
	maxConnectorFileBytes    int64 = 128 << 20
	maxConnectorRedirects          = 5
	connectorDownloadTimeout       = 2 * time.Minute
)

var blockedConnectorDownloadPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
}

type connectorFileInput struct {
	DownloadURL string
	FileID      string
	FileName    string
	MimeType    string
}

func (s *Service) FilePublish(ctx context.Context, request FilePublishRequest) (Result, error) {
	pathValue, cleanup, err := s.filePublishSourcePath(ctx, request)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	store := publicartifacts.New(s.cfg.AgentDockHome, s.cfg.OAuthServerURL, s.cfg.Port)
	published, err := store.Publish(publicartifacts.PublishRequest{Path: pathValue, RetentionSeconds: intValue(request.RetentionSeconds, 0), BaseURL: requestmeta.BaseURL(ctx)})
	if err != nil {
		return nil, fmt.Errorf("publish file: %w", err)
	}
	result := Result{}
	for key, value := range artifactResult(published) {
		result[key] = value
	}
	return result, nil
}

func (s *Service) filePublishSourcePath(ctx context.Context, request FilePublishRequest) (string, func(), error) {
	pathValue := strings.TrimSpace(request.Path)
	if request.File != nil && pathValue != "" {
		return "", func() {}, toolError("FILE_PUBLISH_SOURCE_CONFLICT", "file and path are mutually exclusive", "validation")
	}
	if request.File != nil {
		if mountedPath := connectorLocalPath(request.File); mountedPath != "" {
			resolved, err := s.ws.ResolveExisting(mountedPath)
			if err != nil {
				return "", func() {}, err
			}
			return resolved.Abs, func() {}, nil
		}
		input, ok := connectorFile(request.File)
		if !ok {
			return "", func() {}, toolError("FILE_PUBLISH_FILE_INVALID", "file must be a mounted path or connector file object", "validation")
		}
		clientFactory := s.connectorDownloadClient
		if clientFactory == nil {
			clientFactory = newConnectorDownloadClient
		}
		client := clientFactory()
		if client == nil {
			return "", func() {}, toolError("FILE_PUBLISH_DOWNLOAD_FAILED", "connector download client is unavailable", "runtime")
		}
		return s.downloadConnectorFile(ctx, input, client)
	}
	if pathValue == "" {
		return "", func() {}, toolError("FILE_PUBLISH_SOURCE_REQUIRED", "file or path is required", "validation")
	}
	resolved, err := s.ws.ResolveExisting(pathValue)
	if err != nil {
		return "", func() {}, err
	}
	return resolved.Abs, func() {}, nil
}

func connectorLocalPath(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"local_path", "file_path", "mount_path", "path"} {
			if raw, ok := v[key].(string); ok && strings.TrimSpace(raw) != "" {
				return strings.TrimSpace(raw)
			}
		}
		if raw, ok := v["filename"].(string); ok && strings.TrimSpace(raw) != "" && filepath.IsAbs(raw) {
			return strings.TrimSpace(raw)
		}
	case map[string]string:
		for _, key := range []string{"local_path", "file_path", "mount_path", "path"} {
			if raw := strings.TrimSpace(v[key]); raw != "" {
				return raw
			}
		}
		if raw := strings.TrimSpace(v["filename"]); raw != "" && filepath.IsAbs(raw) {
			return raw
		}
	}
	return ""
}

func connectorFile(value any) (connectorFileInput, bool) {
	var input connectorFileInput
	switch v := value.(type) {
	case map[string]any:
		input = connectorFileInput{
			DownloadURL: stringValue(v["download_url"]),
			FileID:      stringValue(v["file_id"]),
			FileName:    stringValue(v["file_name"]),
			MimeType:    stringValue(v["mime_type"]),
		}
	case map[string]string:
		input = connectorFileInput{
			DownloadURL: strings.TrimSpace(v["download_url"]),
			FileID:      strings.TrimSpace(v["file_id"]),
			FileName:    strings.TrimSpace(v["file_name"]),
			MimeType:    strings.TrimSpace(v["mime_type"]),
		}
	default:
		return connectorFileInput{}, false
	}
	if input.DownloadURL == "" && input.FileID == "" {
		return connectorFileInput{}, false
	}
	return input, true
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func (s *Service) downloadConnectorFile(ctx context.Context, input connectorFileInput, client *http.Client) (string, func(), error) {
	return s.downloadConnectorFileWithLimit(ctx, input, client, maxConnectorFileBytes)
}

func (s *Service) downloadConnectorFileWithLimit(ctx context.Context, input connectorFileInput, client *http.Client, maxBytes int64) (string, func(), error) {
	if input.DownloadURL == "" || input.FileID == "" {
		return "", func() {}, toolError("FILE_PUBLISH_FILE_INVALID", "file.download_url and file.file_id are required", "validation")
	}
	if client == nil || maxBytes <= 0 {
		return "", func() {}, toolError("FILE_PUBLISH_DOWNLOAD_FAILED", "connector download configuration is invalid", "runtime")
	}
	parsed, err := validateConnectorDownloadURL(input.DownloadURL)
	if err != nil {
		return "", func() {}, err
	}
	tmpRoot := filepath.Join(s.cfg.AgentDockHome, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("create file publish temp root: %w", err)
	}
	tmpDir, err := os.MkdirTemp(tmpRoot, "file-publish-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create file publish temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	target := filepath.Join(tmpDir, connectorDownloadName(input, parsed))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		cleanup()
		return "", func() {}, toolError("FILE_PUBLISH_DOWNLOAD_URL_INVALID", "cannot create connector file request", "validation")
	}
	resp, err := client.Do(req)
	if err != nil {
		cleanup()
		return "", func() {}, toolError("FILE_PUBLISH_DOWNLOAD_FAILED", "cannot download connector file", "runtime")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		cleanup()
		return "", func() {}, toolError("FILE_PUBLISH_DOWNLOAD_HTTP_ERROR", fmt.Sprintf("connector file download returned HTTP %d", resp.StatusCode), "runtime")
	}
	if resp.ContentLength > maxBytes {
		cleanup()
		return "", func() {}, toolError("FILE_PUBLISH_FILE_TOO_LARGE", "connector file exceeds maximum supported size", "validation")
	}

	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("create connector temp file: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		cleanup()
		return "", func() {}, toolError("FILE_PUBLISH_DOWNLOAD_FAILED", "cannot read connector file download", "runtime")
	}
	if closeErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close connector temp file: %w", closeErr)
	}
	if written > maxBytes {
		cleanup()
		return "", func() {}, toolError("FILE_PUBLISH_FILE_TOO_LARGE", "connector file exceeds maximum supported size", "validation")
	}
	return target, cleanup, nil
}

func validateConnectorDownloadURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return nil, toolError("FILE_PUBLISH_DOWNLOAD_URL_INVALID", "file.download_url must be an absolute HTTP(S) URL", "validation")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, toolError("FILE_PUBLISH_DOWNLOAD_URL_INVALID", "file.download_url must use http or https", "validation")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, toolError("FILE_PUBLISH_DOWNLOAD_URL_INVALID", "file.download_url cannot contain user credentials or a fragment", "validation")
	}
	host := parsed.Hostname()
	if host == "" || strings.Contains(host, "%") {
		return nil, toolError("FILE_PUBLISH_DOWNLOAD_URL_INVALID", "file.download_url host is invalid", "validation")
	}
	if literal := net.ParseIP(host); literal != nil && blockedConnectorDownloadIP(literal) {
		return nil, toolError("FILE_PUBLISH_DOWNLOAD_URL_BLOCKED", "file.download_url must resolve to a public address", "validation")
	}
	return parsed, nil
}

func connectorDownloadName(input connectorFileInput, parsed *url.URL) string {
	name := strings.TrimSpace(input.FileName)
	if name == "" {
		name = path.Base(parsed.Path)
	}
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "" || name == "." || name == "/" {
		name = "uploaded-file"
	}
	var builder strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\\|?*`, r) {
			builder.WriteByte('_')
			continue
		}
		builder.WriteRune(r)
	}
	name = strings.TrimSpace(builder.String())
	if name == "" || name == "." || name == ".." {
		return "uploaded-file"
	}
	runes := []rune(name)
	if len(runes) > 200 {
		name = string(runes[:200])
	}
	return name
}

func newConnectorDownloadClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = safeConnectorDialContext
	return &http.Client{
		Transport:     transport,
		Timeout:       connectorDownloadTimeout,
		CheckRedirect: checkConnectorRedirect,
	}
}

func checkConnectorRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxConnectorRedirects {
		return fmt.Errorf("connector download exceeded redirect limit")
	}
	parsed, err := validateConnectorDownloadURL(req.URL.String())
	if err != nil {
		return fmt.Errorf("unsafe connector download redirect: %w", err)
	}
	if len(via) > 0 && strings.EqualFold(via[len(via)-1].URL.Scheme, "https") && parsed.Scheme == "http" {
		return fmt.Errorf("connector download redirect cannot downgrade HTTPS to HTTP")
	}
	return nil
}

func safeConnectorDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if err := validateConnectorResolvedAddresses(addresses); err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, resolved := range addresses {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("connector download host resolved to no usable addresses")
	}
	return nil, lastErr
}

func validateConnectorResolvedAddresses(addresses []net.IPAddr) error {
	if len(addresses) == 0 {
		return fmt.Errorf("connector download host resolved to no addresses")
	}
	for _, resolved := range addresses {
		if blockedConnectorDownloadIP(resolved.IP) {
			return fmt.Errorf("connector download host resolves to a non-public address")
		}
	}
	return nil
}

func blockedConnectorDownloadIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range blockedConnectorDownloadPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
