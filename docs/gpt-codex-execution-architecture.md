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

　　分页使用已有不可变快照和准确 cursor。最终响应超出预算时，紧急投影仍保留 Bootstrap 路由字段，将详情指向 retained source。Context 引用响应只保留规则 revision 等短字段，并从已发布内存快照获取当前 catalog 摘要，不重新连接提供者。

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

## 验证状态与剩余工作

　　已完成各批修改的差异审查和 `git diff --check`。当前设备执行使用独立工作树，生产 Core 未替换。上轮离线补丁已写回设备并提交，不再仅为沙箱附件。本轮未运行额外本地测试或安装操作。

　　GitHub Actions 已随提交触发。提交 44766e6 的 Ubuntu CI 中，`internal/mcp/client` 包通过；整体 CI 仍在 Test 阶段失败，不能据此判定整项计划通过。初始化常量、Patch 请求类型和跨平台公开参数断言已迁移，其他失败仍需逐项区分契约变化与运行时问题。

待完成项：

- 迁移余下 `file_edit` 调用夹具、Context/Session/工具发现断言及协议探针，保留实际权限、审批和审计要求，完成现有 Actions 复验。
- 基于原 Activity 校验 Conversation、Workspace、server、连接错误及有效期的 managed-MCP 恢复资格、消费记录和一次性原调用重试。目前仅具备设备契约指导，未实现该运行时机制。
- 重复 Bundle/前置条件失败的稳定 origin reference、repeat_count 和短回执，以及统一错误、副作用字段与完整输出 Schema 验证。
- Lazy 缓存持久化与首次业务启动校验的完整生命周期核对、恢复 Skill 的 origin call 约束及正常业务路径迁移。
- Activity/UI 的 catalog、路由、required action 和 recovery origin 展示，以及其余平台专用展示。
- 使用候选构建完成真实 managed-MCP 业务读取、最小编辑和独立读回回归。当前运行设备的旧 Core 不计作新代码验收。

　　PR #126 尚未合并、发布或安装。实现存在上述缺口，完整验收状态保持未完成。
