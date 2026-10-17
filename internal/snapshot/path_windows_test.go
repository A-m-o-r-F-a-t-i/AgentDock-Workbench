//go:build windows

package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWatchPathWindowsNativeRepresentations(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{"drive", "C:\\selected\\directory", "\\\\?\\C:\\selected\\directory"},
		{"share", "\\\\server\\share\\selected", "\\\\?\\UNC\\server\\share\\selected"},
		{"drive_root", "C:\\", "\\\\?\\C:\\"},
		{"share_root", "\\\\server\\share", "\\\\?\\UNC\\server\\share"},
	} {
		t.Run(test.name, func(t *testing.T) {
			extended, err := watchLongPath(test.input)
			if err != nil || extended != test.want {
				t.Fatalf("extended watch path: %q %v, want %q", extended, err, test.want)
			}
			native, err := normalizedWatchTarget(extended)
			if err != nil || native != test.input {
				t.Fatalf("normalized watch target: %q %v, want %q", native, err, test.input)
			}
		})
	}
	for _, input := range []string{
		"", "relative", "C:relative", "\\\\server",
		"\\\\.\\pipe\\name", "\\\\?\\C:\\selected",
		"\\\\?\\GLOBALROOT\\Device\\HarddiskVolume1", "\\??\\C:\\selected",
	} {
		if target, err := watchLongPath(input); err == nil || target != "" || os.IsNotExist(err) {
			t.Errorf("invalid native input accepted or reported missing: %q -> %q %v", input, target, err)
		}
	}
	for _, result := range []string{
		"C:\\selected", "\\\\?\\C:relative", "\\\\?\\UNC\\server",
		"\\\\?\\Volume{00000000-0000-0000-0000-000000000000}\\selected",
		"\\\\?\\GLOBALROOT\\Device\\HarddiskVolume1\\selected",
	} {
		if target, err := normalizedWatchTarget(result); err == nil || target != "" || os.IsNotExist(err) {
			t.Errorf("unsupported final namespace accepted or reported missing: %q -> %q %v", result, target, err)
		}
	}
	// The platform limit counts UTF-16 units, not UTF-8 bytes or Go runes.
	tooLong := "\\\\?\\C:\\" + strings.Repeat("🙂", windows.MAX_LONG_PATH/2)
	if _, err := watchPathPointer(tooLong); !errors.Is(err, windows.ERROR_FILENAME_EXCED_RANGE) {
		t.Fatalf("oversized UTF-16 path was not rejected: %v", err)
	}
}

func TestWatchPathWindowsResolvesJunctionAndReleasesHandle(t *testing.T) {
	owner, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(owner, "external target")
	child := filepath.Join(target, "nested directory")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(owner, "junction alias")
	createWatchJunctionForTest(t, owner, alias, target)
	for _, path := range []string{alias, filepath.Join(alias, "nested directory")} {
		want := target
		if path != alias {
			want = child
		}
		actual, err := watchPathTarget(path)
		if err != nil || !sameWatchPath(actual, want) {
			t.Fatalf("junction target: %q %v, want %q", actual, err, want)
		}
	}
	info, err := os.Lstat(alias)
	if err != nil {
		t.Fatal(err)
	}
	if isAlias, err := isWatchAlias(alias, info); err != nil || !isAlias {
		t.Fatalf("directory junction was not identified: %t %v", isAlias, err)
	}
	info, err = os.Lstat(child)
	if err != nil {
		t.Fatal(err)
	}
	if isAlias, err := isWatchAlias(child, info); err != nil || isAlias {
		t.Fatalf("ordinary directory was misidentified: %t %v", isAlias, err)
	}
	// Both metadata queries have finished. Their temporary handles must not
	// prevent replacing the directory whose path they just resolved.
	if err := os.Rename(target, filepath.Join(owner, "retired target")); err != nil {
		t.Fatalf("target resolution retained a directory handle: %v", err)
	}
	info, err = os.Lstat(alias)
	if err != nil {
		t.Fatal(err)
	}
	if isAlias, err := isWatchAlias(alias, info); err != nil || !isAlias {
		t.Fatalf("dangling junction lost its alias identity: %t %v", isAlias, err)
	}
	if target, err := watchPathTarget(alias); !os.IsNotExist(err) || target != "" {
		t.Fatalf("dangling junction resolved to an existing lexical ancestor: %q %v", target, err)
	}
}

func TestWatchPathWindowsLoopDoesNotBecomeMissing(t *testing.T) {
	owner, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(owner, "initial target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(owner, "first alias"), filepath.Join(owner, "second alias")
	createWatchJunctionForTest(t, owner, first, target)
	createWatchJunctionForTest(t, owner, second, first)
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	// Moving the second entry to its own stored target creates a directory
	// junction loop without changing system policy or touching other paths.
	if err := os.Rename(second, first); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(first)
	if err != nil {
		t.Fatal(err)
	}
	if isAlias, err := isWatchAlias(first, info); err != nil || !isAlias {
		t.Fatalf("looping junction was not recognized as an alias: %t %v", isAlias, err)
	}
	if target, err := watchPathTarget(first); err == nil || os.IsNotExist(err) || target != "" {
		t.Fatalf("junction loop would permit a missing-ancestor fallback: %q %v", target, err)
	}
}

func TestWatchPathWindowsLongDirectory(t *testing.T) {
	owner, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := owner
	for range 8 {
		path = filepath.Join(path, strings.Repeat("long", 10))
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := watchPathTarget(path)
	if err != nil || !sameWatchPath(resolved, path) {
		t.Fatalf("long directory target: %q %v, want %q", resolved, err, path)
	}
	missing := filepath.Join(path, "absent")
	if target, err := watchPathTarget(missing); !os.IsNotExist(err) || target != "" {
		t.Fatalf("missing target lost its error classification: %q %v", target, err)
	}
}
