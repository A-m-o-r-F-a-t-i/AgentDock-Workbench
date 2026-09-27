//go:build windows

package securepath

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func privateTestDescriptor(t testing.TB, sddl string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	return sd
}

func TestPrivateDACLMatchesRequiresExactProtectedBoundary(t *testing.T) {
	const aces = "(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	desired := privateTestDescriptor(t, "D:P"+aces)
	for _, test := range []struct {
		name string
		sddl string
		want bool
	}{
		{"exact", "D:P" + aces, true},
		{"auto-inherited-descriptor-only", "D:PAI" + aces, true},
		{"owner-group-unrelated", "O:SYG:BAD:P" + aces, true},
		{"unprotected", "D:" + aces, false},
		{"extra-reader", "D:P" + aces + "(A;OICI;FR;;;WD)", false},
		{"different-rights", "D:P(A;OICI;FR;;;SY)(A;OICI;FA;;;BA)", false},
		{"file-inheritance", "D:P(A;;FA;;;SY)(A;;FA;;;BA)", false},
		{"inherited-ace", "D:P(A;OICIID;FA;;;SY)(A;OICI;FA;;;BA)", false},
		{"different-order", "D:P(A;OICI;FA;;;BA)(A;OICI;FA;;;SY)", false},
		{"empty-deny-all", "D:P", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := privateDACLMatches(privateTestDescriptor(t, test.sddl), desired); got != test.want {
				t.Fatalf("privateDACLMatches(%s) = %v, want %v", test.sddl, got, test.want)
			}
		})
	}
	if privateDACLMatches(nil, desired) || privateDACLMatches(desired, nil) {
		t.Fatal("nil descriptor accepted")
	}
	nullACL, err := windows.NewSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	if err := nullACL.SetDACL(nil, true, false); err != nil {
		t.Fatal(err)
	}
	if err := nullACL.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		t.Fatal(err)
	}
	if privateDACLMatches(nullACL, desired) {
		t.Fatal("NULL/permissive DACL accepted")
	}
}

func TestEnsurePrivateDACLIsReadOnlyAfterFirstWriteAndRepairsDrift(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []bool{true, false} {
		t.Run(fmt.Sprintf("directory=%v", directory), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "target")
			inheritance := ""
			if directory {
				inheritance = "OICI"
				err = os.Mkdir(path, 0700)
			} else {
				err = os.WriteFile(path, []byte("fixture"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			wanted := fmt.Sprintf("D:P(A;%s;FA;;;%s)(A;%s;FA;;;SY)(A;%s;FA;;;BA)", inheritance, user.User.Sid.String(), inheritance, inheritance)
			desired := privateTestDescriptor(t, wanted)
			if _, err := ensurePrivateDACL(path, desired); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 4; i++ {
				changed, err := ensurePrivateDACL(path, desired)
				if err != nil || changed {
					t.Fatalf("correct DACL rewritten on repeat %d: changed=%v err=%v", i, changed, err)
				}
			}
			// Broaden only this temporary fixture, then require a real repair.
			drifted := privateTestDescriptor(t, wanted+fmt.Sprintf("(A;%s;FR;;;WD)", inheritance))
			dacl, _, err := drifted.DACL()
			if err != nil {
				t.Fatal(err)
			}
			if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
				t.Fatal(err)
			}
			changed, err := ensurePrivateDACL(path, desired)
			if err != nil || !changed {
				t.Fatalf("changed ACL was not repaired: changed=%v err=%v", changed, err)
			}
			actual, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil || !privateDACLMatches(actual, desired) {
				t.Fatalf("repaired ACL mismatch: %v, err=%v", actual, err)
			}
		})
	}
}

// Compare the previous unconditional write with the new read-only fast path on
// the same bounded, disposable tree. No production path is accessed.
func BenchmarkPrivateDACLExistingTree(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 2048; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("entry-%04d", i)), nil, 0600); err != nil {
			b.Fatal(err)
		}
	}
	if err := EnsurePrivate(root); err != nil {
		b.Fatal(err)
	}
	desired, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("previous-unconditional-write", func(b *testing.B) {
		dacl, _, err := desired.DACL()
		if err != nil {
			b.Fatal(err)
		}
		for i := 0; i < b.N; i++ {
			if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("checked-existing-boundary", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := EnsurePrivate(root); err != nil {
				b.Fatal(err)
			}
		}
	})
}
