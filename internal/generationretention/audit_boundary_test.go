package generationretention

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditCleanupRefusesInvalidOrMissingActiveGeneration(t *testing.T) {
	for _, active := range []string{"", "../v1.0.0", "v9.0.0"} {
		t.Run(active, func(t *testing.T) {
			root := t.TempDir()
			makeGenerations(t, root, "v1.0.0", "v2.0.0")
			report := Clean(Policy{VersionsDir: root, ActiveVersion: active})
			if len(report.Removed) != 0 || len(report.Warnings) == 0 {
				t.Errorf("unsafe policy allowed cleanup: %+v", report)
			}
			assertDirectory(t, filepath.Join(root, "v1.0.0"), true)
			assertDirectory(t, filepath.Join(root, "v2.0.0"), true)
		})
	}
}

func TestAuditRetentionRejectsMalformedTransactionIdentity(t *testing.T) {
	for _, identity := range []string{"..", ".", "../other"} {
		t.Run(identity, func(t *testing.T) {
			root := t.TempDir()
			versions := filepath.Join(root, "versions")
			makeGenerations(t, versions, "v1.0.0", "v2.0.0")
			writeJSONFixture(t, filepath.Join(root, "active-version.json"), `{"active_version":"v2.0.0","fallback_version":"v1.0.0"}`)
			writeJSONFixture(t, filepath.Join(root, "install", "transaction.json"), `{"transaction_id":"`+identity+`","state":"trial"}`)
			if _, err := CollectPolicy(root, versions); err == nil {
				t.Fatal("malformed transaction was treated as missing rollback evidence")
			}
		})
	}
}

func TestAuditRetentionDoesNotFollowLinkedAuthority(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "active-version.json")
	writeJSONFixture(t, outside, `{"active_version":"v2.0.0"}`)
	if err := os.Symlink(outside, filepath.Join(root, "active-version.json")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if _, err := CollectPolicy(root, filepath.Join(root, "versions")); err == nil {
		t.Fatal("followed a linked authoritative generation pointer")
	}
}
