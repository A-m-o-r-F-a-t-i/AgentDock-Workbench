package file

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func fileMetadataRevision(path string, info os.FileInfo) string {
	identity := fmt.Sprintf("%s\x00%d\x00%d\x00%d", filepath.Clean(path), info.Size(), info.ModTime().UnixNano(), info.Mode())
	return fmt.Sprintf("file_%x", sha256.Sum256([]byte(identity)))
}

func fileContentRevision(path, content string) string {
	return fmt.Sprintf("file_%x", sha256.Sum256([]byte(path+"\x00"+content)))
}

// ReadSourceRevision validates the source without rereading native file content.
// WSL uses its existing native helper and a content digest, preserving Linux
// path and symlink semantics rather than mapping a Linux path to a host drive.
func (svc *Service) ReadSourceRevision(ctx context.Context, request ReadRequest) (string, error) {
	selection, err := selectFileRuntime(request.RuntimeOptions)
	if err != nil {
		return "", err
	}
	if selection.isWSL() {
		maxBytes := 4
		request.StartLine, request.EndLine, request.Offset, request.EndOffset = nil, nil, nil, nil
		request.MaxBytes = &maxBytes
		result, err := svc.readFileWSL(ctx, request, selection)
		if err != nil {
			return "", err
		}
		revision, _ := result["source_revision"].(string)
		return revision, nil
	}
	path := ""
	if strings.HasPrefix(request.Path, "skill://") {
		var release func()
		path, _, release, err = svc.resolveSkillResource(ctx, request.Path)
		if release != nil {
			defer release()
		}
	} else {
		resolved, resolveErr := svc.ws.ResolveExisting(request.Path)
		err = resolveErr
		path = resolved.Abs
	}
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", toolError("NOT_REGULAR_FILE", "The paged source is no longer a regular file.", "validation")
	}
	return fileMetadataRevision(path, info), nil
}

func makeReadTextResult(text, path, revision string, request ReadRequest) (Result, error) {
	if request.SourceRevision != "" && request.SourceRevision != revision {
		return nil, toolErrorDetails("SOURCE_CHANGED_DURING_PAGING", "The file changed after the retained page was created.", "conflict", map[string]any{"path": path, "source_revision": revision, "expected_revision": request.SourceRevision})
	}
	if request.Offset != nil && (request.StartLine != nil || request.EndLine != nil) {
		return nil, toolError("INVALID_ARGUMENT", "Use byte offsets or line selectors, not both in one read.", "validation")
	}
	start, end := 0, len(text)
	if request.Offset != nil {
		if *request.Offset < 0 || *request.Offset > int64(len(text)) {
			return nil, toolError("INVALID_ARGUMENT", "offset is outside the file.", "validation")
		}
		start = int(*request.Offset)
	} else {
		first, last := max(1, intValue(request.StartLine, 1)), intValue(request.EndLine, 0)
		if last > 0 && last < first {
			return nil, toolError("INVALID_ARGUMENT", "end_line precedes start_line.", "validation")
		}
		line := 1
		for index, value := range text {
			if value != '\n' {
				continue
			}
			if line < first {
				start = index + 1
			}
			if last > 0 && line == last {
				end = index
				break
			}
			line++
		}
		if first > strings.Count(text, "\n")+1 {
			start = len(text)
		}
	}
	if request.EndOffset != nil {
		if *request.EndOffset < int64(start) || *request.EndOffset > int64(end) {
			return nil, toolError("INVALID_ARGUMENT", "end_offset is outside the selected file range.", "validation")
		}
		end = int(*request.EndOffset)
	}
	if start < len(text) && !utf8.RuneStart(text[start]) || end < len(text) && !utf8.RuneStart(text[end]) {
		return nil, toolError("INVALID_ARGUMENT", "File byte offsets must fall on UTF-8 character boundaries.", "validation")
	}
	maxBytes := boundedInt(intValue(request.MaxBytes, 262144), 262144, 1, maxTextOutputBytes)
	capturedEnd := min(end, start+maxBytes)
	for capturedEnd > start && capturedEnd < len(text) && !utf8.RuneStart(text[capturedEnd]) {
		capturedEnd--
	}
	if start < end && capturedEnd == start {
		_, width := utf8.DecodeRuneInString(text[start:])
		return nil, toolErrorDetails("OUTPUT_BUDGET_TOO_SMALL", "max_bytes cannot hold the next UTF-8 character.", "validation", map[string]any{"minimum_bytes": width})
	}
	content := text[start:capturedEnd]
	startLine := strings.Count(text[:start], "\n") + 1
	endLine := startLine + strings.Count(content, "\n")
	if strings.HasSuffix(content, "\n") {
		endLine--
	}
	more := capturedEnd < end
	result := Result{
		"path": path, "content": content, "encoding": "utf-8", "size_bytes": len(text),
		"source_revision": revision, "offset": start, "next_offset": capturedEnd, "range_end": end,
		"has_more": more, "source_complete": !more, "truncated": more,
		"start_line": startLine, "end_line": max(startLine, endLine), "total_lines": strings.Count(text, "\n") + 1,
		"range": map[string]any{"byte_start": start, "byte_end": capturedEnd, "start_line": startLine, "end_line": max(startLine, endLine)},
	}
	if more {
		result["truncated_reason"] = "max_bytes"
		result["next_start_line"] = startLine + strings.Count(content, "\n")
	}
	return result, nil
}
