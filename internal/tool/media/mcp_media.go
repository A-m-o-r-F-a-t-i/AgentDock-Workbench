package media

import (
	"context"
	"mime"
	"strings"

	"github.com/uvwt/agentdock/internal/httpx/requestmeta"
	"github.com/uvwt/agentdock/internal/publicartifacts"
)

// PublishMCPMedia uses the same retained artifact store as view_image, so removing
// inline Base64 from the model projection does not discard the original media.
func (s *Service) PublishMCPMedia(ctx context.Context, data []byte, mimeType string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	filename := "mcp-media"
	if extensions, err := mime.ExtensionsByType(mimeType); err == nil && len(extensions) > 0 {
		filename += extensions[0]
	}
	store := publicartifacts.New(s.cfg.AgentDockHome, s.cfg.OAuthServerURL, s.cfg.Port)
	published, err := store.PublishBytes(publicartifacts.PublishBytesRequest{
		Filename: filename, Data: data, MimeType: strings.TrimSpace(mimeType),
		BaseURL: requestmeta.BaseURL(ctx),
	})
	if err != nil {
		return nil, err
	}
	return artifactResult(published), nil
}
