<div align="center">

[English](./README.md) | 简体中文

<img src="./docs/assets/agentdock-logo.png" alt="AgentDock Workbench logo" width="128" />

# AgentDock Workbench

**面向真实设备操作的 AI Agent 任务、执行与权限工作台**

AgentDock Workbench 将 ChatGPT、Claude、Codex 等 MCP 客户端连接到本地电脑、远程服务器和移动节点，并在 AgentDock 运行时之上提供以对话为中心的任务管理、工具调用追踪、审批、权限、中途插入、插件管理和多设备协同能力。

[下载发行版](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/releases) · [正式版 v1.1.7](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/releases/tag/v1.1.7) · [预发布版 v1.1.8](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/releases/tag/v1.1.8) · [提交问题](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/issues)

[![CI](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/actions/workflows/ci.yml/badge.svg)](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/A-m-o-r-F-a-t-i/AgentDock-Workbench?display_name=tag&logo=github)](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/releases)
[![License](https://img.shields.io/github/license/A-m-o-r-F-a-t-i/AgentDock-Workbench)](./LICENSE)

</div>

<p align="center">
  <img
    src="./docs/assets/agentdock-multi-device.png"
    alt="AgentDock Workbench 通过一个 AI 对话管理多台设备"
    width="100%"
  />
</p>

## 项目定位

　　AgentDock Workbench 是基于[上游 AgentDock](https://github.com/uvwt/agentdock)独立维护的扩展项目。Core 通过 MCP 向 AI 提供文件、命令、Git、Skill、动态 MCP、浏览器自动化和部署等宿主能力，Workbench 则补齐持续任务所需的管理层，使操作不再是一组彼此孤立的工具调用。

　　本项目不提供聊天界面，也不执行模型推理。模型与对话由 AI 客户端提供，AgentDock Workbench 在已连接环境中执行经过授权的操作，并记录任务、调用、审批、输出和最终状态。

## Workbench 核心能力

| 模块 | 提供的能力 |
| --- | --- |
| 对话优先的导航 | 按工作区组织活动中与历史对话，支持搜索、置顶、归档、恢复和明确的选中状态。 |
| 可恢复任务 | 持久化目标、步骤、线程、检查点、阻塞原因和最终审查，使长任务可在中断后继续。 |
| 任务与活动中心 | 记录根调用与子调用、参数、输出、耗时、文件变化、错误、审批和终态，同时排除心跳、后台探针等诊断流量。 |
| 中途插入与停止 | 向正在执行的对话补充新要求，保存投递与回执状态，并可停止整个对话或指定调用。 |
| 分层权限 | 将 Permission Profile、Approval Policy 和 Approval Reviewer 分离，全局与工作区配置均可读回和审计。 |
| Skill、插件与 MCP | 在统一管理界面中发现、安装、启停和检查 Skill、自包含插件及动态 MCP Server。 |
| 多设备执行 | 连接本机、服务器、容器和移动节点，同时保持目标工作区、对话和调用身份正确绑定。 |
| 安装与恢复 | 使用明确的安装、更新、回退、保留和健康状态，避免未验证完成时提前报告成功。 |

## 系统结构

```text
 ChatGPT / Claude / Codex / 其他 MCP 客户端
                       │
              MCP + Bearer/OAuth
                       │
              AgentDock Core 运行时
       ┌───────────────┼────────────────┐
       │               │                │
 Workbench 界面      管理 CLI          工具运行层
       │               │                │
 对话与工作区        任务与调用         文件 / 命令 / Git
 权限与审批          日志与导出         Skill / 插件 / MCP
 插入与停止          状态管理           浏览器 / 部署
                       │
          本地电脑 · 服务器 · 容器 · 移动节点
```

　　对话身份由可信宿主元数据解析，任务与调用继承该绑定，审批直接关联被审查的具体 Call。管理界面不会通过当前选中的窗口或最近活动任务猜测执行身份。

## 平台状态

| 平台 | 使用形态 | 当前状态 |
| --- | --- | --- |
| Windows | 原生 WPF Workbench、图形安装程序、任务与活动中心、权限管理、中途插入、更新和恢复。 | 当前正式版的主要桌面体验。 |
| macOS | 原生 Swift/AppKit Workbench，与其他客户端共用 Core 管理契约。 | 在 v1.1.8 预发布版中扩展。 |
| Android | Kotlin/Jetpack Compose Workbench，连接外部 Termux/PRoot Core，并提供部署与保活管理。 | 在 v1.1.8 预发布版中加入。 |
| Linux | 无界面 Core 与管理 CLI，适合本机、服务器和脚本化操作。 | 通过发行包或源码构建使用。 |
| 容器 / VPS | 通过 MCP、CLI、认证和远程连接运行无界面 Core。 | 具体能力取决于所选发行资产与部署环境。 |

　　不同版本与 CPU 架构提供的安装资产可能不同，安装前应核对 Release 的文件列表和说明。当前 Latest 正式版为 v1.1.7，v1.1.8 仍是用于跨平台验证的预发布版本。

## 典型使用场景

- 让 AI 在目标电脑上修改真实工程、运行测试、检查失败原因，并提交已验证的结果。
- 浏览器断开后，根据已持久化的步骤与检查点继续数小时的任务。
- 查看一个对话实际触发的根调用、子调用、输出、错误和文件变化。
- 在任务执行中补充新要求，同时保留当前上下文，不创建重复任务。
- 对指定操作要求人工审批，并保持文件系统、网络和沙箱硬边界持续生效。
- 在一个对话中管理多台 AgentDock 节点，并将每次操作路由到正确设备和工作区。

## 快速开始

1. 打开 [Releases](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/releases)，选择正式版或当前预发布版。
2. 下载与操作系统和 CPU 架构对应的安装包。
3. 启动 AgentDock Core；平台提供原生界面时，同时启动 Workbench。
4. 获取 MCP 地址与 Bearer Token，或完成 OAuth 连接。
5. 将连接信息加入 AI 客户端的 MCP、Tools 或 Connectors 设置。

　　本机连接的通用配置示例如下：

```json
{
  "mcpServers": {
    "agentdock": {
      "url": "http://127.0.0.1:8765/mcp",
      "headers": {
        "Authorization": "Bearer <AGENTDOCK_AUTH_TOKEN>"
      }
    }
  }
}
```

　　所有非本机连接都应保持认证开启。不要公开 Token、OAuth 凭据、私有 Origin、审批请求正文，或包含敏感信息的执行日志。

## 项目文档

| 主题 | 文档 |
| --- | --- |
| 任务与活动中心 | [执行中心](./docs/execution-center.md) |
| Core 管理接口 | [执行中心 API](./docs/execution-center-api.md) |
| 权限模型 | [分层权限](./docs/permission-profiles.md) |
| 自定义权限设置 | [自定义权限配置](./docs/permissions-custom-settings.md) |
| 中途插入投递 | [插入投递机制](./docs/insertion-delivery-1.1.7.md) |
| AGENTS.md 上下文注入 | [Agent 上下文](./docs/agents-context.md) |
| Skill 与自包含插件 | [Agent 插件](./docs/agent-plugins.md) |
| Tailscale Funnel 连接 | [Tailscale Funnel](./docs/tailscale-funnel.md) |
| 与上游差异及迁移 | [版本差异与迁移](./docs/official-version-differences-and-migration.md) |
| 当前正式版 | [v1.1.7 版本说明](./docs/releases/v1.1.7.md) |
| 当前预发布版 | [v1.1.8 版本说明](./docs/releases/v1.1.8.md) |

## 仓库结构

| 路径 | 职责 |
| --- | --- |
| `cmd/` | AgentDock 命令行入口。 |
| `internal/` | Core 运行时、状态、权限、任务、调用、安装和平台服务。 |
| `api/` | 对外接口与 Workbench 管理接口定义。 |
| `desktop/` | 原生桌面客户端及桌面平台集成。 |
| `mobile/` | Android Workbench 与移动端集成。 |
| `core-skills/` | 随运行时发布的内置 Skill。 |
| `packaging/` | 安装器、发行打包和各平台交付文件。 |
| `docs/` | 架构、行为、版本、迁移和验证文档。 |

## 开发与验证

　　修改仓库前先阅读 [AGENTS.md](./AGENTS.md)。提交变更前运行完整检查：

```bash
make check
```

　　GitHub Actions 持续执行集成测试、静态检查、平台构建、安装包生成和发行验证。源码构建、安装包生成、安装测试与正式发布是不同交付状态，源码编译成功不等于安装或升级流程已经验证。

　　可复现的缺陷和功能需求统一提交到 [GitHub Issues](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/issues)。问题涉及执行失败时，应提供 Workbench 版本、操作系统、相关任务或调用状态，以及完成脱敏的日志。

## 与上游的关系

　　AgentDock Workbench 基于采用 Apache License 2.0 的[上游 AgentDock 项目](https://github.com/uvwt/agentdock)，并保留所需许可证与署名。本仓库独立维护 Workbench 新增功能、发行版、文档、安装行为和问题反馈。上游发行版与 AgentDock Workbench 的状态及安装边界并不完全兼容，切换前应阅读[迁移说明](./docs/official-version-differences-and-migration.md)。

## 许可证

　　本项目采用 Apache License 2.0，详见 [LICENSE](./LICENSE)。
