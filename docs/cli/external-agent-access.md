# 外部 Agent 控制 AgentDock

AgentDock Core 通过 MCP Streamable HTTP `/mcp` 端点接收 Codex、Claude Code 和其他 MCP Agent 的调用。该入口复用 AgentDock 现有的工具发现、工作区边界、权限、审批、任务、活动记录与中途插入机制，不建立第二套执行通道。

## 平台覆盖

| 平台 | 配置入口 | Core 能力 | 本次范围 |
|---|---|---|---|
| Windows | 控制面板“外部 Agent”页面；`agentdock external-agent` | 完整 `/mcp`、OAuth/Bearer、运行状态描述 | 图形入口与 CLI 均可用 |
| macOS | 菜单栏“External agent access”；`agentdock external-agent` | 完整 `/mcp`、OAuth/Bearer、运行状态描述 | 图形入口与 CLI 均可用 |
| Linux | `agentdock external-agent` | 完整 `/mcp`、OAuth/Bearer、运行状态描述 | 使用 CLI，不增加独立桌面页面 |
| Android | 无新增页面 | Android Workbench 部署的 Linux ARM64 Core 使用相同 `/mcp` 协议与 `external_agent_access` 状态契约 | 只接入共享内核；后续可在移动端复用同一描述生成界面 |

## 生成客户端配置

CLI 默认读取当前进程继承的 AgentDock Core 环境配置，并生成本机端点、认证方式和对应客户端配置。桌面入口直接使用正在运行的 Core 状态快照；独立 shell 未继承服务环境时，应显式设置 `--endpoint` 与 `--auth`：

```powershell
agentdock external-agent --client codex
```

生成公网 Claude Code 配置：

```powershell
agentdock external-agent --client claude-code --source public
```

为任意 MCP 客户端生成 JSON：

```powershell
agentdock external-agent `
  --client generic-mcp `
  --endpoint https://agentdock.example.com/mcp `
  --auth oauth
```

生成可交给其他 Agent 的连接提示词：

```powershell
agentdock external-agent --client agent-prompt --source public
```

机器读取使用 JSON 输出：

```powershell
agentdock external-agent --client codex --format json
```

返回对象包含稳定的 `schema_version`、`protocol`、`client`、`endpoint`、`auth_mode` 和 `configuration` 字段，并固定返回 `credentials_included=false`。

## 参数

| 参数 | 可选值 | 规则 |
|---|---|---|
| `--client` | `codex`、`claude-code`、`generic-mcp`、`agent-prompt` | 默认 `codex` |
| `--source` | `local`、`public` | 默认 `local`；`public` 使用当前 OAuth 公网 Origin |
| `--endpoint` | 以 `/mcp` 结尾的 HTTP(S) URL | 设置后覆盖 `--source`；拒绝 URL 用户信息、查询参数和片段 |
| `--auth` | `auto`、`oauth`、`bearer`、`none` | 默认 `auto`；按当前 Core 配置推导 |
| `--format` | `text`、`json` | 默认输出可复制的客户端配置文本 |

## 认证与凭据

`auto` 模式按以下顺序解析：

1. Core 启用 OAuth 时生成 OAuth 配置；Codex 配置包含 `codex mcp login agentdock`。
2. Core 配置 Bearer 认证时，生成环境变量引用 `AGENTDOCK_MCP_BEARER`。
3. 仅回环监听且 Core 未要求认证时生成 `none`。

生成结果不读取或复制 AgentDock 的实际访问令牌、OAuth 密码或 Tunnel 凭据。使用 Bearer 模式时，由调用方进程的环境变量或其受信凭据存储提供真实值。公网端点应使用 OAuth 或 Bearer，并继续受 AgentDock 权限、审批和工作区策略约束。

## 运行状态契约

`GET /internal/runtime/status` 增加 `external_agent_access`：

```json
{
  "external_agent_access": {
    "schema_version": 1,
    "server_name": "agentdock",
    "protocol": "mcp-streamable-http",
    "endpoint_path": "/mcp",
    "local_endpoint": "http://127.0.0.1:8765/mcp",
    "public_endpoint": "https://agentdock.example.com/mcp",
    "auth_mode": "oauth",
    "supported_clients": [
      "codex",
      "claude-code",
      "generic-mcp",
      "agent-prompt"
    ],
    "configuration_command": "agentdock external-agent",
    "credentials_included": false
  }
}
```

字段来自 Core 的实际监听地址和认证配置，因此 Windows、macOS、Linux 与 Android 使用同一契约。未配置公网 Origin 时省略 `public_endpoint`。

## Agent 调用顺序

外部 Agent 建立 MCP 连接后，应在首次设备操作前调用一次 `agentdock_context`。当 `context_diagnostics.complete=true` 时复用当前上下文；上下文失效后再重新发现。后续工具调用继续进入 AgentDock 的权限、审批、任务绑定和活动记录链路。

现有 AgentDock 调用 ACP/Codex 等下游 Agent 的能力保持原有接口和配置，入站外部 Agent 连接与出站 Agent 调用可以同时使用。
