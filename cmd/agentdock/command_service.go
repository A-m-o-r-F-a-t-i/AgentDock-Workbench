package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/uvwt/agentdock/internal/desktopruntime"
	"github.com/uvwt/agentdock/internal/fs/processlock"
)

func runServiceCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (runErr error) {
	if len(args) > 0 && args[0] == "launch-core" {
		flags := flag.NewFlagSet("agentdock service launch-core", flag.ContinueOnError)
		flags.SetOutput(stderr)
		runtimeRoot := flags.String("runtime-root", desktopruntime.DefaultRuntimeRoot(), "AgentDock 桌面运行目录")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || strings.TrimSpace(*runtimeRoot) == "" {
			return errors.New("用法：agentdock service launch-core --runtime-root <目录>")
		}
		coreLock, acquired, err := acquireCoreRuntimeLock(*runtimeRoot)
		if err != nil {
			return err
		}
		if !acquired {
			fmt.Fprintf(stderr, "agentdock: AgentDock Core 已在此运行目录启动或运行: %s\n", *runtimeRoot)
			return nil
		}
		if coreLock != nil {
			defer func() {
				if releaseErr := coreLock.Release(); releaseErr != nil {
					runErr = errors.Join(runErr, fmt.Errorf("释放 AgentDock Core 运行锁失败: %w", releaseErr))
				}
			}()
		}
		// launch-core 是桌面安装内部入口：先接管受限轮转日志，再恢复配置与 DPAPI 凭据，
		// 最后在当前进程直接运行服务，避免平台启动器长期参与日志写入和运行链路。
		logOutput, err := desktopruntime.OpenCoreLog(*runtimeRoot)
		if err != nil {
			return err
		}
		if logOutput != nil {
			defer logOutput.Close()
			stderr = logOutput
		}
		completeEnvironment := traceStartupPhase(stderr, "environment")
		err = desktopruntime.PrepareCoreEnvironment(*runtimeRoot)
		completeEnvironment(err)
		if err != nil {
			if logOutput != nil {
				fmt.Fprintf(stderr, "agentdock: %v\n", err)
			}
			return err
		}
		err = runServer(ctx, nil, stderr)
		if err != nil && logOutput != nil {
			fmt.Fprintf(stderr, "agentdock: %v\n", err)
		}
		return err
	}
	return desktopruntime.RunServiceCommand(ctx, args, stdout, stderr)
}

func acquireCoreRuntimeLock(runtimeRoot string) (*processlock.Lock, bool, error) {
	if runtime.GOOS != "windows" {
		return nil, true, nil
	}
	lock, acquired, err := processlock.TryAcquire(filepath.Join(runtimeRoot, "locks", "core-runtime.lock"))
	if err != nil {
		return nil, false, fmt.Errorf("获取 AgentDock Core 运行锁失败: %w", err)
	}
	return lock, acquired, nil
}
