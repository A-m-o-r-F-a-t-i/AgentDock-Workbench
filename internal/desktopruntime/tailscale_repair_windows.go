//go:build windows

package desktopruntime

import (
	"context"
	"errors"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

func platformRepairTailscale(ctx context.Context, root string) error {
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return err
	}
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()
	ctx, finishAction, err := tunnelActionContext(ctx, absRoot, false)
	if err != nil {
		return err
	}
	defer finishAction()
	release, err := acquireTunnelOperation(ctx, absRoot)
	if err != nil {
		return err
	}
	defer release()
	runtime, err := loadTunnelRuntime(absRoot)
	if err != nil {
		return err
	}
	if runtime.mode != "funnel" {
		return tailscaleProblem("mode_changed", "当前不是 Funnel 模式，未执行公网映射恢复")
	}
	hooks := defaultPublicAccessHooks()
	binary, err := hooks.findBinary(runtime.manifest.TailscaleBinary)
	if err != nil {
		return err
	}
	return withTailscaleMutationLock(ctx, binary, func() error {
		return repairConfiguredTailscale(ctx, runtime, binary, hooks)
	})
}

// repairConfiguredTailscale never restarts Core or the Tailscale service. It
// rewrites only the exact root mapping recorded in AgentDock's ownership file.
func repairConfiguredTailscale(ctx context.Context, runtime tunnelRuntime, binary string, hooks publicAccessHooks) error {
	if runtime.mode != "funnel" {
		return tailscaleProblem("mode_changed", "当前不是 Funnel 模式，未执行公网映射恢复")
	}
	state, err := loadTailscaleState(runtime.root)
	if err != nil {
		return err
	}
	if state == nil || !state.Enabled {
		return tailscaleProblem("disabled", "Funnel 未启用，未执行公网映射恢复")
	}
	origin, err := readTrimmedText(runtime.files.serverURL)
	if err != nil {
		return err
	}
	access := runtime.manifest.EffectivePublicAccess()
	if access.Provider != PublicAccessProviderTailscale || access.URL != state.PublicOrigin ||
		origin != state.PublicOrigin || runtime.localOrigin() != state.LocalOrigin {
		return tailscaleProblem("origin_mismatch", "Funnel 所有权与当前 AgentDock Origin 不一致，拒绝自动恢复")
	}
	if !hooks.localHealthy(ctx, runtime.localOrigin()+"/healthz") {
		return tailscaleProblem("core_unhealthy", "AgentDock 本机健康检查失败，未刷新公网映射")
	}
	client := hooks.client(binary)
	node, config, err := client.observe(ctx)
	if err != nil {
		return err
	}
	change, err := prepareTailscaleRefresh(node, config, state)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	state.ConfiguredAt, state.Pending, state.VerifiedAt = now, true, nil
	if err := hooks.saveState(runtime.root, state); err != nil {
		return err
	}
	if err := change.apply(ctx, client); err != nil {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		return errors.Join(err, change.rollback(recovery, client))
	}
	return nil
}
