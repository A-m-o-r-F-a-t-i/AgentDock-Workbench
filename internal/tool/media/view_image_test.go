package media

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/workspace"
)

func newMediaTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{AgentDockDefaultDir: root, AgentDockHome: filepath.Join(root, ".agentdock")}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, ws, nil), root
}

func TestViewImageLoadsPathAsMCPImage(t *testing.T) {
	rt, root := newMediaTestService(t)
	imagePath := filepath.Join(root, "tiny.png")
	writeTinyPNG(t, imagePath)

	result, err := rt.ViewImage(context.Background(), ViewImageRequest{Path: "tiny.png", Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	assertMCPImagePayload(t, result, "image/png")
	source, ok := result["source"].(map[string]any)
	if !ok || source["type"] != "path" || source["path"] != "tiny.png" {
		t.Fatalf("path source = %#v", result["source"])
	}
	if _, ok := result["return_mode"]; ok {
		t.Fatalf("view_image should not expose return_mode: %#v", result)
	}
	if _, ok := result["inline"]; ok {
		t.Fatalf("view_image should not expose inline Base64 metadata: %#v", result)
	}
}

func TestViewImageLoadsHTTPURLAsMCPImage(t *testing.T) {
	rt, root := newMediaTestService(t)
	imagePath := filepath.Join(root, "remote.png")
	writeTinyPNG(t, imagePath)
	imageBytes, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes)
	}))
	t.Cleanup(server.Close)

	result, err := rt.ViewImage(context.Background(), ViewImageRequest{URL: server.URL + "/remote.png", Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	assertMCPImagePayload(t, result, "image/png")
	source, ok := result["source"].(map[string]any)
	if !ok || source["type"] != "url" || source["url"] != server.URL+"/remote.png" {
		t.Fatalf("url source = %#v", result["source"])
	}
}

func TestViewImageAutoResizeConvergesOnEffectiveBudget(t *testing.T) {
	rt, root := newMediaTestService(t)
	imagePath := filepath.Join(root, "large-noise.png")
	writeNoisePNG(t, imagePath, 1000, 800)
	maxBytes, maxWidth, maxHeight, quality, autoResize := 750000, 1000, 800, 35, true

	result, err := rt.ViewImage(context.Background(), ViewImageRequest{
		Path: "large-noise.png", Format: "jpeg", MaxBytes: &maxBytes,
		MaxWidth: &maxWidth, MaxHeight: &maxHeight, Quality: &quality, AutoResize: &autoResize,
	})
	if err != nil {
		t.Fatal(err)
	}
	data := assertMCPImagePayload(t, result, "image/jpeg")
	if len(data) > maxBytes || len(data) <= 4096 {
		t.Fatalf("processed bytes=%d, budget=%d", len(data), maxBytes)
	}
	if result["requested_max_bytes"] != maxBytes || result["effective_max_bytes"] != maxBytes {
		t.Fatalf("budget diagnostics = %#v", result)
	}
	metadata, ok := result["image"].(map[string]any)
	if !ok || metadata["size_bytes"] != len(data) {
		t.Fatalf("image metadata = %#v", result["image"])
	}
	width, _ := metadata["width"].(int)
	height, _ := metadata["height"].(int)
	if width <= 242 || height <= 121 {
		t.Fatalf("processed image is still an unusable thumbnail: %dx%d", width, height)
	}
}

func TestViewImageAutoResizeDisabledRejectsWithoutShrinking(t *testing.T) {
	rt, root := newMediaTestService(t)
	imagePath := filepath.Join(root, "fixed-noise.png")
	writeNoisePNG(t, imagePath, 320, 240)
	maxBytes, maxWidth, maxHeight, quality, autoResize := 4096, 160, 120, 35, false

	_, err := rt.ViewImage(context.Background(), ViewImageRequest{
		Path: "fixed-noise.png", Format: "jpeg", MaxBytes: &maxBytes,
		MaxWidth: &maxWidth, MaxHeight: &maxHeight, Quality: &quality, AutoResize: &autoResize,
	})
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "IMAGE_TOO_LARGE" {
		t.Fatalf("error = %T %#v", err, err)
	}
	if toolErr.Details["limit_kind"] != "max_dimensions" || toolErr.Details["actual_width"] != 320 || toolErr.Details["actual_height"] != 240 || toolErr.Details["auto_resize"] != false {
		t.Fatalf("limit details = %#v", toolErr.Details)
	}
}

func TestViewImageImpossibleBudgetStopsAtOnePixel(t *testing.T) {
	rt, root := newMediaTestService(t)
	imagePath := filepath.Join(root, "impossible.png")
	writeNoisePNG(t, imagePath, 8, 8)
	maxBytes, maxWidth, maxHeight, quality, autoResize := 1, 8, 8, 35, true

	_, err := rt.ViewImage(context.Background(), ViewImageRequest{
		Path: "impossible.png", Format: "jpeg", MaxBytes: &maxBytes,
		MaxWidth: &maxWidth, MaxHeight: &maxHeight, Quality: &quality, AutoResize: &autoResize,
	})
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "IMAGE_TOO_LARGE" {
		t.Fatalf("error = %T %#v", err, err)
	}
	if toolErr.Details["limit_kind"] != "requested_max_bytes" || toolErr.Details["effective_max_bytes"] != 1 || toolErr.Details["actual_width"] != 1 || toolErr.Details["actual_height"] != 1 {
		t.Fatalf("limit details = %#v", toolErr.Details)
	}
}

func assertMCPImagePayload(t *testing.T, result Result, mimeType string) []byte {
	t.Helper()
	attachment, ok := result[NativeImageAttachmentKey].(NativeImageAttachment)
	if !ok || len(attachment.Data) == 0 {
		t.Fatalf("native MCP image attachment missing: %#v", result)
	}
	if attachment.MIMEType != mimeType || attachment.Size != len(attachment.Data) {
		t.Fatalf("native MCP image attachment = %#v", attachment)
	}
	metadata, ok := result["image"].(map[string]any)
	if !ok || metadata["artifact_id"] == "" || metadata["size_bytes"] != int64(len(attachment.Data)) {
		t.Fatalf("published image metadata = %#v", result["image"])
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "_mcp_image_base64") || strings.Contains(string(encoded), "\"data\"") {
		t.Fatalf("activity-safe result serialized native bytes: %s", encoded)
	}
	return attachment.Data
}

func writeTinyPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeNoisePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	seed := uint32(1)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			seed = seed*1664525 + 1013904223
			img.SetRGBA(x, y, color.RGBA{R: byte(seed >> 24), G: byte(seed >> 16), B: byte(seed >> 8), A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
