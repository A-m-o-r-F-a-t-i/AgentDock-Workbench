package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

func TestPublishMCPMediaRetainsReadableArtifact(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRz8AAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AgentDockHome: t.TempDir()}
	service := New(cfg, nil, nil)
	artifact, err := service.PublishMCPMedia(context.Background(), data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := artifact["artifact_id"].(string)
	store := publicartifacts.New(cfg.AgentDockHome, "", 0)
	meta, stored, err := store.Read(id, 1024)
	if err != nil || !bytes.Equal(stored, data) || meta.MimeType != "image/png" || meta.Width != 1 || meta.Height != 1 {
		t.Fatalf("published media is not readable by artifact id: metadata=%#v err=%v", meta, err)
	}
}
