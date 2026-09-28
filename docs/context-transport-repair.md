# Context 请求链路修复

## 基线与证据

- 基线：v1.1.8，4bd778d4077bbe58cfe19e4abb777f660694377b。
- 工作树：OPPO /root/AgentDock/workbench-parallel-20260925/worktrees/14-context-transport。
- 任务：tsk_56df71553fe6e2bb。分支：fix/20260928-context-transport。
- 2026-09-28 原失败时间窗未查到 Activity 根调用。缺少 HTTP 与宿主关联日志，不能仅凭 Activity 缺失证明请求没有到达 Core。
- 正式 connector 复测可用，手机当前运行 1.1.6。本修复不安装、不重启、不改生产配置。

## 实施计划

1. 为 HTTP/MCP/Activity 添加同一请求 ID，在鉴权和协议解析之前记录入口，在响应写入之后记录字节数、取消和写入错误。ID 不参与鉴权、对话识别或重试去重。
2. 加固仓库自有 InvokeProjected：捕获投影/提交回调 panic，提供结构化阶段错误与原 call_id，保持业务结果和插入回执，不重放已派发工具。bootstrap 的完整结构不得被 count-only 投影裁剪。
3. 完整结构化上下文保持不变。默认保留 MCP 兼容文本，明确协商后仅提供有界摘要，记录真实响应大小。MCP 2025-11-25 建议同时返回 JSON 文本，不向未知客户端强制取消。
4. 提供鉴权、无自动重试的标准 MCP 深度探针，覆盖 initialize、tools/list 与 agentdock_context。复用正式协议，不创建第二套 Runtime。
5. 添加 Linux/Windows/arm64 隔离回归和 GitHub Actions。提交源码和证据，不修改原 Release。

## 实现契约

### 请求关联

HTTP `/mcp` 和 `/context` 在鉴权前生成请求 ID，并在响应头返回 `X-AgentDock-Request-Id`。调用方可提供 `req_` 加 32 位小写十六进制字符的 ID，其他值不记录，改为生成新 ID。这个值只用于日志关联，不是对话身份、权限依据或幂等键。同一请求 ID 的不同调用仍各自获得独立 call_id。

HTTP 收尾记录实际写入字节数、请求取消、写入失败和异常退出。MCP 结果的 `_meta.agentdock/transport`、工具指导信息与 Activity 根调用携带关联标识。协议错误保留原 JSON-RPC code/message，并在 data 中附加 stage、request_id、backend_call_id、handler_dispatched 和 retryable。handler_dispatched 表示进入业务处理函数，不等于所有副作用已完成，也不等于客户端已收到结果。

### 宿主投影

`InvokeProjected` 对投影回调与上下文提交回调的异常、panic 分别报告 `host_projection`、`host_context_commit` 等阶段。错误不回显原始 panic 内容，不重执行已派发工具，不把传输错误改写成业务执行失败或接收成功。完整 bootstrap 结果不再允许被 count-only 等外部投影裁掉规则。外部未调用本适配器的执行器不受此修改影响。

### 上下文响应

默认模式继续在 structuredContent 与文本中提供完整上下文，以兼容旧客户端。客户端明确保证消费 structuredContent 时，可在工具请求的 `_meta` 中传入：

```json
{"agentdock/context-response-v1":{"structured":true,"text":"summary"}}
```

摘要正文小于 1024 字节，所有规则、工作区、Skill、任务和警告仍保存在 structuredContent。错误结果保留错误文本。可信中途插入继续单独追加，不计入摘要正文上限。适配器也可显式调用 `WithStructuredContext`。

`_meta.agentdock/context-response-v1` 给出 structured_bytes、text_content_bytes、response_bytes、compat_text_mode、response_truncated。response_bytes 是序列化后的 CallToolResult 字节数，包括当前 SDK 的结果修饰字段，不包含 JSON-RPC 外层、HTTP 头和分块编码。HTTP 日志的 bytes 独立反映实际写入量。旧协议与新协议分别验证，不用结构化内容大小冒充网络流量。

### 深度探针

`scripts/diagnostics/probe-context.py` 使用正式 MCP 协议执行 initialize、initialized 通知、tools/list、agentdock_context，校验完整性并只输出元数据。令牌从 `--token-file` 读取，远程必须 HTTPS，禁止重定向，每次响应限制 2 MiB，目录最多 8 页，无自动重试。SSE-only 响应会被明确拒绝为非 JSON；当前 AgentDock 配置使用 JSONResponse。

```sh
python scripts/diagnostics/probe-context.py --endpoint https://NODE/mcp --token-file /private/token --summary
```

此探针验证实际上下文构建，不是 `/healthz` 的廉价替代。它会产生正常的只读 MCP 调用记录，按需运行，不用于高频保活。

## 验证范围

新增测试覆盖请求 ID 格式和并发、不同对话隔离、Activity 读回、HTTP 写入失败/取消/panic、协议错误阶段、完整规则保留、协商拒绝、超过 1 MiB 的结构化上下文、SDK 实际编码大小、宿主回调 panic、插入回执以及外部 Python 探针连接真实隔离 HTTP Server。Windows 模型测试只运行纯模型项目，不启动桌面程序或安装器。

专属 Actions 工作流 `context-transport.yml` 验证 Linux amd64、Linux arm64 与 Windows。所有集成测试使用临时状态目录和随机回环端口，不接管生产服务。测试执行状态以对应提交的实际运行结果为准。

## 已执行的本地验证

2026-09-28，OPPO 手机的 Termux/PRoot Linux arm64 环境，缓存 Go 1.26.5：新增 12 个顶层 Go 回归测试各重复 3 次，36 次顶层执行全部通过，包含子测试共 60 次通过；Python 探针 5 项单元测试全部通过。Go 回归包含 Python 探针通过真实隔离 HTTP Server 的完整协议往返。日志保留于 `/storage/emulated/0/Termux/AgentDock/Artifacts/context-transport/targeted-tests.jsonl`。

这不是手机生产 Core 的升级验收。完整受影响包测试、跨平台与 race 结果以各自实际日志和本分支 Actions 为准。

## 不在本仓库内的宿主边界

外部 ChatGPT JavaScript 执行器的工具名解析、请求发送实现不在本仓库中。本次只能修复自有适配器并提供可关联证据，不能宣称已修改该外部包装器。调用方应直接使用宿主发现的正式工具名称。已派发或结果未知的业务请求禁止盲目重放。
