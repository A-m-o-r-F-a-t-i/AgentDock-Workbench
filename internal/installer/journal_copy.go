package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Backup budgets reject the whole snapshot; they never silently truncate it.
const (
	backupMaxBytes   int64 = 2 << 30
	backupMaxEntries       = 100000
	backupMaxDepth         = 128
)

type backupDirectoryMode struct {
	path string
	mode fs.FileMode
}
type backupContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r backupContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// A destination of "" computes an integrity digest without modifying files.
// Installation material intentionally keeps its separate default-mode copier.
func copyBackupTree(ctx context.Context, source, destination string) (string, error) {
	sourceDigest, destinationDigest, err := copyBackupTreeVersion(ctx, source, destination, backupNativeMetadataVersion, backupNativeMetadataVersion)
	if err != nil {
		return "", err
	}
	if destination != "" && sourceDigest != destinationDigest {
		return "", errors.New("copied backup metadata digest changed")
	}
	return sourceDigest, nil
}

func copyBackupTreeVersion(ctx context.Context, source, destination string, sourceNativeVersion, destinationNativeVersion int) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if !backupNativeMetadataSupported(sourceNativeVersion) {
		return "", "", fmt.Errorf("unsupported source backup metadata version %d", sourceNativeVersion)
	}
	if destination != "" && !backupNativeMetadataSupported(destinationNativeVersion) {
		return "", "", fmt.Errorf("unsupported destination backup metadata version %d", destinationNativeVersion)
	}
	sourceHash := sha256.New()
	destinationHash := sha256.New()
	var bytes int64
	var entries int
	var directories []backupDirectoryMode
	type metadataChange struct {
		path     string
		metadata *backupNativeMetadata
	}
	var nativeChanges []metadataChange
	metadataBytes := 0
	writeRecord := func(writer io.Writer, relative string, mode fs.FileMode, directory bool, size int64, digest string, native *backupNativeMetadata) error {
		record, err := json.Marshal(struct {
			Path      string                `json:"path"`
			Mode      uint32                `json:"mode"`
			Directory bool                  `json:"directory"`
			Bytes     int64                 `json:"bytes"`
			Digest    string                `json:"digest"`
			Native    *backupNativeMetadata `json:"native,omitempty"`
		}{filepath.ToSlash(relative), uint32(mode.Perm()), directory, size, digest, native})
		if err != nil {
			return err
		}
		_, _ = writer.Write(record)
		_, _ = writer.Write([]byte{'\n'})
		return nil
	}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("backup does not follow links or special files: %s", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entries++; entries > backupMaxEntries || strings.Count(relative, string(filepath.Separator)) > backupMaxDepth {
			return errors.New("backup entry or depth budget exceeded")
		}
		sourceNative, err := readBackupNativeMetadataVersion(path, sourceNativeVersion)
		if err != nil {
			return fmt.Errorf("backup metadata for %s: %w", path, err)
		}
		destinationNative := sourceNative
		if destination != "" && destinationNativeVersion != sourceNativeVersion {
			destinationNative, err = readBackupNativeMetadataVersion(path, destinationNativeVersion)
			if err != nil {
				return fmt.Errorf("destination backup metadata for %s: %w", path, err)
			}
		}
		metadataBytes += backupNativeMetadataSize(sourceNative)
		if destination != "" && destinationNativeVersion != sourceNativeVersion {
			metadataBytes += backupNativeMetadataSize(destinationNative)
		}
		if metadataBytes > 32<<20 {
			return errors.New("backup metadata budget exceeded")
		}
		target := ""
		if destination != "" {
			target = filepath.Join(destination, relative)
			if destinationNative != nil {
				nativeChanges = append(nativeChanges, metadataChange{target, destinationNative})
			}
		}
		size := int64(0)
		digest := ""
		if info.IsDir() {
			if target != "" {
				if err := os.Mkdir(target, 0o700); err != nil {
					return err
				}
				directories = append(directories, backupDirectoryMode{target, info.Mode().Perm()})
			}
		} else {
			if info.Size() > backupMaxBytes-bytes {
				return errors.New("backup byte budget exceeded")
			}
			digest, size, err = copyBackupFile(ctx, path, target, info, backupMaxBytes-bytes)
			if err != nil {
				return err
			}
			bytes += size
		}
		// JSON escapes path separators/control characters unambiguously.
		if err := writeRecord(sourceHash, relative, info.Mode(), info.IsDir(), size, digest, sourceNative); err != nil {
			return err
		}
		if destination != "" {
			if err := writeRecord(destinationHash, relative, info.Mode(), info.IsDir(), size, digest, destinationNative); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	// Apply directory modes only after all children have been written.
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chmod(directories[i].path, directories[i].mode); err != nil {
			return "", "", err
		}
	}
	// Restore ancestors before descendants so inherited Windows permissions do
	// not replace the recorded child descriptor. The private stage ancestor
	// still protects snapshots while original rights are restored below it.
	for _, change := range nativeChanges {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if err := applyBackupNativeMetadataVersion(change.path, change.metadata, destinationNativeVersion); err != nil {
			return "", "", err
		}
	}
	for _, change := range nativeChanges {
		actual, err := readBackupNativeMetadataVersion(change.path, destinationNativeVersion)
		if err != nil || !equalBackupNativeMetadata(actual, change.metadata) {
			return "", "", fmt.Errorf("native backup metadata was not preserved: %s: %w", change.path, errors.Join(err, errors.New(backupNativeMetadataDifference(actual, change.metadata))))
		}
	}
	destinationDigest := ""
	if destination != "" {
		destinationDigest = hex.EncodeToString(destinationHash.Sum(nil))
	}
	return hex.EncodeToString(sourceHash.Sum(nil)), destinationDigest, nil
}

func copyBackupFile(ctx context.Context, source, destination string, before fs.FileInfo, remaining int64) (string, int64, error) {
	input, err := os.Open(source)
	if err != nil {
		return "", 0, err
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil {
		return "", 0, err
	}
	if !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		return "", 0, errors.New("backup source changed before opening")
	}
	digest := sha256.New()
	var output *os.File
	var writer io.Writer = digest
	if destination != "" {
		output, err = os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return "", 0, err
		}
		defer output.Close()
		writer = io.MultiWriter(digest, output)
	}
	count, err := io.CopyBuffer(writer, backupContextReader{ctx, io.LimitReader(input, remaining+1)}, make([]byte, 64<<10))
	if err != nil {
		return "", count, err
	}
	if count > remaining {
		return "", count, errors.New("backup byte budget exceeded")
	}
	after, err := input.Stat()
	if err != nil {
		return "", count, err
	}
	if count != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", count, errors.New("backup source changed during copying")
	}
	if output != nil {
		if err := output.Chmod(before.Mode().Perm()); err != nil {
			return "", count, err
		}
		if err := output.Sync(); err != nil {
			return "", count, err
		}
		if err := output.Close(); err != nil {
			return "", count, err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), count, nil
}
