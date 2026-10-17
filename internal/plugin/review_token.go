package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
)

const reviewTokenPrefix = "review-v1:sha256:"

type reviewTokenMaterial struct {
	PackageDigest  string               `json:"package_digest"`
	DetectedFormat string               `json:"detected_format"`
	Adapter        string               `json:"adapter"`
	Source         reviewSourceIdentity `json:"source"`
	Warnings       []string             `json:"warnings,omitempty"`
	Executables    []string             `json:"executables,omitempty"`
}

type reviewSourceIdentity struct {
	Type                      string `json:"type"`
	ReferenceFingerprint      string `json:"reference_fingerprint,omitempty"`
	Revision                  string `json:"revision,omitempty"`
	Selector                  string `json:"selector,omitempty"`
	SubdirFingerprint         string `json:"subdir_fingerprint,omitempty"`
	CatalogFingerprint        string `json:"catalog_fingerprint,omitempty"`
	CatalogItemFingerprint    string `json:"catalog_item_fingerprint,omitempty"`
	ResolvedType              string `json:"resolved_type,omitempty"`
	ResolvedRefFingerprint    string `json:"resolved_reference_fingerprint,omitempty"`
	ResolvedRevision          string `json:"resolved_revision,omitempty"`
	ResolvedSubdirFingerprint string `json:"resolved_subdir_fingerprint,omitempty"`
}

func makeReviewToken(review Review) string {
	material := reviewTokenMaterial{
		PackageDigest:  review.PackageDigest,
		DetectedFormat: strings.TrimSpace(review.Compatibility.DetectedFormat),
		Adapter:        strings.TrimSpace(review.Compatibility.Adapter),
		Source:         normalizedReviewSource(review.Source),
		Warnings:       sortedReviewValues(review.Warnings),
		Executables:    sortedReviewValues(review.Executables),
	}
	data, _ := json.Marshal(material)
	sum := sha256.Sum256(data)
	return reviewTokenPrefix + hex.EncodeToString(sum[:])
}

func verifyReviewToken(review Review, submitted string) error {
	submitted = strings.TrimSpace(submitted)
	if submitted == "" {
		return pluginError("PLUGIN_REVIEW_REQUIRED", "review", errors.New("validate the Plugin and submit the returned review_token"))
	}
	if submitted != makeReviewToken(review) {
		return pluginError("PLUGIN_REVIEW_CHANGED", "review", errors.New("Plugin candidate changed after review; validate again and submit the new review_token"))
	}
	return nil
}

func normalizedReviewSource(source Source) reviewSourceIdentity {
	return reviewSourceIdentity{
		Type:                      strings.TrimSpace(source.Type),
		ReferenceFingerprint:      reviewReferenceFingerprint(source.Type, source.Ref),
		Revision:                  strings.TrimSpace(source.Revision),
		Selector:                  strings.TrimSpace(source.Selector),
		SubdirFingerprint:         reviewStringFingerprint(normalizedReviewSubdir(source.Subdir)),
		CatalogFingerprint:        reviewStringFingerprint(strings.TrimSpace(source.Catalog)),
		CatalogItemFingerprint:    reviewStringFingerprint(strings.TrimSpace(source.CatalogItem)),
		ResolvedType:              strings.TrimSpace(source.ResolvedType),
		ResolvedRefFingerprint:    reviewReferenceFingerprint(source.ResolvedType, source.ResolvedRef),
		ResolvedRevision:          strings.TrimSpace(source.ResolvedRevision),
		ResolvedSubdirFingerprint: reviewStringFingerprint(normalizedReviewSubdir(source.ResolvedSubdir)),
	}
}

func reviewReferenceFingerprint(sourceType, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(strings.TrimSpace(sourceType), "local") {
		return ""
	}
	canonical, ok := normalizedRemoteReference(raw)
	if !ok {
		return ""
	}
	return reviewStringFingerprint(canonical)
}

func reviewStringFingerprint(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedRemoteReference(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Scheme != "" {
		scheme := strings.ToLower(parsed.Scheme)
		switch scheme {
		case "http", "https", "ssh", "git":
			parsed.Scheme = scheme
			parsed.Host = strings.ToLower(parsed.Host)
			parsed.User = nil
			parsed.RawQuery = ""
			parsed.ForceQuery = false
			parsed.Fragment = ""
			return parsed.String(), true
		default:
			return "", false
		}
	}
	if at := strings.LastIndex(raw, "@"); at >= 0 && strings.Contains(raw[at+1:], ":") {
		return strings.TrimSpace(raw[at+1:]), true
	}
	return "", false
}

func normalizedReviewSubdir(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "." {
		return ""
	}
	return strings.TrimPrefix(pathpkg.Clean("/"+value), "/")
}

func sortedReviewValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
