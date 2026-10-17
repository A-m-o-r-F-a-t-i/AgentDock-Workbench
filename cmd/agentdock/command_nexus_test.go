package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNexusPairUsesExplicitServiceDataDirectory(t *testing.T) {
	userHome := t.TempDir()
	serviceHome := t.TempDir()
	t.Setenv("AGENTDOCK_HOME", userHome)
	pairServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/nodes/pair" {
			t.Errorf("unexpected pairing request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"node":{"id":"node_test"},"device_token":"token_test"}`))
	}))
	defer pairServer.Close()

	var output bytes.Buffer
	err := run(t.Context(), []string{"nexus", "pair", "--endpoint", pairServer.URL, "--code", "test-code", "--agentdock-home", serviceHome}, &output, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(serviceHome, "nexus", "device.json")
	if _, err := os.Stat(identityPath); err != nil {
		t.Fatalf("service identity not saved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userHome, "nexus", "device.json")); !os.IsNotExist(err) {
		t.Fatalf("pair unexpectedly wrote into login user's home: %v", err)
	}
	if !strings.Contains(output.String(), identityPath) {
		t.Fatalf("pair success must display the real identity path: %s", output.String())
	}
	output.Reset()
	err = run(t.Context(), []string{"nexus", "status", "--json", "--agentdock-home", serviceHome}, &output, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"paired":true`) {
		t.Fatalf("service identity not visible from status: %s", output.String())
	}
	output.Reset()
	err = run(t.Context(), []string{"nexus", "status", "--json"}, &output, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"paired":false`) {
		t.Fatalf("login user's identity must remain separate: %s", output.String())
	}
}
