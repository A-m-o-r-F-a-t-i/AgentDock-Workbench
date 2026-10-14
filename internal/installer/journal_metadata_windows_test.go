package installer

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func nativeBackupTest(t *testing.T) {
	t.Helper()
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("native metadata/recovery fixture runs only on isolated GitHub Windows")
	}
}

func snapshotWindowsBackupVersion(t *testing.T, journal *rollbackJournal, path string, version int) {
	t.Helper()
	if err := os.MkdirAll(journal.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	container, err := os.MkdirTemp(journal.Dir, "snapshot-")
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(container, "payload")
	sourceDigest, backupDigest, err := copyBackupTreeVersion(t.Context(), path, backup, version, version)
	if err != nil {
		t.Fatal(err)
	}
	if sourceDigest != backupDigest {
		t.Fatal("version-matched backup changed its integrity identity")
	}
	next := journal.nextState()
	next.Backups = append(next.Backups, journalBackup{
		Original:      filepath.Clean(path),
		Backup:        backup,
		Existed:       true,
		Digest:        sourceDigest,
		NativeVersion: version,
	})
	if err := journal.commit(next); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsSnapshotDoesNotRequireForeignOwnerAssignment(t *testing.T) {
	nativeBackupTest(t)
	source := filepath.Join(os.Getenv("WINDIR"), "win.ini")
	metadata, err := readBackupNativeMetadataVersion(source, backupNativeMetadataOwnerVersion)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(metadata.Security)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if owner == nil || owner.Equals(current.User.Sid) {
		t.Skip("Windows fixture owner is assignable by the current token")
	}

	j := newJournal(t.TempDir(), "foreign-owner")
	if err = j.Snapshot(source); err != nil {
		t.Fatalf("snapshot of a readable file with a foreign owner failed: %v", err)
	}
	if len(j.Backups) != 1 {
		t.Fatalf("unexpected backup count: %d", len(j.Backups))
	}
	backupMetadata, err := readBackupNativeMetadataVersion(j.Backups[0].Backup, backupNativeMetadataOwnerVersion)
	if err != nil {
		t.Fatal(err)
	}
	backupDescriptor, err := windows.SecurityDescriptorFromString(backupMetadata.Security)
	if err != nil {
		t.Fatal(err)
	}
	backupOwner, _, err := backupDescriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if backupOwner == nil || backupOwner.Equals(owner) {
		t.Fatalf("snapshot assigned the foreign source owner: source=%v backup=%v", owner, backupOwner)
	}
	controlPath := filepath.Join(filepath.Dir(j.Backups[0].Backup), "owner-control")
	if err = os.WriteFile(controlPath, []byte("control"), 0o600); err != nil {
		t.Fatal(err)
	}
	controlMetadata, err := readBackupNativeMetadataVersion(controlPath, backupNativeMetadataOwnerVersion)
	if err != nil {
		t.Fatal(err)
	}
	controlDescriptor, err := windows.SecurityDescriptorFromString(controlMetadata.Security)
	if err != nil {
		t.Fatal(err)
	}
	controlOwner, _, err := controlDescriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if controlOwner == nil || !backupOwner.Equals(controlOwner) {
		t.Fatalf("snapshot changed the staging identity: backup=%v sibling=%v current=%v", backupOwner, controlOwner, current.User.Sid)
	}
}

func TestWindowsCurrentBackupMetadataUsesBinarySecurityDescriptor(t *testing.T) {
	nativeBackupTest(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := readBackupNativeMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(metadata.Security, backupBinarySecurityPrefix) {
		t.Fatalf("current metadata does not use the binary descriptor format: %q", metadata.Security)
	}
	decoded, storage, err := decodeBackupSecurityDescriptor(metadata.Security, backupNativeMetadataVersion)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.String() != expected.String() {
		t.Fatalf("binary descriptor changed the DACL: expected=%q actual=%q", expected.String(), decoded.String())
	}
	runtime.KeepAlive(storage)

	metadata.Security = backupBinarySecurityPrefix + "invalid"
	if err := applyBackupNativeMetadata(path, metadata); err == nil || !strings.Contains(err.Error(), "encoded backup security descriptor") {
		t.Fatalf("malformed binary descriptor was accepted: %v", err)
	}
}

func TestWindowsBackupMetadataCanonicalizationIgnoresACLSlack(t *testing.T) {
	nativeBackupTest(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := readBackupNativeMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(metadata.Security, backupBinarySecurityPrefix))
	if err != nil || len(raw) < 20 {
		t.Fatalf("decode current metadata: %v", err)
	}
	daclOffset := int(binary.LittleEndian.Uint32(raw[16:20]))
	if daclOffset < 20 || daclOffset+8 > len(raw) {
		t.Fatalf("invalid DACL offset %d for %d bytes", daclOffset, len(raw))
	}
	aclSize := int(binary.LittleEndian.Uint16(raw[daclOffset+2 : daclOffset+4]))
	if daclOffset+aclSize != len(raw) || aclSize > 0xffff-4 {
		t.Fatalf("unexpected DACL layout: offset=%d size=%d total=%d", daclOffset, aclSize, len(raw))
	}
	padded := append(append([]byte(nil), raw...), 0xaa, 0xbb, 0xcc, 0xdd)
	binary.LittleEndian.PutUint16(padded[daclOffset+2:daclOffset+4], uint16(aclSize+4))
	paddedMetadata := *metadata
	paddedMetadata.Security = backupBinarySecurityPrefix + base64.RawStdEncoding.EncodeToString(padded)

	if metadata.Security == paddedMetadata.Security {
		t.Fatal("fixture did not change the binary descriptor")
	}
	if !equalBackupNativeMetadata(metadata, &paddedMetadata) {
		t.Fatalf("ACL slack changed semantic metadata: %s", backupNativeMetadataDifference(metadata, &paddedMetadata))
	}
	canonical, err := canonicalBackupNativeMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	canonicalPadded, err := canonicalBackupNativeMetadata(&paddedMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if *canonical != *canonicalPadded {
		t.Fatalf("canonical metadata changed with ACL slack: %#v %#v", canonical, canonicalPadded)
	}
}

func TestWindowsBackupMetadataCanonicalizationIgnoresIncidentalIdentity(t *testing.T) {
	nativeBackupTest(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := readBackupNativeMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedStorage, err := decodeBackupSecurityDescriptor(metadata.Security, backupNativeMetadataVersion)
	if err != nil {
		t.Fatal(err)
	}
	daclOnly, daclStorage, err := backupDACLOnlySecurityDescriptor(decoded)
	if err != nil {
		t.Fatal(err)
	}
	daclEncoded, err := encodeBackupSecurityDescriptor(daclOnly)
	if err != nil {
		t.Fatal(err)
	}
	withIdentity, identityStorage, err := backupSecurityWithTargetIdentity(path, decoded)
	if err != nil {
		t.Fatal(err)
	}
	identityEncoded, err := encodeBackupSecurityDescriptor(withIdentity)
	if err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(identityStorage)
	runtime.KeepAlive(daclStorage)
	runtime.KeepAlive(decodedStorage)
	if daclEncoded == identityEncoded {
		t.Fatal("fixture did not add incidental owner and group fields")
	}

	daclMetadata := *metadata
	daclMetadata.Security = daclEncoded
	identityMetadata := *metadata
	identityMetadata.Security = identityEncoded
	canonicalDACL, err := canonicalBackupNativeMetadata(&daclMetadata)
	if err != nil {
		t.Fatal(err)
	}
	canonicalIdentity, err := canonicalBackupNativeMetadata(&identityMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(canonicalIdentity.Security, "O:") || strings.Contains(canonicalIdentity.Security, "G:") {
		t.Fatalf("canonical metadata retained incidental identity: %q", canonicalIdentity.Security)
	}
	if *canonicalDACL != *canonicalIdentity || !equalBackupNativeMetadata(&daclMetadata, &identityMetadata) {
		t.Fatalf("incidental identity changed semantic metadata: %s", backupNativeMetadataDifference(&daclMetadata, &identityMetadata))
	}
}

func TestWindowsBackupPreservesDACLAndAttributes(t *testing.T) {
	nativeBackupTest(t)
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "inherited", true: "protected"}[protected], func(t *testing.T) {
			root := t.TempDir()
			original := filepath.Join(root, "original")
			if err = os.Mkdir(original, 0700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(original, "settings.json")
			if err = os.WriteFile(source, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			metadata, err := readBackupNativeMetadata(source)
			if err != nil {
				t.Fatal(err)
			}
			if protected {
				descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + token.User.Sid.String() + ")(A;;FR;;;BU)")
				if err != nil {
					t.Fatal(err)
				}
				metadata.Security, err = encodeBackupSecurityDescriptor(descriptor)
				if err != nil {
					t.Fatal(err)
				}
			}
			metadata.Attributes = windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_ARCHIVE
			if err = applyBackupNativeMetadata(source, metadata); err != nil {
				t.Fatal(err)
			}
			expected, err := readBackupNativeMetadata(source)
			if err != nil {
				t.Fatal(err)
			}
			j := newJournal(filepath.Join(root, "state"), "native-backup")
			if err = j.Snapshot(original); err != nil {
				t.Fatal(err)
			}
			name, _ := windows.UTF16PtrFromString(source)
			if err = windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_NORMAL); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(source, []byte("new-generation"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = j.Restore(t.Context(), Request{}); err != nil {
				t.Fatal(err)
			}
			actual, err := readBackupNativeMetadata(source)
			if err != nil || !equalBackupNativeMetadata(actual, expected) {
				t.Fatalf("permissions or attributes changed: expected=%+v actual=%+v error=%v", expected, actual, err)
			}
			assertJournalFile(t, source, "original")
			if err = j.Restore(t.Context(), Request{}); err != nil {
				t.Fatal(err)
			}
			// Only this temporary fixture is cleared; readonly must not hinder test cleanup.
			_ = windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_NORMAL)
		})
	}
}

func TestWindowsRecoveryProjectsVersionOneBackupToCurrentMetadata(t *testing.T) {
	nativeBackupTest(t)
	root := t.TempDir()
	original := filepath.Join(root, "application")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(original, "settings.json")
	if err := os.WriteFile(source, []byte("version-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := readBackupNativeMetadata(source)
	if err != nil {
		t.Fatal(err)
	}
	beforeIdentity, err := readBackupNativeMetadataVersion(source, backupNativeMetadataOwnerVersion)
	if err != nil {
		t.Fatal(err)
	}

	journal := newJournal(filepath.Join(root, "state"), "version-one-projection")
	snapshotWindowsBackupVersion(t, journal, original, backupNativeMetadataOwnerVersion)
	if err := os.WriteFile(source, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := readBackupNativeMetadata(source)
	if err != nil {
		t.Fatal(err)
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	changed.Security = "D:P(A;;FA;;;" + current.User.Sid.String() + ")"
	if err := applyBackupNativeMetadata(source, changed); err != nil {
		t.Fatal(err)
	}

	if err := journal.Restore(t.Context(), Request{}); err != nil {
		t.Fatal(err)
	}
	assertJournalFile(t, source, "version-one")
	actual, err := readBackupNativeMetadata(source)
	if err != nil || !equalBackupNativeMetadata(actual, expected) {
		t.Fatalf("projected recovery metadata changed: %s %v", backupNativeMetadataDifference(actual, expected), err)
	}
	afterIdentity, err := readBackupNativeMetadataVersion(source, backupNativeMetadataOwnerVersion)
	if err != nil {
		t.Fatal(err)
	}
	beforeDescriptor, _ := windows.SecurityDescriptorFromString(beforeIdentity.Security)
	afterDescriptor, _ := windows.SecurityDescriptorFromString(afterIdentity.Security)
	beforeOwner, _, _ := beforeDescriptor.Owner()
	afterOwner, _, _ := afterDescriptor.Owner()
	if beforeOwner == nil || afterOwner == nil || !beforeOwner.Equals(afterOwner) {
		t.Fatalf("projection changed target owner: before=%v after=%v", beforeOwner, afterOwner)
	}
	beforeGroup, _, _ := beforeDescriptor.Group()
	afterGroup, _, _ := afterDescriptor.Group()
	if beforeGroup == nil || afterGroup == nil || !beforeGroup.Equals(afterGroup) {
		t.Fatalf("projection changed target group: before=%v after=%v", beforeGroup, afterGroup)
	}
	entry := journal.RestoreEntries[0]
	if entry.NativeVersion != backupNativeMetadataOwnerVersion || entry.PreparedNativeVersion != backupNativeMetadataVersion || len(entry.PreparedDigest) != 64 {
		t.Fatalf("version-one backup was not projected to current metadata: %+v", entry)
	}
}

func TestWindowsRecoveryProjectsVersionTwoBackupToBinaryMetadata(t *testing.T) {
	nativeBackupTest(t)
	root := t.TempDir()
	original := filepath.Join(root, "application")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(original, "settings.json")
	if err := os.WriteFile(source, []byte("version-two"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := readBackupNativeMetadata(source)
	if err != nil {
		t.Fatal(err)
	}
	journal := newJournal(filepath.Join(root, "state"), "version-two-projection")
	snapshotWindowsBackupVersion(t, journal, original, backupNativeMetadataDACLVersion)
	if err := os.WriteFile(source, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := journal.Restore(t.Context(), Request{}); err != nil {
		t.Fatal(err)
	}
	assertJournalFile(t, source, "version-two")
	actual, err := readBackupNativeMetadata(source)
	if err != nil || !equalBackupNativeMetadata(actual, expected) {
		t.Fatalf("version-two projection changed metadata: %s %v", backupNativeMetadataDifference(actual, expected), err)
	}
	entry := journal.RestoreEntries[0]
	if entry.NativeVersion != backupNativeMetadataDACLVersion || entry.PreparedNativeVersion != backupNativeMetadataVersion || len(entry.PreparedDigest) != 64 {
		t.Fatalf("version-two backup was not projected to binary metadata: %+v", entry)
	}
}
func TestWindowsRecoveryResumesVersionOnePreparedJournal(t *testing.T) {
	nativeBackupTest(t)
	root := t.TempDir()
	original := filepath.Join(root, "application")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(original, "settings.json")
	if err := os.WriteFile(source, []byte("version-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := newJournal(filepath.Join(root, "state"), "version-one-prepared")
	snapshotWindowsBackupVersion(t, journal, original, backupNativeMetadataOwnerVersion)
	if err := os.WriteFile(source, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	stage, err := os.MkdirTemp(root, ".agentdock-restore-")
	if err != nil {
		t.Fatal(err)
	}
	entry := journalRestoreEntry{
		Original: filepath.Clean(original),
		Backup:   journal.Backups[0].Backup,
		Digest:   journal.Backups[0].Digest,
		Existed:  true,
		Stage:    stage,
		Phase:    "prepared",
	}
	if err := os.WriteFile(filepath.Join(stage, "owner.json"), journal.stageOwner(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceDigest, preparedDigest, err := copyBackupTreeVersion(t.Context(), entry.Backup, filepath.Join(stage, "prepared"), backupNativeMetadataOwnerVersion, backupNativeMetadataOwnerVersion)
	if err != nil {
		t.Fatal(err)
	}
	if sourceDigest != entry.Digest || preparedDigest != entry.Digest {
		t.Fatal("legacy preparation does not match its journal digest")
	}
	next := journal.nextState()
	next.RestoreEntries = []journalRestoreEntry{entry}
	next.RestoreStatus = "prepared"
	if err := journal.commit(next); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadJournal(filepath.Join(root, "state"), "version-one-prepared")
	if err != nil {
		t.Fatal(err)
	}
	if err := loaded.Restore(t.Context(), Request{}); err != nil {
		t.Fatal(err)
	}
	assertJournalFile(t, source, "version-one")
	entry = loaded.RestoreEntries[0]
	if entry.NativeVersion != backupNativeMetadataOwnerVersion || entry.PreparedNativeVersion != backupNativeMetadataOwnerVersion || entry.PreparedDigest != entry.Digest {
		t.Fatalf("legacy prepared journal was not normalized safely: %+v", entry)
	}
}

func TestWindowsBackupRejectsADSAndEABeforeJournalCommit(t *testing.T) {
	nativeBackupTest(t)
	for _, kind := range []string{"ads", "ea"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "original.txt")
			if err := os.WriteFile(source, []byte("keep-original"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "ads" {
				if err := os.WriteFile(source+":retained", []byte("must-not-drop"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				name, _ := windows.UTF16PtrFromString(source)
				handle, err := windows.CreateFile(name, windows.FILE_WRITE_EA, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				// FILE_FULL_EA_INFORMATION: next/flags/name length/value length/name/NUL/value.
				data := []byte{0, 0, 0, 0, 0, 4, 3, 0, 't', 'e', 's', 't', 0, 'e', 'a', '1'}
				var status windows.IO_STATUS_BLOCK
				result, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtSetEaFile").Call(uintptr(handle), uintptr(unsafe.Pointer(&status)), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
				windows.CloseHandle(handle)
				if result != 0 {
					t.Fatalf("NTFS EA fixture failed: %#x", result)
				}
			}
			j := newJournal(filepath.Join(root, "state"), kind)
			err := j.Snapshot(source)
			if err == nil || !strings.Contains(err.Error(), map[string]string{"ads": "alternate data streams", "ea": "extended attributes"}[kind]) {
				t.Fatalf("unsupported metadata was lost: %v", err)
			}
			if len(j.Backups) != 0 {
				t.Fatal("unsupported backup was committed")
			}
			assertJournalFile(t, source, "keep-original")
			if kind == "ads" {
				assertJournalFile(t, source+":retained", "must-not-drop")
			}
		})
	}
}

func TestWindowsRecoverySharingViolationKeepsOriginalAndResumes(t *testing.T) {
	nativeBackupTest(t)
	j, state, original := journalFixture(t)
	source := filepath.Join(original, "secret")
	name, _ := windows.UTF16PtrFromString(source)
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = j.Restore(t.Context(), Request{})
	windows.CloseHandle(handle)
	if err == nil {
		t.Fatal("native open file did not block replacement as expected")
	}
	assertJournalFile(t, source, "current-secret")
	loaded, err := loadJournal(state, "recovery-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = loaded.Restore(t.Context(), Request{}); err != nil {
		t.Fatal(err)
	}
	assertJournalFile(t, source, "original-secret")
}

func TestWindowsRecoveryRejectsChangedACLBeforeMovingCurrent(t *testing.T) {
	nativeBackupTest(t)
	j, _, original := journalFixture(t)
	if err := j.prepareRestore(t.Context()); err != nil {
		t.Fatal(err)
	}
	prepared := filepath.Join(j.RestoreEntries[0].Stage, "prepared", "secret")
	metadata, err := readBackupNativeMetadata(prepared)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Attributes ^= windows.FILE_ATTRIBUTE_HIDDEN
	if err = applyBackupNativeMetadata(prepared, metadata); err != nil {
		t.Fatal(err)
	}
	if err = j.Restore(t.Context(), Request{}); err == nil {
		t.Fatal("changed metadata passed integrity check")
	}
	assertJournalFile(t, filepath.Join(original, "secret"), "current-secret")
	if _, err = os.Stat(j.Dir); errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery evidence removed")
	}
}

func TestWindowsBackupPreservesLegacyAndAutomaticInheritanceModes(t *testing.T) {
	nativeBackupTest(t)
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "automatic"}[automatic], func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source.txt")
			if err := os.WriteFile(source, []byte("retained"), 0600); err != nil {
				t.Fatal(err)
			}
			metadata, err := readBackupNativeMetadata(source)
			if err != nil {
				t.Fatal(err)
			}
			decoded, storage, err := decodeBackupSecurityDescriptor(metadata.Security, backupNativeMetadataVersion)
			if err != nil {
				t.Fatal(err)
			}
			descriptor, err := decoded.ToAbsolute()
			runtime.KeepAlive(storage)
			if err != nil {
				t.Fatal(err)
			}
			flags := windows.SECURITY_DESCRIPTOR_CONTROL(windows.SE_DACL_AUTO_INHERITED)
			value := windows.SECURITY_DESCRIPTOR_CONTROL(0)
			if automatic {
				value = flags
			}
			if err = descriptor.SetControl(flags, value); err != nil {
				t.Fatal(err)
			}
			relative, err := descriptor.ToSelfRelative()
			if err != nil {
				t.Fatal(err)
			}
			metadata.Security, err = encodeBackupSecurityDescriptor(relative)
			if err != nil {
				t.Fatal(err)
			}
			if err = applyBackupNativeMetadata(source, metadata); err != nil {
				t.Fatal(err)
			}
			expected, err := readBackupNativeMetadata(source)
			if err != nil {
				t.Fatal(err)
			}
			parsed, parsedStorage, err := decodeBackupSecurityDescriptor(expected.Security, backupNativeMetadataVersion)
			if err != nil {
				t.Fatal(err)
			}
			control, _, err := parsed.Control()
			runtime.KeepAlive(parsedStorage)
			if err != nil || (control&flags != 0) != automatic {
				t.Fatalf("fixture did not establish requested native mode: %#x %v", control, err)
			}
			j := newJournal(filepath.Join(root, "state"), "inheritance-mode")
			if err = j.Snapshot(source); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(source, []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			if err = j.Restore(t.Context(), Request{}); err != nil {
				t.Fatal(err)
			}
			actual, err := readBackupNativeMetadata(source)
			if err != nil || !equalBackupNativeMetadata(actual, expected) {
				t.Fatalf("native ACL mode changed: %s %v", backupNativeMetadataDifference(actual, expected), err)
			}
		})
	}
}
