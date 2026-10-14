//go:build !windows

package installer

import "fmt"

const backupNativeMetadataVersion = 0

type backupNativeMetadata struct{}

func backupNativeMetadataSupported(version int) bool { return version == 0 }
func readBackupNativeMetadata(path string) (*backupNativeMetadata, error) {
	return readBackupNativeMetadataVersion(path, backupNativeMetadataVersion)
}
func readBackupNativeMetadataVersion(_ string, version int) (*backupNativeMetadata, error) {
	if !backupNativeMetadataSupported(version) {
		return nil, fmt.Errorf("unsupported backup metadata version %d", version)
	}
	return nil, nil
}
func applyBackupNativeMetadata(path string, metadata *backupNativeMetadata) error {
	return applyBackupNativeMetadataVersion(path, metadata, backupNativeMetadataVersion)
}
func applyBackupNativeMetadataVersion(_ string, _ *backupNativeMetadata, version int) error {
	if !backupNativeMetadataSupported(version) {
		return fmt.Errorf("unsupported backup metadata version %d", version)
	}
	return nil
}
func backupNativeMetadataSize(*backupNativeMetadata) int { return 0 }
func canonicalBackupNativeMetadataVersion(value *backupNativeMetadata, version int) (*backupNativeMetadata, error) {
	if !backupNativeMetadataSupported(version) {
		return nil, fmt.Errorf("unsupported backup metadata version %d", version)
	}
	return value, nil
}
func canonicalBackupNativeMetadata(value *backupNativeMetadata) (*backupNativeMetadata, error) {
	return canonicalBackupNativeMetadataVersion(value, backupNativeMetadataVersion)
}
func equalBackupNativeMetadataVersion(a, b *backupNativeMetadata, version int) bool {
	return backupNativeMetadataSupported(version) && a == b
}
func equalBackupNativeMetadata(a, b *backupNativeMetadata) bool {
	return equalBackupNativeMetadataVersion(a, b, backupNativeMetadataVersion)
}
func backupNativeMetadataDifferenceVersion(a, b *backupNativeMetadata, version int) string {
	if !backupNativeMetadataSupported(version) {
		return fmt.Sprintf("unsupported backup metadata version %d", version)
	}
	return "metadata mismatch"
}
func backupNativeMetadataDifference(a, b *backupNativeMetadata) string {
	return backupNativeMetadataDifferenceVersion(a, b, backupNativeMetadataVersion)
}
