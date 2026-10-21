//go:build windows

package main

import "testing"

func TestCoreRuntimeLockRejectsDuplicateLaunch(t *testing.T) {
	root := t.TempDir()
	first, acquired, err := acquireCoreRuntimeLock(root)
	if err != nil || !acquired || first == nil {
		t.Fatalf("first runtime lock: lock=%v acquired=%v err=%v", first, acquired, err)
	}
	second, acquired, err := acquireCoreRuntimeLock(root)
	if err != nil || acquired || second != nil {
		t.Fatalf("duplicate runtime lock: lock=%v acquired=%v err=%v", second, acquired, err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	third, acquired, err := acquireCoreRuntimeLock(root)
	if err != nil || !acquired || third == nil {
		t.Fatalf("reacquired runtime lock: lock=%v acquired=%v err=%v", third, acquired, err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
}
