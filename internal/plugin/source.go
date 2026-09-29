package plugin

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maxPluginArchiveBytes   = int64(64 << 20)
	maxPluginExtractedBytes = int64(256 << 20)
	maxPluginArchiveFiles   = 10000
)

var fullGitCommitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

type stagedPluginSource struct {
	Root    string
	Source  Source
	Cleanup func()
	Hints   adapterHints
}

func legacyLocalSourceRequest(source string) SourceRequest {
	return SourceRequest{Type: "local", Ref: strings.TrimSpace(source), Adapter: "auto"}
}

func normalizeSourceRequest(request SourceRequest) (SourceRequest, error) {
	request.Type = strings.ToLower(strings.TrimSpace(request.Type))
	request.Ref = strings.TrimSpace(request.Ref)
	request.GitRef = strings.TrimSpace(request.GitRef)
	request.GitCommit = strings.ToLower(strings.TrimSpace(request.GitCommit))
	request.Subdir = strings.TrimSpace(strings.ReplaceAll(request.Subdir, "\\", "/"))
	request.SHA256 = normalizePluginSHA256(request.SHA256)
	if strings.HasPrefix(request.SHA256, "invalid:") {
		return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source.sha256", errors.New("sha256 must contain exactly 64 hexadecimal characters"))
	}
	request.Adapter = strings.ToLower(strings.TrimSpace(request.Adapter))
	request.Version = strings.TrimSpace(request.Version)
	request.Catalog = strings.TrimSpace(request.Catalog)
	request.CatalogItem = strings.TrimSpace(request.CatalogItem)
	if request.Type == "" || request.Type == "auto" {
		request.Type = inferPluginSourceType(request.Ref)
	}
	switch request.Type {
	case "local", "git", "archive", "catalog":
	default:
		return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source.type", fmt.Errorf("unsupported Plugin source type %q", request.Type))
	}
	if request.Ref == "" {
		return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source.ref", errors.New("Plugin source is required"))
	}
	if request.Adapter == "" {
		request.Adapter = "auto"
	}
	switch request.Adapter {
	case "auto", "portable", "openai", "claude":
	default:
		return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source.adapter", fmt.Errorf("unsupported Plugin adapter %q", request.Adapter))
	}
	if request.GitCommit != "" && !fullGitCommitPattern.MatchString(request.GitCommit) {
		return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source.git_commit", errors.New("git_commit must be a full 40-character commit SHA"))
	}
	if request.Subdir != "" {
		if _, err := cleanPluginRelativePath(request.Subdir); err != nil {
			return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source.subdir", err)
		}
	}
	if request.Version != "" && ValidateVersion(strings.TrimPrefix(request.Version, "v")) != nil {
		return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source.version", errors.New("source_version must be SemVer"))
	}
	if request.Type == "archive" && request.GitRef != "" {
		return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source.git_ref", errors.New("git_ref is not valid for archive sources"))
	}
	if request.Type == "local" && (request.GitRef != "" || request.GitCommit != "" || request.SHA256 != "") {
		return SourceRequest{}, pluginError("PLUGIN_SOURCE_INVALID", "source", errors.New("local sources do not accept git/archive pin fields"))
	}
	return request, nil
}

func inferPluginSourceType(ref string) string {
	ref = strings.TrimSpace(ref)
	if info, err := os.Lstat(ref); err == nil && info.IsDir() {
		return "local"
	}
	lower := strings.ToLower(ref)
	if strings.HasSuffix(lower, ".zip") {
		return "archive"
	}
	return "git"
}

func (m *Store) stagePluginSource(ctx context.Context, request SourceRequest) (stagedPluginSource, error) {
	request, err := normalizeSourceRequest(request)
	if err != nil {
		return stagedPluginSource{}, err
	}
	switch request.Type {
	case "local":
		return m.stageLocalSource(request)
	case "git":
		return m.stageGitSource(ctx, request)
	case "archive":
		return m.stageArchiveSource(ctx, request)
	case "catalog":
		return m.stageCatalogEntry(ctx, request)
	default:
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.type", errors.New("unsupported Plugin source type"))
	}
}

func (m *Store) stageLocalSource(request SourceRequest) (stagedPluginSource, error) {
	absolute, err := filepath.Abs(request.Ref)
	if err != nil {
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.local", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.local", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.local", errors.New("local Plugin source must be a regular directory"))
	}
	root, err := selectPluginSourceRoot(absolute, request.Subdir)
	if err != nil {
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.subdir", err)
	}
	return stagedPluginSource{
		Root:    root,
		Source:  Source{Type: "local", Ref: absolute, Subdir: request.Subdir, Adapter: request.Adapter},
		Cleanup: func() {},
	}, nil
}

func (m *Store) stageGitSource(ctx context.Context, request SourceRequest) (stagedPluginSource, error) {
	if err := validateGitSourceRef(request.Ref); err != nil {
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.git", err)
	}
	temp, err := m.TempPath("git-source")
	if err != nil {
		return stagedPluginSource{}, err
	}
	cleanup := func() { _ = os.RemoveAll(temp) }
	bare := filepath.Join(temp, "repository.git")
	if output, err := clonePluginGit(ctx, request.Ref, bare, request.GitRef, request.GitCommit == ""); err != nil {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_FETCH_FAILED", "source.git.clone", fmt.Errorf("%w: %s", err, strings.TrimSpace(output)))
	}

	revisionExpr := "HEAD^{commit}"
	if request.GitCommit != "" {
		revisionExpr = request.GitCommit + "^{commit}"
		if _, err := runPluginGit(ctx, bare, "cat-file", "-e", revisionExpr); err != nil {
			if output, fetchErr := runPluginGit(ctx, bare, "fetch", "--depth=1", "origin", request.GitCommit); fetchErr != nil {
				cleanup()
				return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_FETCH_FAILED", "source.git.fetch_commit", fmt.Errorf("%w: %s", fetchErr, strings.TrimSpace(output)))
			}
		}
	}
	resolved, err := runPluginGit(ctx, bare, "rev-parse", revisionExpr)
	if err != nil {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_FETCH_FAILED", "source.git.resolve", err)
	}
	resolved = strings.ToLower(strings.TrimSpace(resolved))
	if !fullGitCommitPattern.MatchString(resolved) {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_FETCH_FAILED", "source.git.resolve", errors.New("git resolved an invalid commit identity"))
	}
	if request.GitCommit != "" && resolved != request.GitCommit {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_PIN_MISMATCH", "source.git.commit", fmt.Errorf("resolved commit %s does not match requested %s", resolved, request.GitCommit))
	}

	extracted := filepath.Join(temp, "tree")
	if err := os.MkdirAll(extracted, 0o700); err != nil {
		cleanup()
		return stagedPluginSource{}, err
	}
	if err := extractGitArchive(ctx, bare, resolved, extracted); err != nil {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_FETCH_FAILED", "source.git.archive", err)
	}
	root, err := selectPluginSourceRoot(extracted, request.Subdir)
	if err != nil {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.subdir", err)
	}
	return stagedPluginSource{
		Root: root,
		Source: Source{
			Type: "git", Ref: request.Ref, Revision: resolved, Selector: request.GitRef,
			Subdir: request.Subdir, Adapter: request.Adapter,
		},
		Cleanup: cleanup,
	}, nil
}

func validateGitSourceRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("Git source is required")
	}
	if info, err := os.Stat(ref); err == nil && info.IsDir() {
		return nil
	}
	parsed, err := url.Parse(ref)
	if err != nil || parsed.Scheme == "" {
		return errors.New("Git source must be a local repository path or an absolute https/ssh URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "ssh" {
		return errors.New("Git source must use https or ssh")
	}
	if parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Git source URL must not contain query parameters or fragments")
	}
	if parsed.Scheme == "https" && parsed.User != nil {
		return errors.New("Git HTTPS source must not embed credentials")
	}
	return nil
}

var gitBranchSelectorPattern = regexp.MustCompile(`^[\pL\pN_][\pL\pN_./+-]*$`)
var gitCommitExpressionPattern = regexp.MustCompile(`^(HEAD|[a-fA-F0-9]{40})\^\{commit\}$`)

// clonePluginGit fixes the operation and options; repository paths remain operands after --.
func clonePluginGit(ctx context.Context, source, destination, ref string, shallow bool) (string, error) {
	if err := validateGitSourceRef(source); err != nil {
		return "", err
	}
	if !filepath.IsAbs(destination) || strings.ContainsRune(destination, 0) {
		return "", errors.New("invalid Git staging destination")
	}
	if len(ref) > 1024 || ref != "" && !gitBranchSelectorPattern.MatchString(ref) {
		return "", errors.New("invalid Git branch selector")
	}
	if strings.HasPrefix(ref, "--") {
		return "", errors.New("Git options are not branch selectors")
	}
	var command *exec.Cmd
	switch {
	case shallow && ref != "":
		command = exec.CommandContext(ctx, "git", "-c", "core.hooksPath="+nullDevicePath(), "-c", "submodule.recurse=false", "clone", "--bare", "--no-recurse-submodules", "--depth=1", "--branch", ref, "--", source, destination)
	case shallow:
		command = exec.CommandContext(ctx, "git", "-c", "core.hooksPath="+nullDevicePath(), "-c", "submodule.recurse=false", "clone", "--bare", "--no-recurse-submodules", "--depth=1", "--", source, destination)
	case ref != "":
		command = exec.CommandContext(ctx, "git", "-c", "core.hooksPath="+nullDevicePath(), "-c", "submodule.recurse=false", "clone", "--bare", "--no-recurse-submodules", "--branch", ref, "--", source, destination)
	default:
		command = exec.CommandContext(ctx, "git", "-c", "core.hooksPath="+nullDevicePath(), "-c", "submodule.recurse=false", "clone", "--bare", "--no-recurse-submodules", "--", source, destination)
	}
	return capturePluginGit(command)
}

func runPluginGit(ctx context.Context, gitDir string, args ...string) (string, error) {
	if !filepath.IsAbs(gitDir) || strings.ContainsRune(gitDir, 0) {
		return "", errors.New("invalid Git repository directory")
	}
	var command *exec.Cmd
	switch {
	case len(args) == 3 && args[0] == "cat-file" && args[1] == "-e" && gitCommitExpressionPattern.MatchString(args[2]):
		command = exec.CommandContext(ctx, "git", "--git-dir", gitDir, "cat-file", "-e", "--", args[2])
	case len(args) == 4 && args[0] == "fetch" && args[1] == "--depth=1" && args[2] == "origin" && fullGitCommitPattern.MatchString(args[3]):
		command = exec.CommandContext(ctx, "git", "--git-dir", gitDir, "fetch", "--depth=1", "--", "origin", args[3])
	case len(args) == 2 && args[0] == "rev-parse" && gitCommitExpressionPattern.MatchString(args[1]):
		command = exec.CommandContext(ctx, "git", "--git-dir", gitDir, "rev-parse", "--verify", "--end-of-options", args[1])
	default:
		return "", errors.New("unsupported Plugin Git operation or revision")
	}
	return capturePluginGit(command)
}

func capturePluginGit(command *exec.Cmd) (string, error) {
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1", "GIT_OPTIONAL_LOCKS=0")
	command.WaitDelay = 2 * time.Second
	var output pluginGitOutput
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	return output.String(), err
}

// A capped diagnostic prefix still drains all writes; it cannot block a noisy child.
type pluginGitOutput struct {
	bytes     []byte
	truncated bool
}

func (b *pluginGitOutput) Write(value []byte) (int, error) {
	keep := min(len(value), (64<<10)-len(b.bytes))
	b.bytes = append(b.bytes, value[:keep]...)
	b.truncated = b.truncated || keep < len(value)
	return len(value), nil
}
func (b *pluginGitOutput) String() string {
	if b.truncated {
		return string(b.bytes) + "\n[Git output truncated after 65536 bytes]"
	}
	return string(b.bytes)
}

func extractGitArchive(ctx context.Context, gitDir, revision, destination string) error {
	if !filepath.IsAbs(gitDir) || !fullGitCommitPattern.MatchString(revision) {
		return errors.New("invalid Git archive identity")
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(childCtx, "git", "--git-dir", gitDir, "archive", "--format=tar", revision)
	command.WaitDelay = 2 * time.Second
	var diagnostics pluginGitOutput
	command.Stderr = &diagnostics
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err = command.Start(); err != nil {
		return err
	}
	extractErr := extractPluginTar(stdout, destination)
	if extractErr != nil {
		// An early rejected entry leaves later archive bytes in stdout. Cancel
		// this child before waiting, rather than deadlocking on its full pipe.
		cancel()
	} else {
		_, extractErr = io.Copy(io.Discard, stdout)
		if extractErr != nil {
			cancel()
		}
	}
	waitErr := command.Wait()
	if extractErr != nil {
		return extractErr
	}
	if waitErr != nil {
		return fmt.Errorf("%w: %s", waitErr, strings.TrimSpace(diagnostics.String()))
	}
	return nil
}

func extractPluginTar(input io.Reader, destination string) error {
	handle, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer handle.Close()
	reader := tar.NewReader(bufio.NewReader(input))
	var total int64
	files := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		files++
		if files > maxPluginArchiveFiles {
			return fmt.Errorf("Plugin archive exceeds %d entries", maxPluginArchiveFiles)
		}
		archivePath := filepath.FromSlash(strings.ReplaceAll(strings.TrimSpace(header.Name), "\\", "/"))
		if !filepath.IsLocal(archivePath) || filepath.Clean(archivePath) == "." {
			return fmt.Errorf("archive path %q is not local", header.Name)
		}
		relative, err := cleanPluginRelativePath(header.Name)
		if err != nil {
			return fmt.Errorf("archive path %q: %w", header.Name, err)
		}
		if filepath.Clean(archivePath) != filepath.FromSlash(relative) {
			return fmt.Errorf("archive path %q has ambiguous normalization", header.Name)
		}
		target := filepath.Clean(archivePath)
		switch header.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		case tar.TypeDir:
			if err := handle.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxPluginExtractedBytes-total {
				return errors.New("archive file exceeds the remaining extracted-byte budget")
			}
			total += header.Size
			if total > maxPluginExtractedBytes {
				return fmt.Errorf("Plugin archive exceeds %d extracted bytes", maxPluginExtractedBytes)
			}
			if err := handle.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode).Perm() & 0o755
			if mode == 0 {
				mode = 0o600
			}
			file, err := handle.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("Plugin archive contains unsupported special entry %q", header.Name)
		}
	}
}

func (m *Store) stageArchiveSource(ctx context.Context, request SourceRequest) (stagedPluginSource, error) {
	temp, err := m.TempPath("archive-source")
	if err != nil {
		return stagedPluginSource{}, err
	}
	cleanup := func() { _ = os.RemoveAll(temp) }
	archivePath := filepath.Join(temp, "plugin.zip")

	var input io.ReadCloser
	sourceRef := request.Ref
	if info, statErr := os.Lstat(request.Ref); statErr == nil {
		absolute, absErr := filepath.Abs(request.Ref)
		if absErr != nil {
			cleanup()
			return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.archive", absErr)
		}
		info, statErr = os.Lstat(absolute)
		if statErr != nil {
			cleanup()
			return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.archive", statErr)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			cleanup()
			return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.archive", errors.New("local archive source must be a regular file"))
		}
		file, openErr := os.Open(absolute)
		if openErr != nil {
			cleanup()
			return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.archive", openErr)
		}
		input = file
		sourceRef = absolute
	} else {
		parsed, parseErr := url.Parse(request.Ref)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			cleanup()
			return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.archive", errors.New("archive source must be a regular local file or an HTTPS URL without embedded credentials, query parameters, or fragments"))
		}
		requestHTTP, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, request.Ref, nil)
		if requestErr != nil {
			cleanup()
			return stagedPluginSource{}, requestErr
		}
		client := &http.Client{
			Timeout: 2 * time.Minute,
			CheckRedirect: func(next *http.Request, via []*http.Request) error {
				if len(via) >= 8 {
					return errors.New("too many archive redirects")
				}
				if next.URL.Scheme != "https" || next.URL.Host == "" || next.URL.User != nil ||
					next.URL.RawQuery != "" || next.URL.Fragment != "" {
					return errors.New("archive redirect must remain an HTTPS URL without credentials, query parameters, or fragments")
				}
				return nil
			},
		}
		response, requestErr := client.Do(requestHTTP)
		if requestErr != nil {
			cleanup()
			return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_FETCH_FAILED", "source.archive.download", requestErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			_ = response.Body.Close()
			cleanup()
			return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_FETCH_FAILED", "source.archive.download", fmt.Errorf("archive server returned HTTP %d", response.StatusCode))
		}
		input = response.Body
	}
	defer input.Close()

	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		cleanup()
		return stagedPluginSource{}, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input, maxPluginArchiveBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		cleanup()
		return stagedPluginSource{}, errors.Join(copyErr, closeErr)
	}
	if written > maxPluginArchiveBytes {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.archive.size", fmt.Errorf("archive exceeds %d bytes", maxPluginArchiveBytes))
	}
	resolved := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if request.SHA256 != "" && resolved != request.SHA256 {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_PIN_MISMATCH", "source.archive.sha256", fmt.Errorf("archive digest %s does not match requested %s", resolved, request.SHA256))
	}

	extracted := filepath.Join(temp, "tree")
	if err := os.MkdirAll(extracted, 0o700); err != nil {
		cleanup()
		return stagedPluginSource{}, err
	}
	if err := extractPluginZip(archivePath, extracted); err != nil {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.archive.extract", err)
	}
	root, err := selectPluginSourceRoot(extracted, request.Subdir)
	if err != nil {
		cleanup()
		return stagedPluginSource{}, pluginError("PLUGIN_SOURCE_INVALID", "source.subdir", err)
	}
	return stagedPluginSource{
		Root:    root,
		Source:  Source{Type: "archive", Ref: sourceRef, Revision: resolved, Subdir: request.Subdir, Adapter: request.Adapter},
		Cleanup: cleanup,
	}, nil
}

func extractPluginZip(path, destination string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	handle, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer handle.Close()
	if len(reader.File) > maxPluginArchiveFiles {
		return fmt.Errorf("Plugin archive exceeds %d entries", maxPluginArchiveFiles)
	}
	var total int64
	for _, entry := range reader.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" {
			continue
		}
		archivePath := filepath.FromSlash(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
		if !filepath.IsLocal(archivePath) || filepath.Clean(archivePath) == "." {
			return fmt.Errorf("archive path %q is not local", entry.Name)
		}
		relative, err := cleanPluginRelativePath(name)
		if err != nil {
			return fmt.Errorf("archive path %q: %w", entry.Name, err)
		}
		if filepath.Clean(archivePath) != filepath.FromSlash(relative) {
			return fmt.Errorf("archive path %q has ambiguous normalization", entry.Name)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Plugin archive contains symlink %q", entry.Name)
		}
		target := filepath.Clean(archivePath)
		if entry.FileInfo().IsDir() {
			if err := handle.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("Plugin archive contains special file %q", entry.Name)
		}
		if entry.UncompressedSize64 > uint64(maxPluginExtractedBytes-total) {
			return fmt.Errorf("Plugin archive exceeds %d extracted bytes", maxPluginExtractedBytes)
		}
		total += int64(entry.UncompressedSize64)
		if err := handle.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		mode := entry.Mode().Perm() & 0o755
		if mode == 0 {
			mode = 0o600
		}
		output, err := handle.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			input.Close()
			return err
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
		if copyErr == nil && written != int64(entry.UncompressedSize64) {
			copyErr = io.ErrUnexpectedEOF
		}
		closeOut := output.Close()
		closeIn := input.Close()
		if copyErr != nil || closeOut != nil || closeIn != nil {
			return errors.Join(copyErr, closeOut, closeIn)
		}
	}
	return nil
}

func cleanPluginRelativePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") {
		return "", errors.New("path must be a non-empty relative path")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(filepath.FromSlash(clean)) {
		return "", errors.New("path escapes source root")
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("path contains an invalid segment")
		}
	}
	return clean, nil
}

func resolvePluginSourcePath(root, relative string, wantDirectory bool) (string, error) {
	clean, err := cleanPluginRelativePath(relative)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	handle, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer handle.Close()

	current := ""
	parts := strings.Split(filepath.FromSlash(clean), string(filepath.Separator))
	var info os.FileInfo
	for index, part := range parts {
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		info, err = handle.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path contains symlink component %q", filepath.ToSlash(current))
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("path component %q is not a directory", filepath.ToSlash(current))
		}
	}
	if wantDirectory {
		if info == nil || !info.IsDir() {
			return "", errors.New("path is not a directory")
		}
	} else if info == nil || !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

func selectPluginSourceRoot(root, subdir string) (string, error) {
	root = filepath.Clean(root)
	if subdir != "" {
		clean, err := cleanPluginRelativePath(subdir)
		if err != nil {
			return "", err
		}
		target, err := resolvePluginSourcePath(root, clean, true)
		if err != nil {
			return "", fmt.Errorf("subdir must stay inside source root as a regular directory: %w", err)
		}
		return target, nil
	}
	if hasPluginLayoutMarker(root) {
		return root, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	var onlyDir string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") && !entry.IsDir() {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return root, nil
		}
		if onlyDir != "" {
			return root, nil
		}
		onlyDir = entry.Name()
	}
	if onlyDir != "" {
		candidate := filepath.Join(root, onlyDir)
		if hasPluginLayoutMarker(candidate) {
			return candidate, nil
		}
	}
	return root, nil
}

func hasPluginLayoutMarker(root string) bool {
	for _, relative := range []string{
		"plugin.json",
		filepath.Join(".codex-plugin", "plugin.json"),
		filepath.Join(".claude-plugin", "plugin.json"),
	} {
		if info, err := os.Lstat(filepath.Join(root, relative)); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return true
		}
	}
	return false
}

func normalizePluginSHA256(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != 64 {
		return "invalid:" + value
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "invalid:" + value
	}
	return "sha256:" + value
}

func nullDevicePath() string {
	if filepath.Separator == '\\' {
		return "NUL"
	}
	return "/dev/null"
}
