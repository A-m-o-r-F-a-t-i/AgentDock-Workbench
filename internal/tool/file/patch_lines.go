package file

import "strings"

type patchTextLine struct{ text, ending string }

func patchTextLines(content string) ([]patchTextLine, string) {
	lines := []patchTextLine{}
	crlf, lf := 0, 0
	for content != "" {
		index := strings.IndexByte(content, '\n')
		if index < 0 {
			lines = append(lines, patchTextLine{text: content})
			break
		}
		text, ending := content[:index], "\n"
		if strings.HasSuffix(text, "\r") {
			text, ending = strings.TrimSuffix(text, "\r"), "\r\n"
			crlf++
		} else {
			lf++
		}
		lines = append(lines, patchTextLine{text, ending})
		content = content[index+1:]
	}
	ending := "\n"
	if crlf > lf || crlf == lf && len(lines) > 0 && lines[0].ending == "\r\n" {
		ending = "\r\n"
	}
	return lines, ending
}

// Unchanged rows retain their original bytes, including mixed line endings.
// Only added rows use the dominant ending; BOM and the final-newline convention survive.
func applyUpdateHunks(content string, chunks []patchUpdateChunk, path string) (string, error) {
	if len(chunks) == 0 {
		return content, nil
	}
	bom := ""
	if strings.HasPrefix(content, "\ufeff") {
		bom, content = "\ufeff", strings.TrimPrefix(content, "\ufeff")
	}
	trailing := strings.HasSuffix(content, "\n")
	rows, ending := patchTextLines(content)
	start := 0
	for index, chunk := range chunks {
		old, added, err := parseUpdateChunk(chunk)
		if err != nil {
			return "", err
		}
		lines := make([]string, len(rows))
		for i := range rows {
			lines[i] = rows[i].text
		}
		anchor := -1
		if chunk.Anchor != "" {
			matches := findPatchMatches(lines, []string{chunk.Anchor}, start, false)
			if len(matches) != 1 {
				code := "CONTEXT_NOT_FOUND"
				if len(matches) > 1 {
					code = "AMBIGUOUS_CONTEXT"
				}
				return "", patchContextError("patch anchor must match exactly one location", code, path, index, lines, []string{chunk.Anchor}, matches)
			}
			anchor, start = matches[0], matches[0]+1
		}
		at := len(rows)
		if len(old) == 0 {
			if len(added) == 0 {
				continue
			}
			if anchor >= 0 {
				at = anchor + 1
			}
		} else {
			matches := findPatchMatches(lines, old, start, chunk.EndOfFile)
			if len(matches) != 1 {
				code := "CONTEXT_NOT_FOUND"
				if len(matches) > 1 {
					code = "AMBIGUOUS_CONTEXT"
				}
				return "", patchContextError("patch context must match exactly one location", code, path, index, lines, old, matches)
			}
			at = matches[0]
		}
		materialized := make([]patchTextLine, 0, len(added))
		oldIndex := at
		for _, raw := range chunk.Lines {
			if raw == "" || raw[0] == ' ' {
				materialized = append(materialized, rows[oldIndex])
				oldIndex++
			} else if raw[0] == '-' {
				oldIndex++
			} else if raw[0] == '+' {
				materialized = append(materialized, patchTextLine{raw[1:], ending})
			}
		}
		next := make([]patchTextLine, 0, len(rows)-len(old)+len(materialized))
		next = append(next, rows[:at]...)
		next = append(next, materialized...)
		next = append(next, rows[at+len(old):]...)
		rows, start = next, at+len(materialized)
	}
	var output strings.Builder
	output.WriteString(bom)
	for index, row := range rows {
		output.WriteString(row.text)
		if index+1 < len(rows) {
			if row.ending == "" {
				output.WriteString(ending)
			} else {
				output.WriteString(row.ending)
			}
		} else if trailing {
			if row.ending == "" {
				output.WriteString(ending)
			} else {
				output.WriteString(row.ending)
			}
		}
	}
	return output.String(), nil
}
