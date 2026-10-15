# GPT/Codex 执行架构实施说明

　　本文对应《AgentDock-Workbench-GPT-Codex-Agent执行架构改进计划-补充版-20261015.md》。实施分支以 PR #125 合并后的 main 为基线，交付到 PR #126。本说明区分已实现接口与仍需验收的内容，不改变 1.1.9 版本号，也不授权合并或发布。

## 正式 Patch 契约

```json
{"patch":"*** Begin Patch\n*** Update File: src/example.go\n@@\n-old\n+new\n*** End Patch"}
```

　　`apply_patch` 通过原 ToolSpec 注册和 Runtime 派发。公开输入仅有 `patch`，Workspace、权限、Task、Thread 和预检 revision 由运行时继承。模型目录移除 `file_edit`；内部文件修改服务与历史活动 JSON 的 `file_edit` 字段继续保留，旧活动读取不会被迁移破坏。

　　支持 Add、Update、Delete、Move 及 V4A hunk。输入上限为 4 MiB、128 项文件操作，预检内存预算为 64 MiB。全部目标完成解析后，以现有跨进程文件锁按稳定顺序加锁，再读取并模拟整个 Patch。预检冲突返回 `effect_state=none`。审批预览记录 `source_revision`，应用时重新核对，外部编辑使旧审批失效。

| effect_state | 实际含义 | 后续处理 |
| --- | --- | --- |
| none | 目标文件未写入 | 根据具体输入或冲突修正 |
| complete | 请求修改已应用并核对 | 使用返回的实际 diff |
| partial | 部分变更已生效 | 检查 completed_changes、failed_change 和保留原件 |
| unknown | 无法确认全部实际效果 | 读取当前文件和 diff 后再决定操作 |

　　正式 Patch 应用失败不会自动回滚或重放。原生执行和 WSL 文件 Helper 复用同一执行器；WSL 仍执行既有受保护路径检查。Windows 使用平台默认新文件换行，已有文件保留 BOM、主导换行和未修改行的字节。符号链接、特殊文件、重复目标、覆盖目标、路径越界与歧义上下文在预检阶段处理。

## 命令观察与 Context

　　`exec_command` 沿用 10 秒缺省前台等待。`session_observe` 接收上次 `next_offset` 的 stdout/stderr UTF-8 字节位置，单次请求最多执行一次指定等待。状态和字节位置均未变化时，模型投影返回 `changed=false` 紧凑回执；诊断次数保留在 Canonical Activity。已交付终态复用持久回执，原命令完整输出由 retained source 按需读取。

　　Context 先并发收集规则、插件快照、公共 Skill、Task、Git 与会话计数，再基于插件快照并发展开 Skill、动态 MCP 和插件摘要。可选目录失败进入 warnings，规则与绑定仍独立校验。GitSnapshot 只运行本地只读 Git 命令，使用独立 revision；Context 引用响应也会刷新 Git 状态。详细组件计时保留在 Activity，默认模型视图只显示完整性和绑定状态。

　　MCP 初始化交付单一设备执行契约。低频工具通过现有 `capability_load` 目录按需公开，重复加载返回 `changed=false`。该目录只控制模型可见性，Core API 仍执行原有权限边界。旧 `file_edit` 的禁止和审批规则继续约束新工具，原先仅允许单一 add/replace 动作的规则不会扩大为任意 Patch 许可。

## 当前验收范围

　　已完成 Windows Core/MCP 与 Linux/WSL Helper 定向编译。两项原生 Patch 定向检查已通过，覆盖全目标预检零写入、BOM/混合换行保留、移动与删除、实际 diff 和审批 revision 失效。Windows 工具标题、文件定位与 Patch 效果详情已更新。版本说明在保留原 1.1.9 完整范围的基础上增加本次功能。

　　首轮 GitHub Actions 未通过。失败涉及旧 `file_edit` 调用夹具、初始化和 Context 固定指导预期，以及工具包可见性对直接 Core 调用的影响。后者已按“仅模型可见性”边界修正；其余旧契约迁移与全量 Actions 复验仍待完成。

## 未完成项

- 所有 ToolSpec 的运行时输出 Schema 校验及统一错误、副作用字段仍需补齐和审查。
- 可信插入仍需将逐条固定说明收敛为协议版本与真实事件，保留既有认证回执。
- Task/Thread Handoff 字段精简与检查点批量步骤更新需要核对现有实现。
- macOS、Linux/Android 的专用 Patch/Session 展示，PowerShell 5.1 复合命令诊断，以及旧 Skill/协议/测试中的模型工具引用迁移尚未完成。
- 实机端到端指标（重复交付、无变化观察正文、路径隔离和部分失败恢复）尚未完成验收，不将实现或编译结果换算为零缺陷指标。

　　PR 保持草稿，未合并、未发布、未替换当前设备运行的 Core。
