# 结构化输出预算与源完整性修改计划

## 1. 范围

本计划仅补齐运行记录暴露出的新增遗漏，不重复已经分别实施的成功回执压缩、`output_policy` 稀疏化、结构化结果单表示、Android 工具条件注册、`agentdock_context` 范围收缩和公开 Schema 分离。

本次处理两类问题：

1. `search_text`、`list_dir`、`mcp_tool_search`、`mcp_tool_list`、`mcp_tool_inspect`、`agentdock_context` 的结构化数组可能绕过普通文本上限，单次响应可增长到数十 KB 或更高。
2. 命令会话只报告展示截断，无法直接区分全部历史输出已留存、缓冲区前缀已丢弃和当前页尚未展示。

版本继续使用 `1.1.9`，不增加版本号。

## 2. 结构化结果预算与分页

- 在 Runtime 的模型输出边界统一应用结构化结果预算，默认 24 KiB，调用方可在 4–64 KiB 范围内显式调整。
- 对上述六类工具按稳定集合路径分页，例如 `matches`、`entries`、`tools`、`tasks.items` 和 `instruction_files.files`。
- 首次超预算时保存不可变结果快照。后续调用携带服务端签发的 `cursor`，从快照继续读取，不重新执行搜索、目录扫描、MCP 发现或上下文装载。
- 游标绑定原工具、原选择参数、认证来源、对话/任务和工作区。参数变化、跨作用域读取、过期或已回收快照均返回明确错误。
- 内存快照保留 15 分钟，单快照上限 16 MiB，进程级缓存上限 64 MiB；过期和超额内容按最旧快照回收。
- 完整结果同步写入脱敏后的活动输出源。单个集合项超过最大语义页时，返回该项位置、序列化大小和 `activity://call/.../source` 读取入口，避免静默丢弃或反复执行原工具。
- 分页元数据只在结果超预算时出现，包含实际页大小、源大小、返回项数、源完整状态、游标有效期和续读信息。

## 3. 命令输出源完整性

`exec_command` 和 `session_observe` 增加统一字段：

```json
{
  "source_complete": true,
  "displayed_bytes": 4096,
  "total_output_bytes": 180000,
  "retained_bytes": 50000,
  "dropped_before_bytes": 130000,
  "offset": {"stdout": 130000, "stderr": 0},
  "next_offset": {"stdout": 134096, "stderr": 0},
  "offset_unit": "utf8_byte"
}
```

- `source_complete` 表示当前已产生的 stdout/stderr 是否仍全部保存在有界会话缓冲区中。
- `dropped_before_bytes` 报告因缓冲区回收而不可恢复的前缀字节。
- `offset` 和 `next_offset` 使用绝对 UTF-8 字节位置，并分别记录 stdout、stderr。
- 零值流统计、空字符串和 `false` 截断标记默认省略；无新增输出的观察结果只保留会话状态、必要计数和游标。
- 多字节 UTF-8 字符不得被分页切断，游标只按实际交付字节推进。

## 4. 实施步骤

1. 在共享工具契约中增加结构化分页输入、输出字段和预算范围。
2. 在 Runtime 执行返回边界实现不可变快照、授权游标、语义化分页、超大单项降级和活动源留存。
3. 为文件搜索、目录列举、MCP 目录/Schema 和 AgentDock 上下文接入统一分页契约。
4. 为命令会话快照补充绝对字节偏移、留存量和丢弃量，并稀疏化空观察响应。
5. 更新模型摘要和 guidance，使模型能够沿游标或活动源继续读取，同时明确续读不会重新执行原操作。
6. 在最新 `origin/main` 上解决与公开/验证 Schema 分离、紧凑上下文实现的接口差异。

## 5. 验收

- `search_text`、`list_dir`、MCP 目录/Schema 和 `agentdock_context` 默认单页结果受 24 KiB 预算约束。
- 使用 `next_cursor` 能完整恢复所有分页项，原处理器只执行一次，顺序稳定且无重复、无遗漏。
- 修改原选择参数、跨认证来源/工作区使用游标会被拒绝。
- 超过 64 KiB 的单个结构化项不会进入模型主响应，活动源可继续读取。
- 命令会话在完整留存、部分丢弃、UTF-8 多字节、输出增长、并发观察和进程结束场景下均返回准确字节范围。
- 无新增输出的 `session_observe` 业务结果不超过 1 KiB。
- 定向测试、相关包测试、全仓 Go 测试及 `go test -race` 通过；环境波动测试需隔离复测并记录真实结果。
- 分支推送后创建面向 `main` 的独立 PR，发布版本仍为 `1.1.9`。
