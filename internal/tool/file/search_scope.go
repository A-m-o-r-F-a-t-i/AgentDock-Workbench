package file

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Exact filename filters do not need to traverse unrelated caches, browser
// databases or dependency trees. Globs retain the existing doublestar matcher.
func walkSearchScope(ctx context.Context, root string, opts SearchOptions, visit fs.WalkDirFunc) error {
	paths := map[string]bool{}
	for _, raw := range opts.IncludeGlobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		pattern := strings.TrimSpace(raw)
		if pattern == "" || strings.ContainsAny(pattern, "*?[]{}\\") || filepath.IsAbs(pattern) {
			return filepath.WalkDir(root, visit)
		}
		candidate := filepath.Join(root, filepath.FromSlash(pattern))
		rel, err := filepath.Rel(root, candidate)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.WalkDir(root, visit)
		}
		paths[candidate] = true
	}
	if len(paths) == 0 {
		return filepath.WalkDir(root, visit)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return filepath.WalkDir(root, visit)
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		// WalkDir does not follow symlinked directories. Preserve that property
		// when jumping directly to an exact descendant filename.
		if !searchPathHasPlainParents(root, filepath.Dir(path)) {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if err = visit(path, nil, err); err != nil {
				return err
			}
			continue
		}
		if info.IsDir() {
			continue
		}
		if err := visit(path, fs.FileInfoToDirEntry(info), nil); err != nil {
			if err == filepath.SkipAll {
				return nil
			}
			return err
		}
	}
	return nil
}

func searchPathHasPlainParents(root, parent string) bool {
	for parent != root {
		info, err := os.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		next := filepath.Dir(parent)
		if next == parent {
			return false
		}
		parent = next
	}
	return true
}

// Only a pattern that excludes every descendant can prune an entire directory.
// For example cache/* must not hide cache/nested/keep.txt, but cache/** may.
func excludedSearchDirectory(rel string, patterns []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
		if pattern == "**" || pattern == "**/*" {
			return true
		}
		for _, suffix := range []string{"/**/*", "/**"} {
			if strings.HasSuffix(pattern, suffix) && globMatch(strings.TrimSuffix(pattern, suffix), rel) {
				return true
			}
		}
	}
	return false
}

func skippedSearchParent(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, part := range parts[:len(parts)-1] {
		if shouldSkipDir(part) {
			return true
		}
	}
	return false
}
