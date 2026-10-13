package buildinfo

import "testing"

func TestFullCommitPreservesImmutableRevision(t *testing.T) {
	previousCommit, previousBuildDate := Commit, BuildDate
	t.Cleanup(func() {
		Commit, BuildDate = previousCommit, previousBuildDate
	})
	Commit = "0123456789abcdef0123456789abcdef01234567"
	BuildDate = "2026-10-13T12:00:00Z"

	info := Current()
	if info.Commit != "0123456789ab" {
		t.Fatalf("display commit = %q", info.Commit)
	}
	if FullCommit() != Commit {
		t.Fatalf("full commit = %q, want %q", FullCommit(), Commit)
	}
	if info.BuildDate != BuildDate {
		t.Fatalf("build date = %q, want %q", info.BuildDate, BuildDate)
	}
}
