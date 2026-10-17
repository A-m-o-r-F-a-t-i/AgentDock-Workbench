# GPT/Codex 执行架构实施说明

　　本文对应《AgentDock-Workbench-GPT-Codex-Agent执行架构改进计划-来源校正版-20261015.md》。实现接续 PR #125 合并后的主线，在独立工作树和功能分支交付到 PR #126。版本号仍为 1.1.9；PR 保持草稿，尚未完成全计划验收。

## 正式 Patch 契约

```json
{"patch":"*** Begin Patch\n*** Update File: src/example.go\n@@\n-old\n+new\n*** End Patch"}
```

　　`apply_patch` 复用 ToolSpec 注册、Runtime 派发、Workspace 和权限入口。公开输入仅为 `patch`，执行目录、运行环境、Task、Thread 和审批预检 revision 由运行时继承。模型目录移除 `file_edit`，内部文件服务和历史活动数据继续保留。

　　支持 Add、Update、Delete、Move 及 V4A hunk。输入上限为 4 MiB、128 项文件操作，预检内存预算为 64 MiB。全部目标完成解析后，以已有跨进程文件锁按稳定顺序加锁，读取当前文件并模拟整个 Patch。审批预览保存 `source_revision`，应用前重新核对，以识别审批后的外部编辑。

| effect_state | 实际含义 | 后续处理 |
| --- | --- | --- |
| none | 预检失败，目标文件未写入 | 修正输入或冲突 |
| complete | 修改已应用并读回核对 | 使用实际 diff |
| partial | 部分变更已生效 | 检查 completed_changes、failed_change 和保留原件 |
| unknown | 实际效果尚未全部确认 | 读取当前文件和 diff 后再决定操作 |

　　应用失败不自动回滚或重放。原生平台和 WSL Helper 使用同一执行器，并保留既有受保护路径检查。已有文件保留 BOM、主导换行及未修改行字节。符号链接、特殊文件、重复目标、覆盖目标、路径越界和歧义上下文在预检阶段处理。

## Context Bootstrap 与详情交付

　　Context 先并发收集规则、插件快照、公共 Skill、Task、Git 和会话计数，再依据插件快照展开对应目录。可选目录失败进入 warnings，规则与工作区绑定独立校验。GitSnapshot 使用独立 revision，引用响应仍刷新 Git 状态。详细计时保留在 Activity。

　　模型首屏保留 runtime、workspace、workdir、Host 工具准确名称、低频 Bundle 及成员名称、动态 MCP 摘要、规则完整性与 revision、Task 绑定。`bootstrap_complete` 与详情序列的 `sequence_complete` 分开表示：前者完成后，长列表分页只形成 `category=detail` 的按需操作，不阻断与该详情无关的工作。具体写入仍须遵守已加载规则。

　　分页使用已有不可变快照和准确 cursor。续页保护绑定工作区和已加载规则；目录刷新和普通 Git 记账不会使规则未变的 Context 续页失效，AGENTS.md、工作区边界或 Git 根标记变化仍会拒绝旧 cursor。最终响应超出预算时，紧急投影仍保留 Bootstrap 路由字段，将详情指向 retained source。Context 引用响应只保留规则 revision 等短字段，并从已发布内存快照获取当前 catalog 摘要，不重新连接提供者。

## 工具目录与精确搜索

　　Host 工具、内置 Bundle 和动态 MCP 保持各自命名空间。低频 Bundle 仅保留 `system-management` 与 `artifact-publishing`。加载只改变当前对话工具可见性，返回准确成员、目录 revision 与 `host_refresh_required`；Host 需要重新获取 `tools/list`。Core API 继续执行既有权限检查。

　　MCP 搜索先区分精确 qualified/canonical/已注册 alias，再排序模糊候选。默认模糊候选数为 5，精确匹配保留在限额之前。唯一结果直接返回正式 callable、完整输入 Schema、目录 revision 和下一调用模板。Schema 较大时复用 `required_full` 无损分页，后续页保持同一输出类别，不通过再次 inspect 恢复被截断 Schema。

　　Catalog 使用 `fresh/cached/stale/refresh_failed/unavailable` 描述状态。未发现、过期、缓存限定或刷新失败的零命中保持非权威结果。Discovery 不执行业务工具，也不生成 Shell、Bridge HTTP、端口扫描或裸 RPC 替代路径。

　　提供者的 `notifications/tools/list_changed` 更新目录代次，并按 server/client 身份排队合并刷新。刷新复用当前连接和现有互斥锁，在锁内再次核对 server、client 和 generation，保护运行中调用。连续通知合并处理，刷新失败保留旧快照，不启动业务恢复。

　　目录发布及注册配置变化通过现有稳定工具描述符触发标准 Host `tools/list_changed`。通知在注册表和客户端锁外发送。关闭 Manager 时取消并收敛相关工作协程；退役连接的通知不能刷新替代连接。

## 命令与设备契约

　　`exec_command` 缺省前台等待为 10 秒。`session_observe` 使用 stdout/stderr UTF-8 字节位置和一次有界等待。无变化观察与已交付终态使用短回执，完整输出继续由原 retained source 提供。

　　命令 `operation_status` 根据真实进程状态和 `command_ok` 生成，区分 running、succeeded、failed、outcome_unknown。成功读取终态不会把失败命令标为成功。Session 列表、批量停止和单次观察统一使用 cancelled 等公开状态名称。

　　MCP initialize 只交付稳定设备契约与 operator instructions；AGENTS.md 经 Context 按作用域交付。设备契约说明精确解析、目录刷新、业务优先、连接错误后恢复和未知效果核对。可信插入仍沿用既有认证与回执边界。

## Managed MCP 恢复与统一结果

　　真实 `mcp_tool_call` 只有在业务调用返回受控连接类错误、且实际效果为 `none` 时，才在原 Activity 上建立十分钟恢复资格。资格校验固定认证所有者、Conversation、Workspace、server、执行配置指纹与工具 Schema；领取、恢复和一次性重试消费均写回原调用，并复用 Activity 的跨进程日志锁。

　　`mcp_manage recover` 接收原 `origin_call_id`。恢复 Skill 在标准 frontmatter 的 `metadata.mcp-recovery-server` 中声明目标服务；`exec_command` 携带准确 `skill_ref` 与 `origin_call_id`，继续经过现有权限审批。恢复资格先原子领取，再关联恢复调用和唯一重试调用；并发领取只有一个成功者。恢复启动失败、异步失败、取消或到期均落下终态，不能留下永远占用的恢复资格。恢复完成后只重试原业务参数一次，目标或 Schema 改变、原参数完整源缺失或被脱敏时停止重放。未知副作用只允许恢复连接，仍要求独立业务读回。

　　Canonical Result 统一记录 `operation_status`、`error_kind`、`effect_state`、`execution_performed`、`retry_safe` 和分类 `required_action`。相同认证作用域、Workspace、参数、原因和目录 revision 的重复协议前置条件失败，返回首个 origin、原恢复动作与递增 `repeat_count`；完整结果先留存，再生成模型短回执。

## 完整输出校验与失败续页

　　Runtime 在模型投影前校验 handler 的完整成功结果，输出验证器与输入验证器一起由正式 ToolSpec 编译。实际业务失败保留原始失败分类；已冻结快照的显式续页不重新执行 handler。上游 MCP 声明的 outputSchema 在业务调用前编译，对原始 structuredContent 的校验也发生在分页前。无效 Schema 在执行前拒绝，执行后的输出不符则保留实际执行与副作用事实，不把未验证结果当作可安全重试。

　　输出校验失败仍可通过准确 cursor 读取保留的原文，但每页继续携带原失败，不能记为已成功交付、不能形成隐式去重引用，也不能通过读取 source 绕过 required-full 确认。read_file 在 handler 阶段已创建的快照由相同所有者、对话、任务和工作区范围回写失败标志，不影响别的调用。

　　上游业务失败的分页保存有界 `upstream_failure`，在首屏、语义续页、fragment 和原文读取中保留失败码、实际效果及后续动作。后续动作要么完整保留，要么明确列入 omitted_fields，不截断成不可执行的半个请求。原始输出仍由现有完整来源保存。

　　Standalone Context 的 ACP 摘要由 Context owner 声明当前的 `default_profile` 与 `profiles` 契约；顶层和 profile 条目继续使用闭合 Schema，拒绝旧 `agent` 别名和私有命令字段。此修正不改变 ACP 执行功能，也不放宽共享协议的其他字段。

## 惰性目录缓存与 Activity/UI

　　成功发现的完整 MCP 目录可保存在受管缓存中，单项上限 8 MiB、最多 32 项、有效期 24 小时。`lazy=true` 的服务只在缓存身份与当前执行配置匹配时使用缓存 Schema；首次实际业务调用才启动提供者并重新校验当前 Schema。过期、缓存限定与刷新失败的零命中保持 `absence_authoritative=false`。

　　Activity 新增有界的 catalog、route、match、required action、业务效果和 recovery 字段。Windows 活动摘要与悬停详情、macOS 技术详情已显示 catalog state/revision、匹配方式、Host 刷新要求、恢复 origin 及消费状态；历史记录缺失字段保持未知，不补成成功或零值。

## 验证状态与剩余工作

　　验证必须关联实际源码版本。以下既有证据来自 `f8571f0` 候选及随后提交的证据文档，不代表后续审查补丁或最终合并主线已通过同一验证：

- 按 CI 使用的 `go test -p 2 ./... -count=1 -timeout=8m` 设置完成全量 Go 复验；真实 HTTP Context 探针完成固定 Bootstrap、语义分页重建和关联字段检查。
- 重新编译的 Windows 候选 `candidate-post-main-core.exe` 的 SHA-256 为 `1fde9ac8440e7d9068c86b5e411b68f4169145dd89ee85642032a571154f9081`。
- 隔离候选通过真实 EasyEDA managed-MCP 回归：加载插件、精确发现四个 PCB 工具、读取完整 Skill、无重放消费六页状态；添加未保存的板外临时丝印，独立读回精确句柄，删除后再次读回。最终区域为空，document revision 恢复到初始值，未保存工程文件。候选进程已停止。证据见 `docs/validation/1.1.9/easyeda-managed-mcp-live.json`。

　　基于 `325777f` 的后续审查补丁已通过 Linux 上 activity、MCP client、MCP 工具、文件和 workspace 包验证，以及完整输出校验、失败续页、原子恢复和 Context 规则续页回归。app 包复验发现 ACP 正式 Schema 漂移，修正后三项原失败及共享契约检查均通过，随后完整 app 包通过（25.821 秒）。文件权限回归在保留实际权限限制的环境运行。格式化与 `git diff --check` 通过；这些局部证据不替代修复提交和最终合并主线的 Actions。

　　Windows/macOS 活动详情适配已进入分支，原生编译由仓库现有 Actions 验证。设备连接与源码推送状态单独记录，不用候选编译成功推断平台构建、生产安装或发布完成。

待完成项：

- 提交本批审查修复，完成 PR #126 的既有 Actions，按实际失败修复剩余契约或平台问题。
- 统筹 Plugin 生命周期、执行架构、Context、安装器、媒体预算及公开品牌等 Core 变更。合并时逐项核对恢复资格、Plugin generation/lease、完整来源、目录身份和作用域边界，不以文本无冲突代替语义审查。
- 功能 PR 全部合并后，必须另交一个 Core 集成与代码质量 PR，修复已复现的交互问题、收敛公共实现与代码风格，并运行与实际风险对应的既有回归和 Actions。
- 在质量 PR 合并后的准确主线提交构建 1.1.9 预发布候选，完成既有安装/发布门禁与必要实机读取，核对八项资产、摘要、标签提交及发布说明后再发布。

　　PR #126 当前保持草稿。先前设备或其他任务更新的生产 Core 不作为本分支部署证据；本轮合并与发布结果以对应提交、Actions 和 release 记录为准。
