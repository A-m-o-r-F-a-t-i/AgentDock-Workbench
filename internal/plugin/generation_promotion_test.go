package plugin

import (
	"path/filepath"
	"testing"
)

const promotionTestGeneration = "0123456789abcdef0123456789abcdef"

func TestPromoteGenerationAcceptsMatchingConcurrentWinner(t *testing.T) {
	source := writePluginPackage(t, t.TempDir(), "promotion-demo", "1.0.0", []string{"demo-skill"}, nil)
	expectedDigest, exists, err := packageDigestAt(source)
	if err != nil || !exists {
		t.Fatalf("source digest = %q exists=%v err=%v", expectedDigest, exists, err)
	}
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate")
	destination := filepath.Join(root, "destination")
	if err := copyPackageTree(source, candidate); err != nil {
		t.Fatal(err)
	}
	if err := copyPackageTree(source, destination); err != nil {
		t.Fatal(err)
	}

	if err := promoteGeneration(candidate, destination, expectedDigest, "promotion-demo", promotionTestGeneration); err != nil {
		t.Fatalf("matching concurrent generation was rejected: %v", err)
	}
}

func TestPromoteGenerationRejectsMismatchedConcurrentWinner(t *testing.T) {
	candidateSource := writePluginPackage(t, t.TempDir(), "promotion-demo", "1.0.0", []string{"demo-skill"}, nil)
	winnerSource := writePluginPackage(t, t.TempDir(), "promotion-demo", "2.0.0", []string{"demo-skill"}, nil)
	expectedDigest, exists, err := packageDigestAt(candidateSource)
	if err != nil || !exists {
		t.Fatalf("candidate digest = %q exists=%v err=%v", expectedDigest, exists, err)
	}
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate")
	destination := filepath.Join(root, "destination")
	if err := copyPackageTree(candidateSource, candidate); err != nil {
		t.Fatal(err)
	}
	if err := copyPackageTree(winnerSource, destination); err != nil {
		t.Fatal(err)
	}

	err = promoteGeneration(candidate, destination, expectedDigest, "promotion-demo", promotionTestGeneration)
	if pluginErrorCode(err) != "PLUGIN_GENERATION_DRIFT" {
		t.Fatalf("mismatched concurrent generation error = %v", err)
	}
}
