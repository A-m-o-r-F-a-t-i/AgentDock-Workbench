package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// retainModelMedia runs after duplicate JSON text removal and before page
// construction. Only the projection changes; resultStore retains the raw reply.
func (s *Service) retainModelMedia(ctx context.Context, normalized map[string]any) map[string]any {
	retained := map[string]map[string]any{}
	publish := func(encoded, mimeType string) map[string]any {
		ref := map[string]any{"type": "media_reference", "mimeType": mimeType}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			ref["media_error"] = "invalid base64 media"
			return ref
		}
		digest := sha256.Sum256(data)
		key := mimeType + ":" + hex.EncodeToString(digest[:])
		if previous, ok := retained[key]; ok {
			return cloneResultMap(previous)
		}
		ref["size_bytes"] = len(data)
		ref["sha256"] = hex.EncodeToString(digest[:])
		if s.mediaPublisher == nil {
			ref["media_error"] = "media publisher unavailable; original remains in raw result"
		} else if artifact, publishErr := s.mediaPublisher(ctx, data, mimeType); publishErr != nil {
			ref["media_error"] = "media retention failed; original remains in raw result"
		} else {
			ref["artifact"] = artifact
			if strings.HasPrefix(mimeType, "image/") && artifact["artifact_id"] != nil {
				ref["view_image"] = map[string]any{"tool": "view_image", "arguments": map[string]any{"artifact_id": artifact["artifact_id"]}}
			}
		}
		retained[key] = cloneResultMap(ref)
		return ref
	}
	var walk func(any) any
	walk = func(value any) any {
		switch item := value.(type) {
		case map[string]any:
			out := cloneResultMap(item)
			kind, _ := item["type"].(string)
			mimeType, _ := item["mimeType"].(string)
			if mimeType == "" {
				mimeType, _ = item["mime_type"].(string)
			}
			binaryKey := ""
			if kind == "image" || kind == "audio" {
				binaryKey = "data"
			} else if mimeType != "" && item["blob"] != nil {
				binaryKey = "blob"
			}
			if encoded, ok := item[binaryKey].(string); binaryKey != "" && ok {
				delete(out, binaryKey)
				delete(out, "_meta")
				delete(out, "icons")
				for key, field := range publish(encoded, mimeType) {
					out[key] = field
				}
				if kind != "" {
					out["media_type"] = kind
				}
			}
			for key, field := range out {
				out[key] = walk(field)
			}
			return out
		case []any:
			out := make([]any, len(item))
			for index, field := range item {
				out[index] = walk(field)
			}
			return out
		case string:
			if mimeType, encoded, ok := inlineMediaData(item); ok {
				return publish(encoded, mimeType)
			}
		}
		return value
	}
	return walk(normalized).(map[string]any)
}

func inlineMediaData(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false
	}
	header, encoded, ok := strings.Cut(value[5:], ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return "", "", false
	}
	mimeType := strings.TrimSuffix(header, ";base64")
	return mimeType, encoded, strings.HasPrefix(mimeType, "image/") || strings.HasPrefix(mimeType, "audio/") || strings.HasPrefix(mimeType, "video/")
}
