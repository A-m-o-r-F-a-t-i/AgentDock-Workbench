//go:build windows

package desktopruntime

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestHealthVersionRejectsAnotherCoreVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":"1.1.9"}`))
	}))
	defer server.Close()
	if !testHealth(context.Background(), server.URL) {
		t.Fatal("generic health should accept the healthy endpoint")
	}
	if !testHealthVersion(context.Background(), server.URL, "v1.1.9") {
		t.Fatal("matching Core version was rejected")
	}
	if testHealthVersion(context.Background(), server.URL, "1.1.10") {
		t.Fatal("another Core version satisfied startup readiness")
	}
}

func TestCoreListenAddressPreflightRejectsOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(listener.Addr().String(), "127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	err = ensureCoreListenAddressAvailable(Manifest{Host: "127.0.0.1", Port: port})
	if err == nil || !strings.Contains(err.Error(), listener.Addr().String()) {
		t.Fatalf("occupied address error = %v", err)
	}
}
