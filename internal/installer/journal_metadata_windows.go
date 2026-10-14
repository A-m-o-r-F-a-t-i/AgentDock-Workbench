package installer

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"runtime"
	"strings"
	"unicode/utf16"
	"unsafe"
)

const backupNativeMetadataVersion = 3
const backupNativeMetadataDACLVersion = 2
const backupNativeMetadataOwnerVersion = 1
const backupBinarySecurityPrefix = "base64-self-relative:"
const nativeFileEAInformation = 7
const nativeFileStreamInformation = 7
const backupDACLControlMask = windows.SE_DACL_AUTO_INHERIT_REQ | windows.SE_DACL_AUTO_INHERITED | windows.SE_DACL_PROTECTED
const backupSettableAttributes = windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_SYSTEM | windows.FILE_ATTRIBUTE_ARCHIVE | windows.FILE_ATTRIBUTE_TEMPORARY | windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED

type backupNativeMetadata struct {
	Security   string `json:"owner_group_dacl"`
	Attributes uint32 `json:"attributes"`
}

var writeBackupSecurity = windows.NewLazySystemDLL("kernel32.dll").NewProc("BackupWrite")

func readBackupNativeMetadata(path string) (*backupNativeMetadata, error) {
	return readBackupNativeMetadataVersion(path, backupNativeMetadataVersion)
}

func backupNativeMetadataSupported(version int) bool {
	return version == backupNativeMetadataOwnerVersion || version == backupNativeMetadataDACLVersion || version == backupNativeMetadataVersion
}

func backupSecurityInformation(version int) (windows.SECURITY_INFORMATION, error) {
	switch version {
	case backupNativeMetadataOwnerVersion:
		return windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION, nil
	case backupNativeMetadataDACLVersion, backupNativeMetadataVersion:
		return windows.DACL_SECURITY_INFORMATION, nil
	default:
		return 0, fmt.Errorf("unsupported Windows backup metadata version %d", version)
	}
}

func readBackupNativeMetadataVersion(path string, version int) (*backupNativeMetadata, error) {
	securityInformation, err := backupSecurityInformation(version)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		return nil, err
	}
	if attributes & ^uint32(backupSettableAttributes|windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_NORMAL) != 0 {
		return nil, errors.New("backup refuses unsupported file attributes, reparse, encrypted, sparse or compressed data")
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_EA|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)
	// Extended attributes and named streams are explicitly refused until they
	// have a bounded preservation contract. Never silently drop those data.
	var status windows.IO_STATUS_BLOCK
	// NtQueryInformationFile probes native alignment even on x64. A small
	// Go byte array may be packed at an unaligned address by the allocator.
	var ea uint64
	if err = windows.NtQueryInformationFile(handle, &status, (*byte)(unsafe.Pointer(&ea)), 4, nativeFileEAInformation); err != nil {
		return nil, fmt.Errorf("cannot establish extended-attribute fidelity: %w", err)
	}
	if uint32(ea) != 0 {
		return nil, errors.New("backup refuses NTFS extended attributes; original retained")
	}
	// FILE_STREAM_INFO requires an 8-byte base and entry alignment.
	// Keep native storage aligned, while parsing bounded bytes explicitly.
	var streamStorage [8192]uint64
	streams := unsafe.Slice((*byte)(unsafe.Pointer(&streamStorage[0])), len(streamStorage)*8)
	err = windows.GetFileInformationByHandleEx(handle, nativeFileStreamInformation, &streams[0], uint32(len(streams)))
	if err != nil && !errors.Is(err, windows.ERROR_HANDLE_EOF) {
		return nil, fmt.Errorf("cannot enumerate file streams: %w", err)
	}
	if err == nil {
		for offset := 0; ; {
			if offset+24 > len(streams) {
				return nil, errors.New("invalid stream enumeration")
			}
			next := int(binary.LittleEndian.Uint32(streams[offset:]))
			length := int(binary.LittleEndian.Uint32(streams[offset+4:]))
			if length < 0 || length%2 != 0 || length > len(streams)-offset-24 {
				return nil, errors.New("invalid stream name")
			}
			units := make([]uint16, length/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(streams[offset+24+i*2:])
			}
			name := string(utf16.Decode(units))
			if name != "" && name != "::$DATA" {
				return nil, errors.New("backup refuses alternate data streams; original retained")
			}
			if next == 0 {
				break
			}
			if next%8 != 0 || next < 24+length || next > len(streams)-offset {
				return nil, errors.New("invalid stream offset")
			}
			offset += next
		}
	}
	security, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInformation)
	if err != nil {
		return nil, err
	}
	var encoded string
	if version == backupNativeMetadataVersion {
		encoded, err = encodeBackupSecurityDescriptor(security)
		if err != nil {
			return nil, err
		}
	} else {
		encoded = security.String()
	}
	if encoded == "" || len(encoded) > 128<<10 {
		return nil, errors.New("unsupported security descriptor size")
	}
	return &backupNativeMetadata{Security: encoded, Attributes: attributes & backupSettableAttributes}, nil
}

func encodeBackupSecurityDescriptor(security *windows.SECURITY_DESCRIPTOR) (string, error) {
	if security == nil || !security.IsValid() {
		return "", errors.New("invalid backup security descriptor")
	}
	length := int(security.Length())
	if length < 20 || length > 65536 {
		return "", errors.New("unsupported security descriptor size")
	}
	control, _, err := security.Control()
	if err != nil || control&windows.SE_SELF_RELATIVE == 0 {
		return "", errors.Join(errors.New("backup security descriptor is not self-relative"), err)
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(security)), length)
	encoded := backupBinarySecurityPrefix + base64.RawStdEncoding.EncodeToString(raw)
	runtime.KeepAlive(security)
	return encoded, nil
}

func decodeBackupSecurityDescriptor(encoded string, version int) (*windows.SECURITY_DESCRIPTOR, []uint64, error) {
	if version != backupNativeMetadataVersion || !strings.HasPrefix(encoded, backupBinarySecurityPrefix) {
		security, err := windows.SecurityDescriptorFromString(encoded)
		return security, nil, err
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, backupBinarySecurityPrefix))
	if err != nil || len(raw) < 20 || len(raw) > 65536 {
		return nil, nil, errors.Join(errors.New("invalid encoded backup security descriptor"), err)
	}
	storage := make([]uint64, (len(raw)+7)/8)
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), len(storage)*8)
	copy(buffer, raw)
	security := (*windows.SECURITY_DESCRIPTOR)(unsafe.Pointer(&storage[0]))
	if !security.IsValid() || int(security.Length()) != len(raw) {
		return nil, nil, errors.New("invalid binary backup security descriptor")
	}
	control, _, err := security.Control()
	if err != nil || control&windows.SE_SELF_RELATIVE == 0 {
		return nil, nil, errors.Join(errors.New("binary backup security descriptor is not self-relative"), err)
	}
	if _, _, err := security.DACL(); err != nil {
		return nil, nil, fmt.Errorf("binary backup security descriptor has no DACL: %w", err)
	}
	return security, storage, nil
}

func applyBackupNativeMetadata(path string, metadata *backupNativeMetadata) (resultErr error) {
	return applyBackupNativeMetadataVersion(path, metadata, backupNativeMetadataVersion)
}

func applyBackupNativeMetadataVersion(path string, metadata *backupNativeMetadata, version int) (resultErr error) {
	if metadata == nil {
		return errors.New("missing Windows backup metadata")
	}
	if !backupNativeMetadataSupported(version) {
		return fmt.Errorf("unsupported Windows backup metadata version %d", version)
	}
	security, securityStorage, err := decodeBackupSecurityDescriptor(metadata.Security, version)
	if err != nil {
		return err
	}
	defer runtime.KeepAlive(securityStorage)
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Apply attributes before the recorded DACL. The destination is still a
	// private staging object at this point; after DACL restoration the current
	// desktop user may intentionally have read-only access.
	if err := applyBackupAttributes(name, metadata.Attributes); err != nil {
		return fmt.Errorf("restore backup attributes: %w", err)
	}
	if version != backupNativeMetadataOwnerVersion {
		security, err = backupSecurityWithTargetIdentity(path, security)
		if err != nil {
			return err
		}
	}
	length := int(security.Length())
	if length < 20 || length > 65536 {
		return errors.New("invalid backup security descriptor size")
	}
	// BackupWrite preserves inherited ACEs byte-for-byte, unlike ordinary ACL
	// setters that recompute them from the private staging parent. Version 2
	// combines the recorded DACL with the new target's already-assigned owner
	// and group, so the stream never attempts to assign a foreign SID. Version 1
	// remains readable for interrupted 1.1.7-1.1.9 journals.
	handle, err := windows.CreateFile(name, windows.WRITE_DAC|windows.WRITE_OWNER|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("open backup security target: %w", err)
	}
	defer windows.CloseHandle(handle)
	storage := make([]uint64, (20+length+7)/8)
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), 20+length)
	binary.LittleEndian.PutUint32(buffer[0:], 3) // BACKUP_SECURITY_DATA
	binary.LittleEndian.PutUint32(buffer[4:], 2) // STREAM_CONTAINS_SECURITY
	binary.LittleEndian.PutUint64(buffer[8:], uint64(length))
	copy(buffer[20:], unsafe.Slice((*byte)(unsafe.Pointer(security)), length))
	runtime.KeepAlive(security)
	var context uintptr
	defer func() {
		if context != 0 {
			var ignored uint32
			ok, _, failure := writeBackupSecurity.Call(uintptr(handle), 0, 0, uintptr(unsafe.Pointer(&ignored)), 1, 0, uintptr(unsafe.Pointer(&context)))
			if ok == 0 {
				resultErr = errors.Join(resultErr, fmt.Errorf("release backup restoration context: %w", failure))
			}
		}
	}()
	var written uint32
	ok, _, failure := writeBackupSecurity.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&written)), 0, 1, uintptr(unsafe.Pointer(&context)))
	runtime.KeepAlive(storage)
	if ok == 0 {
		return fmt.Errorf("restore backup security stream: %w", failure)
	}
	if int(written) != len(buffer) {
		return io.ErrShortWrite
	}
	return nil
}

func backupSecurityWithTargetIdentity(path string, daclSecurity *windows.SECURITY_DESCRIPTOR) (*windows.SECURITY_DESCRIPTOR, error) {
	identity, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	owner, ownerDefaulted, err := identity.Owner()
	if err != nil || owner == nil {
		return nil, errors.Join(errors.New("backup target has no owner"), err)
	}
	group, groupDefaulted, err := identity.Group()
	if err != nil || group == nil {
		return nil, errors.Join(errors.New("backup target has no group"), err)
	}
	dacl, daclDefaulted, err := daclSecurity.DACL()
	if err != nil || dacl == nil {
		return nil, errors.Join(errors.New("backup metadata has no DACL"), err)
	}
	control, _, err := daclSecurity.Control()
	if err != nil {
		return nil, err
	}
	absolute, err := windows.NewSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	if err := absolute.SetOwner(owner, ownerDefaulted); err != nil {
		return nil, err
	}
	if err := absolute.SetGroup(group, groupDefaulted); err != nil {
		return nil, err
	}
	if err := absolute.SetDACL(dacl, true, daclDefaulted); err != nil {
		return nil, err
	}
	if err := absolute.SetControl(backupDACLControlMask, control&backupDACLControlMask); err != nil {
		return nil, err
	}
	return absolute.ToSelfRelative()
}

func applyBackupAttributes(name *uint16, attributes uint32) error {
	if attributes == 0 {
		attributes = windows.FILE_ATTRIBUTE_NORMAL
	}
	return windows.SetFileAttributes(name, attributes)
}

func backupNativeMetadataSize(value *backupNativeMetadata) int {
	if value == nil {
		return 0
	}
	return len(value.Security) + 4
}
func equalBackupNativeMetadata(a, b *backupNativeMetadata) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func backupNativeMetadataDifference(actual, expected *backupNativeMetadata) string {
	if actual == nil || expected == nil {
		return "metadata unavailable"
	}
	if actual.Attributes != expected.Attributes {
		return fmt.Sprintf("attribute mismatch: actual=%#x expected=%#x", actual.Attributes, expected.Attributes)
	}
	if actual.Security != expected.Security {
		a, aStorage, ae := decodeBackupSecurityDescriptor(actual.Security, backupNativeMetadataVersion)
		b, bStorage, be := decodeBackupSecurityDescriptor(expected.Security, backupNativeMetadataVersion)
		if ae == nil && be == nil {
			ac, _, _ := a.Control()
			bc, _, _ := b.Control()
			runtime.KeepAlive(aStorage)
			runtime.KeepAlive(bStorage)
			return fmt.Sprintf("security descriptor mismatch: actual control=%#x expected control=%#x", ac, bc)
		}
		return "security descriptor mismatch"
	}
	return "metadata matches"
}
