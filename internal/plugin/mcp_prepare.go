package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/uvwt/agentdock/internal/fs/securepath"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
)

// PrepareMCPData revalidates the selected formal generation or its private
// stdio runtime copy before applying private data-directory permissions.
func (s *Store) PrepareMCPData(ctx context.Context, cfg mcpclient.ServerConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validPluginName(cfg.PluginName) {
		return errors.New("invalid plugin data owner")
	}
	mappedState, releaseMapped, mapped, err := s.acquireCatalogMCPRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	if mapped {
		defer releaseMapped()
	}
	directory, _, err := s.Snapshot(ctx)
	if err != nil {
		return err
	}
	member, owned := directory.MCPMembership(cfg.Name)
	if !owned || !member.Enabled || member.Plugin != cfg.PluginName {
		return errors.New("plugin MCP is no longer enabled for this owner")
	}

	release, err := s.acquire()
	if err != nil {
		return err
	}
	state := mappedState
	if !mapped {
		state, err = s.readNamedState(cfg.PluginName)
		if err != nil {
			release()
			return err
		}
	}
	root, err := containedPath(s.root, cfg.PluginRoot, false)
	if err != nil {
		release()
		return err
	}
	data, err := containedPath(s.root, filepath.Join(s.root, ".data", cfg.PluginName), true)
	if err != nil {
		release()
		return err
	}
	configuredData, err := containedPath(s.root, cfg.PluginData, true)
	if err != nil || !sameFilesystemPath(data, configuredData) {
		release()
		return errors.New("plugin data target changed; refresh the service before dispatch")
	}
	generationRoot, err := s.generationPath(cfg.PluginName, state.Generation)
	if err != nil {
		release()
		return err
	}
	// MCPServersContext remains a compatibility/inspection API and returns the
	// visible package path. The live MCP provider uses generation roots or
	// private runtime copies, but preparation still validates the visible path.
	visibleRoot := filepath.Join(s.root, cfg.PluginName)
	validRoot := sameFilesystemPath(root, generationRoot) || sameFilesystemPath(root, visibleRoot)
	runtimeCopy := !validRoot && s.ownsRuntimeGeneration(cfg.PluginName, state.Generation, root)
	if !validRoot && !runtimeCopy {
		release()
		return errors.New("plugin filesystem target changed; refresh the service before dispatch")
	}
	digestRoot := root
	if runtimeCopy {
		digestRoot = generationRoot
	}
	digest, exists, err := packageDigestAt(digestRoot)
	if err != nil || !exists || digest != state.PackageDigest {
		release()
		return errors.New("plugin runtime package no longer matches formal state")
	}
	if runtimeCopy {
		if err := validateRuntimePackageFiles(ctx, generationRoot, root); err != nil {
			release()
			return err
		}
	}
	record, err := readPackage(root, false)
	if err != nil || record.manifest.Name != cfg.PluginName || record.manifest.Version != cfg.PluginVersion {
		release()
		return errors.New("plugin runtime package identity no longer matches formal state")
	}
	release()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	after, err := containedPath(s.root, data, false)
	if err != nil {
		return err
	}
	if !sameFilesystemPath(after, data) {
		return errors.New("plugin data directory changed during preparation")
	}
	if err := securepath.EnsurePrivate(data); err != nil {
		return err
	}
	return ctx.Err()
}

func sameFilesystemPath(left, right string) bool {
	leftResolved, leftErr := resolvedPath(left, true)
	rightResolved, rightErr := resolvedPath(right, true)
	if leftErr == nil && rightErr == nil {
		leftClean := filepath.Clean(leftResolved)
		rightClean := filepath.Clean(rightResolved)
		if runtime.GOOS == "windows" {
			return strings.EqualFold(leftClean, rightClean)
		}
		return leftClean == rightClean
	}
	leftInfo, leftStatErr := os.Stat(left)
	rightInfo, rightStatErr := os.Stat(right)
	if leftStatErr == nil && rightStatErr == nil {
		return os.SameFile(leftInfo, rightInfo)
	}
	leftAbs, leftAbsErr := filepath.Abs(left)
	rightAbs, rightAbsErr := filepath.Abs(right)
	if leftAbsErr != nil || rightAbsErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}
