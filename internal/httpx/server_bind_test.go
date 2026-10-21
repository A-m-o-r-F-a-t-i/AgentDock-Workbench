package httpx

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/mcp"
)

func TestServeDoesNotClaimListeningWhenBindFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address := listener.Addr().(*net.TCPAddr)
	cfg := testConfig(t)
	cfg.Host = "127.0.0.1"
	cfg.Port = address.Port
	runtime, err := app.NewRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(original)
	err = Serve(context.Background(), mcp.NewServer(runtime, cfg), runtime, cfg)
	if err == nil || !strings.Contains(err.Error(), "listen HTTP server") {
		t.Fatalf("Serve() error = %v", err)
	}
	if strings.Contains(logs.String(), "http server listening") {
		t.Fatalf("server claimed to listen after bind failure: %s", logs.String())
	}
}
