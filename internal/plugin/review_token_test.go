package plugin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestReviewTokenBindsReviewedMaterial(t *testing.T) {
	base := Review{
		PackageDigest: "sha256:package",
		Source: Source{
			Type:             "git",
			Ref:              "https://user:secret@example.com/org/repo.git?token=hidden#fragment",
			Revision:         "0123456789abcdef",
			Selector:         "main",
			Subdir:           "plugins/demo",
			ResolvedType:     "git",
			ResolvedRef:      "ssh://deploy:password@example.com/org/repo.git",
			ResolvedRevision: "0123456789abcdef",
			ResolvedSubdir:   "plugins/demo",
		},
		Compatibility: Compatibility{DetectedFormat: "openai", Adapter: "openai"},
		Warnings:      []string{"warning-b", "warning-a"},
		Executables:   []string{"bin/b", "bin/a"},
	}
	stable := makeReviewToken(base)
	if !strings.HasPrefix(stable, reviewTokenPrefix) {
		t.Fatalf("token = %q", stable)
	}
	material, err := json.Marshal(normalizedReviewSource(base.Source))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user", "secret", "token", "password", "plugins/demo", "org/repo.git"} {
		if strings.Contains(stable, secret) || strings.Contains(string(material), secret) {
			t.Fatalf("review token material exposed source value %q: token=%q material=%s", secret, stable, material)
		}
	}

	reordered := base
	reordered.Warnings = []string{"warning-a", "warning-b", "warning-a"}
	reordered.Executables = []string{"bin/a", "bin/b"}
	if got := makeReviewToken(reordered); got != stable {
		t.Fatalf("order-only change altered token: %q != %q", got, stable)
	}

	cases := []struct {
		name   string
		mutate func(*Review)
	}{
		{"package digest", func(review *Review) { review.PackageDigest = "sha256:other" }},
		{"format", func(review *Review) { review.Compatibility.DetectedFormat = "claude" }},
		{"adapter", func(review *Review) { review.Compatibility.Adapter = "claude" }},
		{"source revision", func(review *Review) { review.Source.Revision = "fedcba9876543210" }},
		{"source reference", func(review *Review) { review.Source.Ref = "https://example.com/other/repo.git" }},
		{"source subdir", func(review *Review) { review.Source.Subdir = "plugins/other" }},
		{"catalog item", func(review *Review) { review.Source.CatalogItem = "other.plugin" }},
		{"warning", func(review *Review) { review.Warnings = append(review.Warnings, "warning-c") }},
		{"executable", func(review *Review) { review.Executables = append(review.Executables, "bin/c") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			changed.Warnings = append([]string(nil), base.Warnings...)
			changed.Executables = append([]string(nil), base.Executables...)
			tc.mutate(&changed)
			if got := makeReviewToken(changed); got == stable {
				t.Fatalf("%s change did not alter token", tc.name)
			}
		})
	}
}

func TestReviewTokenOmitsLocalPathsAndRemoteCredentials(t *testing.T) {
	local := Review{
		PackageDigest: "sha256:package",
		Source:        Source{Type: "local", Ref: `C:\Users\one\private\plugin`},
		Compatibility: Compatibility{DetectedFormat: "portable", Adapter: "portable"},
	}
	otherPath := local
	otherPath.Source.Ref = `/home/two/private/plugin`
	if makeReviewToken(local) != makeReviewToken(otherPath) {
		t.Fatal("local filesystem path changed review token")
	}

	remote := local
	remote.Source = Source{Type: "git", Ref: "https://first:secret@example.com/org/repo.git?token=one"}
	otherCredentials := remote
	otherCredentials.Source.Ref = "https://second:other@example.com/org/repo.git?token=two"
	if makeReviewToken(remote) != makeReviewToken(otherCredentials) {
		t.Fatal("remote credentials or query changed review token")
	}
}

func TestVerifyReviewTokenUsesReviewErrors(t *testing.T) {
	review := Review{PackageDigest: "sha256:package", Compatibility: Compatibility{Adapter: "portable"}}
	token := makeReviewToken(review)
	for _, tc := range []struct {
		name  string
		token string
		code  string
	}{
		{"missing", "", "PLUGIN_REVIEW_REQUIRED"},
		{"changed", token + "0", "PLUGIN_REVIEW_CHANGED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyReviewToken(review, tc.token)
			var pluginErr *Error
			if !errors.As(err, &pluginErr) || pluginErr.Code != tc.code {
				t.Fatalf("error = %#v, want %s", err, tc.code)
			}
		})
	}
	if err := verifyReviewToken(review, token); err != nil {
		t.Fatal(err)
	}
}
