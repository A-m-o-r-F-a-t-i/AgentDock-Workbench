package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecuritySkillDocumentRejectsExternalLink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "safe-skill")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.md")
	if err := os.WriteFile(outside, []byte("---\nname: safe-skill\ndescription: test fixture\nversion: 1.0.0\n---\nprivate data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "SKILL.md")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	if _, err := LoadSkillDocument(root); err == nil {
		t.Error("standalone loader followed an external document link")
	}
	if _, err := LoadPortableSkillDocument(root); err == nil {
		t.Error("portable loader followed an external document link")
	}
}
