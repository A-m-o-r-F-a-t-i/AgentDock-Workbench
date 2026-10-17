//go:build !windows

package file

import "context"

func (svc *Service) previewV4APatchWSL(context.Context, ApplyPatchRequest, fileRuntimeSelection) (Result, error) {
	return unsupportedWSLFileRuntime()
}
func (svc *Service) applyV4APatchWSL(context.Context, ApplyPatchRequest, fileRuntimeSelection) (Result, error) {
	return unsupportedWSLFileRuntime()
}
