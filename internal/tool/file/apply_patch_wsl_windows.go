//go:build windows

package file

import (
	"context"
	"errors"
	"fmt"
)

func (svc *Service) previewV4APatchWSL(ctx context.Context, request ApplyPatchRequest, selection fileRuntimeSelection) (Result, error) {
	return svc.callV4APatchWSL(ctx, request, selection, true)
}

func (svc *Service) applyV4APatchWSL(ctx context.Context, request ApplyPatchRequest, selection fileRuntimeSelection) (Result, error) {
	return svc.callV4APatchWSL(ctx, request, selection, false)
}

func (svc *Service) callV4APatchWSL(ctx context.Context, request ApplyPatchRequest, selection fileRuntimeSelection, preview bool) (Result, error) {
	if _, err := parseV4APatch(request.Patch); err != nil {
		return failedV4APatch("preflight", "none", err, nil)
	}
	root, err := resolveWSLFilePath(request.Workdir)
	if err != nil {
		return failedV4APatch("preflight", "none", err, nil)
	}
	response, err := svc.callWSLFileHelper(ctx, selection, map[string]any{
		"action": ToolApplyPatch, "patch": request.Patch, "workdir": root, "dry_run": preview,
		"expected_patch_revision": request.ExpectedRevision, "workspace_id": request.WorkspaceID,
	})
	if err != nil {
		effect, phase := "unknown", "apply"
		var failure *ToolError
		if preview || errors.As(err, &failure) && failure.Category == "validation" {
			effect, phase = "none", "preflight"
		}
		return failedV4APatch(phase, effect, err, nil)
	}
	canonical, ok := response["patch_result"].(map[string]any)
	if !ok {
		return failedV4APatch("apply", "unknown", fmt.Errorf("WSL helper omitted the canonical patch result"), nil)
	}
	result := addFileRuntimeResult(Result(canonical), selection)
	result["workspace_id"], result["resolved_path"] = request.WorkspaceID, root
	if result["operation_status"] == "failed" {
		message, _ := result["message"].(string)
		return result, toolErrorDetails("PATCH_APPLY_FAILED", message, "runtime", map[string]any(result))
	}
	return result, nil
}
