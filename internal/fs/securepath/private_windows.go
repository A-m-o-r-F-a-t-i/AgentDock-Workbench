//go:build windows

package securepath

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// EnsurePrivate installs a protected DACL that grants full control only to the
// current user, SYSTEM and local administrators. Directory entries inherit the
// same boundary to newly-created children. An already-correct DACL is read-only:
// setting inheritable ACEs again makes Windows revisit existing descendants.
func EnsurePrivate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows user SID: %w", err)
	}
	inheritance := ""
	if info.IsDir() {
		inheritance = "OICI"
	}
	sddl := fmt.Sprintf(
		"D:P(A;%s;FA;;;%s)(A;%s;FA;;;SY)(A;%s;FA;;;BA)",
		inheritance,
		user.User.Sid.String(),
		inheritance,
		inheritance,
	)
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("build private Windows DACL: %w", err)
	}
	_, err = ensurePrivateDACL(path, descriptor)
	return err
}

// ensurePrivateDACL reports whether a write was necessary. Never cache this
// decision: a permissions change must be noticed on the next call.
func ensurePrivateDACL(path string, descriptor *windows.SECURITY_DESCRIPTOR) (bool, error) {
	current, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err == nil && privateDACLMatches(current, descriptor) {
		return false, nil
	}
	// A failed read is not evidence of a safe ACL. Retain the authoritative
	// write and its error rather than silently skipping the security boundary.
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return false, fmt.Errorf("read private Windows DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return false, fmt.Errorf("secure private Windows path %s: %w", path, err)
	}
	return true, nil
}

func privateDACLMatches(current, desired *windows.SECURITY_DESCRIPTOR) bool {
	if current == nil || desired == nil {
		return false
	}
	control, _, err := current.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	dacl, defaulted, err := current.DACL()
	if err != nil || defaulted || dacl == nil {
		return false
	}
	// Compare only the DACL, preserving ACE order, masks and inheritance flags.
	// Owner/group and the OS-maintained AUTO_INHERITED descriptor flag do not
	// change this boundary. Never strip flags from individual ACEs.
	normalized, err := windows.NewSecurityDescriptor()
	if err != nil {
		return false
	}
	if err := normalized.SetDACL(dacl, true, false); err != nil {
		return false
	}
	if err := normalized.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return false
	}
	actual, expected := normalized.String(), desired.String()
	return actual != "" && expected != "" && actual == expected
}
