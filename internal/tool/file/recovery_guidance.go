package file

import (
	"errors"
	"os"
	"path/filepath"
)

func (svc *Service) pathResolutionError(err error, rawPath string) error {
	parentPath := filepath.Dir(rawPath)
	if _, parentErr := svc.ws.ResolveExisting(parentPath); parentErr != nil {
		parentPath = "."
	}
	return pathResolutionError(err, rawPath, parentPath)
}

func pathResolutionError(err error, rawPath, parentPath string) error {
	if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
		return err
	}
	return toolErrorCause(
		"PATH_NOT_FOUND",
		"path does not exist",
		"validation",
		pathFailureDetails("PATH_NOT_FOUND", rawPath, parentPath, "missing"),
		err,
	)
}

func pathTypeError(code, message, rawPath, parentPath, actualType string) error {
	return toolErrorDetails(code, message, "validation", pathFailureDetails(code, rawPath, parentPath, actualType))
}

func pathFailureDetails(code, rawPath, parentPath, actualType string) map[string]any {
	details := map[string]any{
		"path":          rawPath,
		"actual_type":   actualType,
		"retryable":     false,
		"failure_class": "AGENT_INPUT_INVALID",
	}
	switch code {
	case "PATH_NOT_FOUND":
		details["next_action"] = ToolListDir
		details["retry_arguments"] = map[string]any{"path": parentPath}
	case "IS_DIRECTORY":
		details["next_action"] = ToolListDir
		details["retry_arguments"] = map[string]any{"path": rawPath}
	case "NOT_A_DIRECTORY":
		details["next_action"] = ToolReadFile
		details["retry_arguments"] = map[string]any{"path": rawPath}
	case "NOT_REGULAR_FILE":
		details["next_action"] = ToolListDir
		details["retry_arguments"] = map[string]any{"path": parentPath}
	}
	return details
}

func addPathRecoveryGuidance(details map[string]any, code, rawPath, parentPath string) {
	if details == nil {
		return
	}
	actualType := "unknown"
	if value, ok := details["type"].(string); ok && value != "" {
		actualType = value
	}
	for key, value := range pathFailureDetails(code, rawPath, parentPath, actualType) {
		if _, exists := details[key]; !exists {
			details[key] = value
		}
	}
}

func addSearchRecoveryGuidance(details map[string]any, code string) {
	if details == nil {
		return
	}
	switch code {
	case "RESOURCE_LIMIT", "WSL_FILE_TIMEOUT":
		details["retryable"] = false
		details["failure_class"] = "AGENT_REQUEST_TOO_BROAD"
		details["next_action"] = ToolSearchText
		details["recommended_adjustments"] = []string{
			"narrow path to the smallest relevant directory",
			"add include_globs for expected file types",
			"exclude build, cache, dependency, and activity-log directories",
			"use a literal query unless regular expressions are required",
		}
	case "INVALID_REGEX":
		details["retryable"] = false
		details["failure_class"] = "AGENT_INPUT_INVALID"
		details["next_action"] = ToolSearchText
		details["recommended_arguments"] = map[string]any{"regex": false}
	}
}
