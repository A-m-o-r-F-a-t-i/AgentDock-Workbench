package media

import (
	"context"
	"strings"
)

func (s *Service) ViewImage(ctx context.Context, request ViewImageRequest) (Result, error) {
	loaded, err := s.loadImageSource(ctx, request)
	if err != nil {
		return nil, err
	}
	info, err := identifyImage(loaded.Data)
	if err != nil {
		return nil, toolError("BINARY_FILE", "source is not a supported image", "validation")
	}

	requestedMaxBytes := intValue(request.MaxBytes, defaultViewImageBytes)
	if requestedMaxBytes <= 0 {
		requestedMaxBytes = defaultViewImageBytes
	}
	if requestedMaxBytes > hardViewImageBytes {
		requestedMaxBytes = hardViewImageBytes
	}
	// Image processing follows the caller's limit. The response adapter owns
	// the separate native-attachment budget and can fall back to the artifact
	// link without forcing every image down to that transport threshold.
	effectiveMaxBytes := requestedMaxBytes
	maxWidth := intValue(request.MaxWidth, 1280)
	maxHeight := intValue(request.MaxHeight, 1280)
	format := request.Format
	if strings.TrimSpace(format) == "" {
		format = "jpeg"
	}
	quality := intValue(request.Quality, 72)
	crop := imageCropFromRequest(request.Crop)
	autoResize := boolValue(request.AutoResize, true)

	prepared, preparedInfo, original, warnings, ok := prepareImageBytes(loaded.Data, crop, effectiveMaxBytes, maxWidth, maxHeight, format, quality, autoResize)
	if original != nil {
		if bytes, exists := original["bytes"]; exists {
			original["size_bytes"] = bytes
			delete(original, "bytes")
		}
	} else {
		original = map[string]any{"size_bytes": len(loaded.Data), "width": info.Width, "height": info.Height, "mime_type": info.MIME}
	}
	if !ok || len(prepared) > effectiveMaxBytes || preparedInfo.Width > maxWidth || preparedInfo.Height > maxHeight {
		return nil, toolErrorDetails("IMAGE_TOO_LARGE", "image exceeds the effective processing limits", "validation", imageLimitDetails(requestedMaxBytes, effectiveMaxBytes, maxWidth, maxHeight, len(prepared), preparedInfo, autoResize, warnings))
	}
	resized := preparedInfo.Width != info.Width || preparedInfo.Height != info.Height || len(prepared) != len(loaded.Data)

	image := imageMetadata(loaded.Name, preparedInfo, len(prepared))
	published, publishErr := s.publishImageArtifact(ctx, prepared, loaded.Name, preparedInfo, 0)
	if publishErr == nil {
		image = artifactResult(published)
		image["link_available"] = published.URL != ""
	} else {
		image["link_available"] = false
		warnings = append(warnings, "processed image link could not be published; native MCP image delivery remains available")
	}
	result := Result{
		"source":              loaded.Source,
		"image":               image,
		"original":            original,
		"resized":             resized,
		"warnings":            warnings,
		"requested_max_bytes": requestedMaxBytes,
		"effective_max_bytes": effectiveMaxBytes,
	}
	attachMCPImage(result, prepared, preparedInfo.MIME)
	return result, nil
}

func imageLimitKind(requestedMaxBytes, effectiveMaxBytes, maxWidth, maxHeight int, info imageInfo) string {
	if info.Width > maxWidth || info.Height > maxHeight {
		return "max_dimensions"
	}
	return "requested_max_bytes"
}

func imageLimitDetails(requestedMaxBytes, effectiveMaxBytes, maxWidth, maxHeight, actualBytes int, info imageInfo, autoResize bool, warnings []string) map[string]any {
	return map[string]any{
		"bytes":               actualBytes,
		"actual_bytes":        actualBytes,
		"actual_width":        info.Width,
		"actual_height":       info.Height,
		"requested_max_bytes": requestedMaxBytes,
		"effective_max_bytes": effectiveMaxBytes,
		"max_bytes":           effectiveMaxBytes,
		"max_width":           maxWidth,
		"max_height":          maxHeight,
		"limit_kind":          imageLimitKind(requestedMaxBytes, effectiveMaxBytes, maxWidth, maxHeight, info),
		"auto_resize":         autoResize,
		"warnings":            warnings,
	}
}
