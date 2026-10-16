package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/externalagent"
)

func runExternalAgentCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("agentdock external-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	clientName := flags.String("client", string(externalagent.ClientCodex), "客户端：codex、claude-code、generic-mcp 或 agent-prompt")
	source := flags.String("source", "local", "端点来源：local 或 public")
	endpoint := flags.String("endpoint", "", "显式 MCP Streamable HTTP 地址；设置后覆盖 source")
	authName := flags.String("auth", "auto", "认证方式：auto、oauth、bearer 或 none")
	format := flags.String("format", "text", "输出格式：text 或 json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return externalAgentUsageError()
	}

	client, err := externalagent.ParseClient(*clientName)
	if err != nil {
		return err
	}

	resolvedEndpoint := strings.TrimSpace(*endpoint)
	autoAuth := strings.EqualFold(strings.TrimSpace(*authName), "auto")
	var cfg config.Config
	if resolvedEndpoint == "" || autoAuth {
		cfg, err = config.FromEnv()
		if err != nil {
			return fmt.Errorf("读取 ACTOVO 运行配置失败: %w", err)
		}
	}

	if resolvedEndpoint == "" {
		switch strings.ToLower(strings.TrimSpace(*source)) {
		case "local":
			resolvedEndpoint, err = externalagent.LocalEndpoint(cfg.Host, cfg.Port)
		case "public":
			resolvedEndpoint, err = externalagent.PublicEndpoint(cfg.OAuthServerURL)
		default:
			return errors.New("source 仅支持 local 或 public")
		}
		if err != nil {
			return err
		}
	} else {
		resolvedEndpoint, err = externalagent.NormalizeEndpoint(resolvedEndpoint)
		if err != nil {
			return err
		}
	}

	var authMode externalagent.AuthMode
	if autoAuth {
		authMode = externalagent.AuthModeForRuntime(cfg.OAuthEnabled, cfg.AuthRequired())
	} else {
		authMode, err = externalagent.ParseAuthMode(*authName)
		if err != nil {
			return err
		}
	}

	access, err := externalagent.Build(client, resolvedEndpoint, authMode)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "text":
		_, err = fmt.Fprintln(stdout, access.Configuration)
		return err
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(access)
	default:
		return errors.New("format 仅支持 text 或 json")
	}
}

func externalAgentUsageError() error {
	return errors.New("用法：agentdock external-agent [--client codex|claude-code|generic-mcp|agent-prompt] [--source local|public | --endpoint URL] [--auth auto|oauth|bearer|none] [--format text|json]")
}
