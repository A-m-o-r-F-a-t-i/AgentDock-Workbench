//go:build windows

package file

func preserveForwardPatchOwner(_ string, _ stagedPatchFile) error { return nil }
