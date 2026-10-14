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
const backupSecurityDescriptorHeaderSize = 20
const nativeFileEAInformation = 7
const nativeFileStreamInformation = 7
const backupDACLControlMask = windows.SE_DACL_PRESENT | windows.SE_DACL_DEFAULTED | windows.SE_DACL_AUTO_INHERIT_REQ | windows.SE_DACL_AUTO_INHERITED | windows.SE_DACL_PROTECTED
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
	var identityStorage []uint64
	if version != backupNativeMetadataOwnerVersion {
		security, identityStorage, err = backupSecurityWithTargetIdentity(path, security)
		if err != nil {
			return fmt.Errorf("combine DACL metadata with backup target identity: %w", err)
		}
	}
	defer runtime.KeepAlive(identityStorage)
	length := int(security.Length())
	if length < 20 || length > 65536 {
		return errors.New("invalid backup security descriptor size")
	}
	// BackupWrite preserves inherited ACEs byte-for-byte, unlike ordinary ACL
	// setters that recompute them from the private staging parent. Versions 2
	// and 3 combine the recorded DACL with the staging object's existing owner
	// and group in a directly constructed self-relative descriptor. Version 1
	// remains readable for interrupted 1.1.7-1.1.9 journals and may restore its
	// recorded owner and group.
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

func backupSecurityWithTargetIdentity(path string, daclSecurity *windows.SECURITY_DESCRIPTOR) (*windows.SECURITY_DESCRIPTOR, []uint64, error) {
	identity, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION)
	if err != nil {
		return nil, nil, fmt.Errorf("read backup target owner and group: %w", err)
	}
	owner, ownerDefaulted, err := identity.Owner()
	if err != nil {
		return nil, nil, fmt.Errorf("read backup target owner: %w", err)
	}
	if owner == nil || !owner.IsValid() || owner.Len() <= 0 {
		return nil, nil, errors.New("backup target has no valid owner")
	}
	group, groupDefaulted, err := identity.Group()
	if err != nil {
		return nil, nil, fmt.Errorf("read backup target group: %w", err)
	}
	if group == nil || !group.IsValid() || group.Len() <= 0 {
		return nil, nil, errors.New("backup target has no valid group")
	}
	dacl, daclDefaulted, err := daclSecurity.DACL()
	if err != nil {
		return nil, nil, fmt.Errorf("read backup metadata DACL: %w", err)
	}
	if dacl == nil {
		return nil, nil, errors.New("backup metadata has no DACL")
	}
	control, _, err := daclSecurity.Control()
	if err != nil {
		return nil, nil, fmt.Errorf("read backup metadata DACL control: %w", err)
	}

	ownerLength := owner.Len()
	groupLength := group.Len()
	daclHeader := unsafe.Slice((*byte)(unsafe.Pointer(dacl)), 8)
	daclLength := int(binary.LittleEndian.Uint16(daclHeader[2:4]))
	if daclLength < 8 {
		return nil, nil, fmt.Errorf("invalid backup DACL size %d", daclLength)
	}
	ownerOffset := alignBackupSecurityOffset(backupSecurityDescriptorHeaderSize)
	groupOffset := alignBackupSecurityOffset(ownerOffset + ownerLength)
	daclOffset := alignBackupSecurityOffset(groupOffset + groupLength)
	totalLength := daclOffset + daclLength
	if totalLength < daclOffset || totalLength > 65536 {
		return nil, nil, fmt.Errorf("combined backup security descriptor is too large: %d", totalLength)
	}

	storage := make([]uint64, (totalLength+7)/8)
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), totalLength)
	buffer[0] = 1 // SECURITY_DESCRIPTOR_REVISION
	combinedControl := windows.SECURITY_DESCRIPTOR_CONTROL(windows.SE_SELF_RELATIVE | windows.SE_DACL_PRESENT)
	combinedControl |= control & backupDACLControlMask
	if ownerDefaulted {
		combinedControl |= windows.SE_OWNER_DEFAULTED
	}
	if groupDefaulted {
		combinedControl |= windows.SE_GROUP_DEFAULTED
	}
	if daclDefaulted {
		combinedControl |= windows.SE_DACL_DEFAULTED
	} else {
		combinedControl &^= windows.SE_DACL_DEFAULTED
	}
	binary.LittleEndian.PutUint16(buffer[2:4], uint16(combinedControl))
	binary.LittleEndian.PutUint32(buffer[4:8], uint32(ownerOffset))
	binary.LittleEndian.PutUint32(buffer[8:12], uint32(groupOffset))
	binary.LittleEndian.PutUint32(buffer[16:20], uint32(daclOffset))
	copy(buffer[ownerOffset:ownerOffset+ownerLength], unsafe.Slice((*byte)(unsafe.Pointer(owner)), ownerLength))
	copy(buffer[groupOffset:groupOffset+groupLength], unsafe.Slice((*byte)(unsafe.Pointer(group)), groupLength))
	copy(buffer[daclOffset:daclOffset+daclLength], unsafe.Slice((*byte)(unsafe.Pointer(dacl)), daclLength))
	runtime.KeepAlive(identity)
	runtime.KeepAlive(daclSecurity)

	combined := (*windows.SECURITY_DESCRIPTOR)(unsafe.Pointer(&storage[0]))
	if !combined.IsValid() {
		return nil, nil, errors.New("constructed backup security descriptor is invalid")
	}
	if length := int(combined.Length()); length != totalLength {
		return nil, nil, fmt.Errorf("constructed backup security descriptor length mismatch: actual=%d expected=%d", length, totalLength)
	}
	return combined, storage, nil
}

func alignBackupSecurityOffset(value int) int {
	return (value + 3) &^ 3
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

// canonicalBackupNativeMetadataVersion normalizes integrity records according
// to the historical contract that produced them. Version 1 includes owner and
// group; versions 2 and 3 preserve only the DACL and its inheritance controls.
func canonicalBackupNativeMetadataVersion(value *backupNativeMetadata, version int) (*backupNativeMetadata, error) {
	if value == nil {
		return nil, nil
	}
	if !backupNativeMetadataSupported(version) {
		return nil, fmt.Errorf("unsupported Windows backup metadata version %d", version)
	}
	canonical := *value
	security, storage, err := decodeBackupSecurityDescriptor(value.Security, version)
	if err != nil {
		return nil, err
	}
	if version == backupNativeMetadataOwnerVersion {
		canonical.Security = security.String()
		runtime.KeepAlive(storage)
		if canonical.Security == "" {
			return nil, errors.New("could not canonicalize owner-aware backup descriptor")
		}
		return &canonical, nil
	}
	daclOnly, daclStorage, err := backupDACLOnlySecurityDescriptor(security)
	if err != nil {
		return nil, err
	}
	canonical.Security = daclOnly.String()
	runtime.KeepAlive(daclStorage)
	runtime.KeepAlive(storage)
	if canonical.Security == "" {
		return nil, errors.New("could not canonicalize backup DACL")
	}
	return &canonical, nil
}

func canonicalBackupNativeMetadata(value *backupNativeMetadata) (*backupNativeMetadata, error) {
	return canonicalBackupNativeMetadataVersion(value, backupNativeMetadataVersion)
}

func backupDACLOnlySecurityDescriptor(security *windows.SECURITY_DESCRIPTOR) (*windows.SECURITY_DESCRIPTOR, []uint64, error) {
	dacl, daclDefaulted, err := security.DACL()
	if err != nil {
		return nil, nil, fmt.Errorf("read backup metadata DACL: %w", err)
	}
	if dacl == nil {
		return nil, nil, errors.New("backup metadata has no DACL")
	}
	control, _, err := security.Control()
	if err != nil {
		return nil, nil, fmt.Errorf("read backup metadata DACL control: %w", err)
	}
	daclHeader := unsafe.Slice((*byte)(unsafe.Pointer(dacl)), 8)
	daclLength := int(binary.LittleEndian.Uint16(daclHeader[2:4]))
	if daclLength < 8 {
		return nil, nil, fmt.Errorf("invalid backup DACL size %d", daclLength)
	}
	daclOffset := alignBackupSecurityOffset(backupSecurityDescriptorHeaderSize)
	totalLength := daclOffset + daclLength
	if totalLength < daclOffset || totalLength > 65536 {
		return nil, nil, fmt.Errorf("canonical backup DACL descriptor is too large: %d", totalLength)
	}

	storage := make([]uint64, (totalLength+7)/8)
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), totalLength)
	buffer[0] = 1 // SECURITY_DESCRIPTOR_REVISION
	canonicalControl := windows.SECURITY_DESCRIPTOR_CONTROL(windows.SE_SELF_RELATIVE | windows.SE_DACL_PRESENT)
	canonicalControl |= control & backupDACLControlMask
	if daclDefaulted {
		canonicalControl |= windows.SE_DACL_DEFAULTED
	} else {
		canonicalControl &^= windows.SE_DACL_DEFAULTED
	}
	binary.LittleEndian.PutUint16(buffer[2:4], uint16(canonicalControl))
	binary.LittleEndian.PutUint32(buffer[16:20], uint32(daclOffset))
	copy(buffer[daclOffset:daclOffset+daclLength], unsafe.Slice((*byte)(unsafe.Pointer(dacl)), daclLength))
	runtime.KeepAlive(security)

	canonical := (*windows.SECURITY_DESCRIPTOR)(unsafe.Pointer(&storage[0]))
	if !canonical.IsValid() {
		return nil, nil, errors.New("constructed canonical backup DACL descriptor is invalid")
	}
	if length := int(canonical.Length()); length != totalLength {
		return nil, nil, fmt.Errorf("canonical backup DACL descriptor length mismatch: actual=%d expected=%d", length, totalLength)
	}
	return canonical, storage, nil
}

func equalBackupNativeMetadataVersion(a, b *backupNativeMetadata, version int) bool {
	canonicalA, errA := canonicalBackupNativeMetadataVersion(a, version)
	canonicalB, errB := canonicalBackupNativeMetadataVersion(b, version)
	if errA != nil || errB != nil || canonicalA == nil || canonicalB == nil {
		return errA == nil && errB == nil && canonicalA == canonicalB
	}
	return *canonicalA == *canonicalB
}

func equalBackupNativeMetadata(a, b *backupNativeMetadata) bool {
	return equalBackupNativeMetadataVersion(a, b, backupNativeMetadataVersion)
}

func backupNativeMetadataDifferenceVersion(actual, expected *backupNativeMetadata, version int) string {
	if actual == nil || expected == nil {
		return "metadata unavailable"
	}
	if actual.Attributes != expected.Attributes {
		return fmt.Sprintf("attribute mismatch: actual=%#x expected=%#x", actual.Attributes, expected.Attributes)
	}
	canonicalActual, actualErr := canonicalBackupNativeMetadataVersion(actual, version)
	canonicalExpected, expectedErr := canonicalBackupNativeMetadataVersion(expected, version)
	if actualErr != nil || expectedErr != nil {
		return fmt.Sprintf("security descriptor canonicalization failed: actual=%v expected=%v", actualErr, expectedErr)
	}
	if canonicalActual.Security != canonicalExpected.Security {
		return fmt.Sprintf("security descriptor mismatch: actual=%q expected=%q", canonicalActual.Security, canonicalExpected.Security)
	}
	return "metadata matches"
}

func backupNativeMetadataDifference(actual, expected *backupNativeMetadata) string {
	return backupNativeMetadataDifferenceVersion(actual, expected, backupNativeMetadataVersion)
}
