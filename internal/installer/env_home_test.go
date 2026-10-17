package installer

import (
	"path/filepath"
	"testing"

	"github.com/uvwt/agentdock/internal/envstore"
)

func TestWriteCoreEnvironmentKeepsServiceDataDirectories(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "agentdock.env")
	home := filepath.Join(t.TempDir(), "custom-state")
	work := filepath.Join(t.TempDir(), "custom-work")

	if err := writeCoreEnvironment(envFile, Request{
		AgentDockHome:       home,
		AgentDockDefaultDir: work,
	}); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		values, err := envstore.ParseFile(envFile)
		if err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{
			"AGENTDOCK_HOME":        home,
			"AGENTDOCK_DEFAULT_DIR": work,
		} {
			if got := values[key]; got != want {
				t.Errorf("Core service env %s = %q, want %q", key, got, want)
			}
		}
	}
	check()

	if err := writeCoreEnvironment(envFile, Request{}); err != nil {
		t.Fatal(err)
	}
	check()
}
