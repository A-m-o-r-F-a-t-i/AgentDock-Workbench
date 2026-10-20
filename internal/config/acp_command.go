package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ValidateACPCommand checks the configured executable immediately before use.
// Configuration loading only checks structure, so an unavailable optional
// agent cannot prevent Core startup, installation or rollback.
func ValidateACPCommand(command string) error {
	if !filepath.IsAbs(command) {
		return fmt.Errorf("ACP command must be an absolute executable path: %s", command)
	}
	info, err := os.Stat(command)
	if err != nil {
		return fmt.Errorf("stat ACP command %s: %w", command, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("ACP command is not a regular file: %s", command)
	}
	return validateACPCommandPlatform(command, info)
}
