# Core 集成质量审查与验证（1.1.9，预提交快照）

> **状态截止：2026-10-17 约 08:31 UTC。** 本文保存该时点的源码、历史验证及运行中门禁。根流程将在本轮功能 PR 全部合并后，依据实际 main、独立质量 PR 和对应检查更新末尾交付入口；本快照中的“运行中”不预示最终结果。

## 目的与基线

本记录依据《AgentDock-Workbench-GPT-Codex-Agent执行架构改进计划-来源校正版-20261015.md》及[实施说明](../../gpt-codex-execution-architecture.md)，检查多个 Core PR 合并后，原有功能目标是否仍由实际执行链路实现。修复沿用现有输入、输出、Activity、命令、安装器和插件责任边界；本记录不把编译成功等同于运行正确，也不把某个功能分支的检查等同于独立质量 PR 的检查。

| 项目 | 本文记录的实际状态 |
| --- | --- |
| 本地及设备汇合审查源码 | `7e60922f8ea73a553de688dea261ff3a41cf55ad`；包含 `bdbf461` 的验证文档、`243a801` 的 Skill 准备分类修复及承接 `32e697f` 的 `836fb50` 安装器修复。实机 smoke 的可执行文件仍对应 `516a3ba` |
| 功能分支当前 head | #126 `836fb50846cfa7143bb219fd82318c71877d2aee` 已推送；#140 `898bc766c490d448306fbbe623a8ee5661e903c6` 已同步相同安装器三文件并推送；#142 已通过 `2fe6b93` 并入本轮主线来源 |
| 历史 Linux 全仓 Go | `a7f2bba` 已执行；只有 installer 的服务账号夹具失败，整体命令仍为失败，详见验证状态 |
| 当前 Windows 全仓 Go | `7e60922` 正在执行；会话 `session-5432d9402b55160d33ad2ba1`，日志 `core-quality-7e60922-windows-full-go.log`。截止本快照尚无完成结果 |
| 功能 PR 当前检查 | #126 `836fb50` 与 #140 `898bc76` 的新 head CI 运行中，截止本快照暂无失败报告，尚未获得最终结论；#140 旧 head `c517f1b` 的 37 success、2 skipped 仅为历史证据 |
| 独立质量 PR | 尚未创建；必须在本轮功能 PR 全部合并后的 main 上单独创建 |
| 最终 main / quality head / PR 编号 | **待最终合并后填写** |
| 最终 CI、构建、安装与发布 | **待按最终提交分别填写**；本文不宣布完成 |

本文同时记录已回送功能分支的交互修复和仍在独立质量分支中的修改。正式 PR 以最终 `base main..quality head` 的实际差异为准，不重复提交功能分支已经吸收的实现。例如 direct guidance 已进入 #126，验证目标仍须在最终汇合集保留。

## 功能目标与审查范围

| 来源 | 必须保留的目标 | 本次核对的交互边界 |
| --- | --- | --- |
| #125、#126 执行架构 | Canonical Result 校验和留存先于模型投影；原调用恢复受作用域与一次性消费约束 | 动态 JSON 参数、原请求读回、Schema 数值、命令结果与 Activity 来源 |
| #127/#128 的参数和输出目标，以及 #130/#137 的后续主线实现 | 完整参数可审查，保留非等价文本、原生内容、领域 cursor；宿主续页归入原调用 | 去重只移除可证明等价的副本；首页、续页、诊断读取不替换完整来源 |
| #134 媒体预算、#138 文件对象 | 对最终 SDK envelope 计量；保留有效原生 content 与正式文件输入 | 原生媒体计量与完整来源回退仍使用已有实现 |
| #135 审阅绑定、#139 Skill 事务、#140 Plugin 生命周期 | 执行固定审阅内容、generation、digest 与运行副本；释放归属明确 | 恢复资格、WSL 环境、稳定目录缓存身份、旧连接在途调用的 runtime lease |
| #141 Linux/browser | 服务、Core、Skill 使用同一运行目录；请求超时覆盖浏览器启动 | 已提交 Home 的恢复顺序及浏览器调试连接等待 |
| #142 工具域 | 设备内置工具直接调用，动态 MCP 通过 qualified selector；错误指引不重新混淆域 | Context、目录/Schema 引用和 `next_required` 保留相同域与调用方式 |

#133 的公共名称兼容与旧安装识别夹具作为既有基线保留。本次没有用重复实现替换已被主线覆盖的旧 PR；公开发行说明仍描述相对 v1.1.8 的完整用户变化。

## 已复现问题、修复责任与保留行为

| 问题 | 实际复现或代码失配 | 现有责任边界中的修复 | 必须保留的结果 |
| --- | --- | --- | --- |
| 精确 JSON 与恢复参数 | 动态解码/参数克隆/缓存读回先转 `float64`；相邻大整数可能被当作同一 JSON 文本，目录 digest 可能在重启后变化 | 公共 `tool/core.DecodeJSON`、`Remarshal` 与保守的 `JSONTextEquivalent`；Core 输入、克隆、恢复读取、目录缓存和文本去重复用它们 | Core 自有边界保留 `json.Number`；类型化字段仍执行原范围检查；非等价文本及 native metadata 不被去除 |
| 超大指数导致验证器 panic | `1e1000001` 可以通过 JSON 解码，但底层数值比较遇到不可表示的 `big.Rat` 后 panic | `tool/contract/validator.go` 在现有 Schema 编译和值归一化边界同时检查数值可表示性 | 明确拒绝不可表示的值和 Schema 约束；保留正常大整数、精确小数及可表示的 `1e309`，不用 recover 吞 panic 或退回浮点数 |
| 完整来源被宿主页覆盖 | 默认 Activity `OutputSource` 变成首个约 5.7 KB 的宿主短页；来源保存失败时却出现 `source_complete=true` | `app/specs_mcp.go` 先留存原响应；`prepareModelOutput` 和 `RetainResponseSource` 复用原 MCP owner 的完整源，禁止把 host page 再存成默认源 | 完整错误 envelope、`_meta`、非等价文本和原生内容可独立读回；保存失败不改变实际业务状态，也不重跑业务 |
| 子 Activity 续页丢失来源状态 | 续页折叠为 child 后触发通用提前返回，`capture` 与 `source_complete` 消失 | 只让真实 `mcp_result_continue` host page 进入对应投影；`mcpResultOrigin` 校验原调用作用域，按精确 `raw_result_ref` 读取原来源或保留版本 | 所有成功页引用相同来源；来源失败继续为 false；末页完成只表示交付完成；child 不创建新的 OutputSource |
| Context 工具数量错误 | `Remarshal` 改为 `UseNumber` 后，旧的 `tool_count.(float64)` 把实际非零目录报为 0；旧转换还会截断小数或使越界值变负 | `contextCatalogSummary` 使用现有严格整数 helper；限定数值类型并检查负数、单项范围和合计溢出；reference 直接保留 `Snapshot.ToolCount` | full/reference 使用同一数量和目录状态规则；非法值令数量未知，不伪造已知 0；#142 域字段在同一 summary owner 中生成 |
| 命令与 Android 输出合同不一致 | 三个命令工具的空成功结果错误通过已注册验证器；收紧后暴露 Android status 错用 exec 根 Schema | 命令 owner 要求 session/status 或显式 sessions/count；Android owner 只复用公共字段，并按 action 用 `if/then/else` 定义自己的分支 | operation/capture 元数据不能代替业务结果；Android status 要有 device_status，执行分支保留 session/status 与 action/backend |
| WSL Skill 环境与 Android 直接入口 | WSL 忽略 `skill_env`，未从真实 Plugin lease 派生私有数据路径；WSLENV 继承 `/p`、`/w` 会重复转换或抑制转发；Android Service.Exec 携带 origin 仍创建 queued session | native/WSL 共用 `applySkillEnvironment`；由 lease.PluginName 创建私有数据目录并转 POSIX，清理受管变量的 WSLENV 修饰；Android owner 在 broker dispatch 前拒绝 `origin_call_id` | 独立环境选择保留默认 workdir；普通环境变量、冲突检测、受管变量保护及原 lease release owner 保持一致；Android 拒绝时零派发 |
| Skill 准备失败误记业务执行 | `516a3ba` 实机请求在 20 秒准备预算内未完成 Plugin runtime 文件校验，没有 Session，却被通用失败投影记为 `execution_performed=true/effect_state=unknown`；Runtime 回归同时复现返回与 Activity 误分类 | `243a801` 仅在 native/WSL 共用 `acquireSkill` 的 resolver 错误返回点保留 cause 与已有 ToolError 信息，并附明确的非执行事实；该边界位于恢复授权和 `invocation.start` 之前 | `execution_performed=false/effect_state=none/retry_safe=false`；保留内部运行副本及 Activity 可能写入的事实。命令启动后的失败、远端 backend、超时预算和 Plugin 完整摘要校验保持原语义，不按 deadline 或缺少 Session 全局推断零副作用 |
| Lazy 缓存身份不稳定 | 同一 Plugin generation 只重建 `.runtime` UUID 后，持久目录变成 fresh 且 preparations=1 | `mcp/client` 的持久缓存身份使用 immutable 源、路径映射、generation/digest；loader 保留默认来源与显式覆盖的区别 | 重建运行目录后仍 cached 且零准备；首个业务调用使用当前 Root；显式 command/args/cwd 覆盖和真正版本升级仍使缓存失效；正式执行/恢复 fingerprint 不放宽 |
| 旧连接提前失去 generation 文件 | 配置异步 retire 旧 serverState 后，Plugin 当前集合 lease 先释放；阻塞中的 stdio 调用在 update/remove/member_disable 三个场景均读不到原 `.runtime/lease.txt` | `mcp/client` 仅在真实 initialize 时取得连接专属 runtime lease；refresh 成功转移、失败/取消释放候选；由既有 connection retirement 释放原 lease | 在途调用独立读回原 generation；连接关闭后才清理运行目录；不让慢旧调用阻塞全局目录操作，不改变 Lazy 发现行为 |
| Linux Home 与 browser timeout | 自定义 Home 升级、新装默认值、中断 trial 回滚恢复三个场景失败；本地浏览器连接等待未完整使用请求期限 | shell adapter 只传显式路径；Engine 在安装锁和事务恢复之后 hydrate 已提交 Home；browser 将同一 req.Timeout 传给 allocator URL 等待和 dial timeout | 显式路径优先，其次已提交 Home，再次新装 DataDir 默认值；服务 env、Core、Skill 和已有 auth token 保持一致；启动失败按请求期限返回 |
| Windows 升级时 launcher 子进程停止不完整 | 旧 `ddf5ff7` 的同一真实父子进程回归失败并遗留 Tray；`32e697f` 的后续验证又暴露 PowerShell 5.1 可选 `[ref]=$null` 绑定错误 | 累计修改 `scripts/install/install.ps1`、`scripts/test/install_windows_test.go` 与 `scripts/test/setup_runtime_stop_windows_test.go`：匹配精确安装路径，固定已验证进程句柄，等待 stable launcher 退出后重新查询 generation；`836fb50` 在没有观察引用时省略相应参数 | 停止与等待使用同一责任边界，保留其他安装的进程；rollback 调用不污染升级前的运行快照。15 秒仅是共享等待/重试预算，不表示 WMI 查询有硬截止；保留既有回滚状态机 |
| #142 域在后续 guidance 中丢失 | Schema reference 未携带完整动态路由；内置错误 guidance 仍构造 `invoke_via=host`，与新的 direct 合同不一致 | 动态 reference 保留路由元数据；Runtime resolver 和 `hostBuiltinHandoff` 生成 `agentdock_builtin/direct`，沿用本地 provenance 检查 | Context、引用、错误 next_required 使用同一域；指引不伪造执行参数，不派发业务；伪造 provenance 仍不能成为可信恢复指令 |

有界样式整理只处理本批修改中混放标准库和项目包的 import 块，保留别名、注释和函数实现。最初涉及六个文件；其中 `host_builtin_handoff_test.go` 的同样分组已被后续 #126 修复吸收，最终差异不重复叠加该改动。

## 回归与前后对比证据

测试环境使用 Go 1.26.5。下表中的局部耗时和结果来自对应修复阶段，不冒充最终 quality head 的一次全量运行。日志名用于对应实施记录；本地日志不是已上传的 Actions 附件。

| 范围 | 可定位的回归与实际证据 | 结果及范围 |
| --- | --- | --- |
| 精确 JSON / 恢复 / 数值验证 | `TestJSONArgumentsPreserveNumbersAndTypedBounds`、`TestJSONTextEquivalentRequiresExactNumbersAndCompleteValue`、`TestDynamicMCPArgumentCloneAndTypedInputPreserveNumbers`、`TestMCPRecoveryRequestPreservesExactPagedArguments`、`TestSchemaValidationPreservesExactNumberBoundaries`、`TestSchemaValidationRejectsUnrepresentableNumbers`、`TestPreparedOutputValidationRejectsUnrepresentableNumber` | 冻结前定向 app 1.771s、client 0.241s、contract 0.011s、tool/mcp 0.015s 通过。覆盖超过一个 payload 页的原请求、脱敏拒绝、白名单、相邻大整数、超范围输入/约束和输出失败事实；没有将人工注入的精确值冒充 SDK wire 保真 |
| 完整来源 / envelope | `TestMCPResultContinuationUsesItsOwnCursor`、`TestToolOutputRedactionAndOriginalTruncationRemainExplicit`、`TestDynamicMCPModelProjectionPreservesNonEquivalentTextAndMetadata`；`quality-source-before.log` / `quality-source-after.log` | 修前真实默认源错误及来源失败误报；修后 app 0.325s、mcp 0.017s 通过 |
| 子 Activity 续页 | 增强 `TestMCPResultContinuationUsesItsOwnCursor` 与来源失败续页子用例；`TestResultFailureFactsSurviveFragmentPagingAndReplay` 保留错误分片/重放/原始读取检查 | 源状态丢失修复后 app 0.255s 定向通过；读完全部页并再次独立读回 100 条完整记录和 metadata，业务调用仍为 1，child 无新 OutputSource |
| Context 数量 | `context_bootstrap_precision_test.go` 三组：Remarshal 数量、非法/越界值、full/reference；`context-bootstrap-counts-before.log` / `context-bootstrap-counts-after.log` | 修前 7+12 报 0、7.5 被截成 7、溢出变负、实际单工具目录报 0；修后 app 0.241s 通过 |
| 命令与 Android 输出 | `TestCommandOutputContractRejectsMissingSessionFacts` 与 `TestAndroidDevice*`；`command-contract-before.log`、`command-contract-after.log`、`command-contract-android-after.log` | 修前三工具空输出全部错误通过；命令 owner 定向 app 0.101s，Android owner 收敛后 app 0.362s 通过；旧 packages 日志中的两个 Android 失败不作成功证据 |
| WSL / Android 平台边界 | `TestBuildWSLProcessEnvProtectsRuntimeDataPathForwarding`、`TestAndroidExecRejectsRecoveryOriginBeforeDispatch`、`TestPrepareWSLSkillInvocationUsesPrivateDataDirectory`；`plugin-backend-skill-env-targeted.log` | overlay 已复现错误 WSLENV 修饰和 Android queued session；共享 SkillEnv 后 app 0.241s、command 0.016s 定向通过；Windows 62fc9b6 已执行 plugin/managed/environment_only invocation 三场景，未启动真实 WSL |
| 命令准备失败与启动后失败 | `TestCommandSkillPreparationFailureReportsNoExecution`、`TestCommandFailureAfterStartRetainsExecutedOutcome`；`core-command-preparation-before.log` / `core-command-preparation-after.log` | 修前实际 Runtime 返回及 Activity 均错误报告业务已执行；修后本地 app 1.186s、command 0.123s 定向通过，包含既有取消、完成通知和 unknown settlement 回归。`243a801` 同步设备后原生 Windows app 6.896s 通过，覆盖 native/WSL 准备失败与启动后写 marker、exit 7 的真实终态；未改变命令超时和 Plugin 校验 |
| 隔离候选真实 Context / Plugin WSL | [机器证据](core-quality-candidate-readonly.json)：`516a3ba` 标准 MCP 探针、工具域检查，以及 Ubuntu `exec_command` 的完整 response 独立读回 | Context 三页交付完整、summary 协商正确；direct/dynamic 和错 namespace 指引通过；Plugin Skill 的真实 cwd 与私有数据目录均为可读 POSIX 目录，退出 0。原 20 秒准备失败保留；另一个 120 秒固定单次请求成功。未提供 managed Skill selector，记为 `not_available`，不计通过 |
| Plugin 恢复接线 | `TestPluginRecoverySkillRequiresOriginBeforeCommandStarts` 与既有 Plugin 组件/私有数据目录回归 | app 0.240s 通过；无 origin 时零命令启动、无 marker、lease 正常释放，保留恢复资格与 generation/digest 绑定 |
| 稳定目录缓存 | `TestLazyPluginCatalogSurvivesRuntimeRebuild`、`TestPluginCatalogIdentityRetainsDefinitionAndOverlayBoundaries`、`TestCatalogCachePreservesNumbersAndUnchangedRefreshRevision` | 旧 catalog identity overlay 实际 fresh/preparations=1；修后 cached/0，首业务使用当前 Root，覆盖/升级失效；定向 client 0.225s、plugin 0.068s、tool/plugin 0.044s 通过，另有 62fc9b6 Windows 实际目录删除重建证据 |
| 旧连接 runtime lease | `TestRetiredMCPCallRetainsPluginRuntimeDuringReconcile`；`plugin-retired-lease-before.log` / `plugin-retired-lease-after.log` | 真实阻塞 stdio 的 update/remove/member_disable 修前 3 项 ENOENT，修后 tool/plugin 0.428s 通过；连接关闭前保留原文件，关闭后清理 |
| lease 并发 | 同一实现的 `plugin-retired-lease-race.log`、`plugin-retired-lease-dispatch-race.log` | tool/plugin 1.853s、client 1.271s 与 client dispatch 1.238s 的 race 定向通过；不把局部 race 等同全仓 race |
| Linux Home | `TestEngineLinuxUpgradeKeepsServiceDataDirectories` 的自定义 Home、新装、中断 trial 三场景；既有 install shell 回归 | 修前 3 场景失败，修后 installer 0.393s 及 shell 测试通过；使用临时目录中的实际 Engine 事务，不是用户设备安装证据 |
| Windows 升级父子进程与 PowerShell 兼容 | `TestSetupRuntimeStopWaitsForLauncherChildren`：`tray_after_empty_generation_scan`、`tray_during_launcher_stop`、`core_during_launcher_stop`，并保留既有结构回归 | 同一 Windows 真实父子测试使用旧 `ddf5ff7` 时 3.418s 失败并遗留 Tray；`32e697f` 验证发现 PowerShell 5.1 可选 `[ref]=$null` 绑定问题；修至 `836fb50` 后完整 3 case 加 3 个既有 structural tests **PASS，18.727s**。无观察引用的 Cloudflared 诊断也通过；历史版本升级 CI 的最终结果仍待核对 |
| Browser timeout | `TestLocalExecAllocatorOptionsUseRequestTimeout` | 定向 0.210s 通过；假浏览器 allocator 不代表真实 Chrome 全流程启动 |
| #142 direct 域 | `TestAgentDockContextSeparatesDirectBuiltinsFromNestedMCP`、`TestMCPSchemaReferencesRepeatWithinTrustedConversation`、`TestBuiltinDiscoveryHandoffDoesNotExecuteOrHideCalls`、`TestBuiltinErrorGuidanceRejectsForgedProvenance` | 修前日志 `core126-main142-route-before.log` 留有 reference 丢路由证据；Context 测试已先完成必要续页再断言 reference，不修改运行时来迎合旧预期；direct guidance 本地定向 app 0.603s，见 `core126-direct-guidance-tests.log` |

### 汇合集及功能分支的检查状态

| 提交与环境 | 已确认结果 | 不能由此推断的状态 |
| --- | --- | --- |
| `62fc9b6`，Windows 独立质量工作树 | WSL invocation、Plugin Lazy identity、Android/command 输出、MCP 续页/来源失败定向通过：command 6.233s、client 3.842s、app 7.629s | 不是 Windows 全量、真实 WSL 进程或后来新增 lease 修复的验证 |
| `a7f2bba`，本地 Linux 全仓 Go | app 32.117s、mcp 6.721s、client 0.672s、activity 2.053s、plugin 1.269s、command 3.642s 等其余包通过；只有 installer 失败，整体命令为失败 | 不能写成全仓全部通过，不能代替原生 CI/安装门禁 |
| #126 历史 `ddf5ff7`，Windows | 五个定向测试通过，app 7.323s；该 head 的历史 1.1.9 升级及 aggregate 失败已触发本轮安装器修复 | 此为旧 head 结果；新 head `836fb50` 已推送并重新检查，不能沿用旧失败或此前定向通过作为新 head 的最终结论 |
| #140 历史 `c517f1b`，Windows / Actions | 完整受影响包通过：app 194.908s、tool/plugin 3.279s、plugin 33.529s、tool/skill 1.131s、mcp/client 2.173s；该 head CI 37 success、2 skipped，已完成 | 当前 head 已推进到 `898bc76`；旧 head 的绿色结果不代表当前 head 或质量增量已全部通过 |
| `516a3ba`，Windows 独立质量工作树 | 新 lease、Context 数量、来源、direct、WSL invocation 和精确 JSON 定向全部通过：tool/plugin 5.268s、app 7.729s、mcp/client 2.390s、command 0.225s、tool/core 0.644s；候选编译及 version 校验通过，并已实际启动隔离候选，完成 Context、工具域及 Plugin Ubuntu WSL 只读 smoke 后停止 | 不是 Windows 全量；managed WSL 未覆盖。该实机二进制尚不含 `243a801` 分类修复；未安装到生产，最终 CI 和主线构建仍待补 |
| `243a801`，Windows 独立质量工作树 | Skill 准备失败分类修复已同步设备；原生 Windows app 定向 6.896s 通过，涵盖 native/WSL 准备失败的返回、Activity 和启动后 exit 7 保留 | 新分类通过原生回归；没有将旧 `516a3ba` smoke 记作此提交的实机执行，也不是最终 main 产物或发布验收 |
| 安装器累计修复至 `836fb50`，Windows | 真实父子进程 3 case 与 3 项既有结构回归 PASS，18.727s；无观察引用的 Cloudflared 诊断通过。修复已进入 #126 `836fb50` 和 #140 `898bc76` | 这些定向结果尚不等同原历史 1.1.9 升级 CI 已通过；AccessDenied 的唯一根因仍未由原 artifact 证明 |
| #126 `836fb50` / #140 `898bc76`，当前 Actions | 两个新 head 均已推送；截止约 08:31 UTC，CI 运行中暂无失败报告 | 检查未完成，不填全绿；合并仍须按各自最终 head 的实际结果决定 |
| `7e60922`，Windows 独立质量工作树全仓 Go | 本地与设备源码一致；完整测试正在会话 `session-5432d9402b55160d33ad2ba1` 执行，日志 `core-quality-7e60922-windows-full-go.log` | 截止本快照尚无完成结果，不能据已通过的局部包填写全仓 PASS |
| 最终 main / 独立质量 PR | 待功能 PR 全部合并后确定 | 编号、精确 head、Actions、CodeQL、构建、安装、发布均待补 |

本地全仓日志：`core-quality-tool-domain-full-go.log`。唯一失败为 `TestEngineInstallsLinuxLayoutWithoutStartingServices`，`engine_test.go:64` 在既有测试准备阶段直接 `activateLinux` 时执行 `user.Lookup("agentdock")`，当前 root 沙箱缺少该服务账号，返回 `unknown user agentdock`。失败发生在本轮 Home hydrate 路径之前；没有跳过或放宽该用例，仍需最终 Linux 原生 CI 确认正式门禁。

本地日志根为 `/tmp/agentdock-core-toolchain`。Windows 62fc9b6 定向日志为 `E:\PLAYGROUND\AGENTDOCK\artifacts\source-corrected-20261015\core-quality-62fc9b6-windows-contracts.log`。上述路径只定位既有证据，不表示它们已作为本 PR 的 CI 附件发布。

历史 Windows 升级 artifact 支持“目标 generation Tray 文件访问被拒绝（AccessDenied）”这一事实，但没有导致占用的 PID 证据，因此不能将父子进程停止竞态断言为该历史失败的唯一原因。定向回归证明了真实可重现的停止缺口及修复；原历史升级路径仍需新 head 的 CI 确认。原故障后的最终 rollback 状态实际为 `healthy/rolled_back`，本轮没有改写该状态机或将成功回滚误记为失败。停止 helper 固定精确路径对应的进程句柄，先等待 stable launcher 再读取新的 generation 进程集合；rollback 不传观察引用，保持原来的 Core/Tray 运行快照。共享 15 秒预算限制等待和重试，不能据此宣称 `Get-CimInstance`/WMI 本身有硬截止。

Windows `516a3ba` 的真实旧连接回归分别通过 update 1.12s、remove 0.94s、member_disable 1.55s，三个场景合计 3.62s；它们验证阻塞 stdio 调用仍能读取原 generation。该源码编译得到 `candidate-quality-516a3ba-core.exe`，大小为 `35807232` bytes，SHA256 为 `9bf00605a85610032c43c1026a6c52425b2f976b4b9bbab9a624b07a3fe18c24`，已执行 version 校验并运行以下隔离 smoke，未安装到生产。该文件是质量分支候选，不能作为尚未确定的最终 main 发布产物。

### 隔离候选实机证据与清理

[core-quality-candidate-readonly.json](core-quality-candidate-readonly.json) 保存可执行文件摘要、原调用关联、独立读回结果及进程清理事实。候选于 2026-10-17 07:43:22 UTC 启动，监听 `127.0.0.1:18765`，使用独立 Home；PID `27704` 已于同日 08:08:55 UTC 停止，`has_exited=true`，`production_installation_performed=false`。

| 检查 | 已保存的实际事实 |
| --- | --- |
| 标准 MCP / Context | initialize、initialized、tools/list 和三页 Context 均成功；`context_complete=true`，`compat_text_mode=summary`，`response_truncated=false`；请求关联 ID 正常返回 |
| 工具域 | direct/dynamic 路由与错 namespace 指引检查通过；实际动态目录包含 `easyeda-pcb`，没有执行 PCB 业务工具 |
| 原 20 秒请求 | `call_46034c57834d5c3e12b9c46f609209af` 在 Skill resolver 的 runtime 文件校验阶段截止，`session_id=null`，未到 `TryReserve` 和 `invocation.start`；原失败及错误分类保留在证据中 |
| 新 120 秒固定请求 | `call_4a79247c9bda9e686a09f239c10f6e8b` 对应 `session-9be1450938290379574a4871`；一次提交后返回 `status=exited`、`exit_code=0`、`command_ok=true`，通过完整 response 的独立读回确认，而非重发原业务获取输出 |
| Plugin Ubuntu WSL 环境 | cwd 指向隔离 Home 内当前 generation 的 `skills/easyeda-api`；`PLUGIN_DATA_DIR` 指向 `plugins/.data/easyeda`；两者均为存在且可读取的 POSIX 目录，cwd 内存在 `SKILL.md`。`SKILL_DATA_DIR` 未注入，符合 Plugin Skill 环境边界 |
| 来源与未覆盖项 | 成功调用的 `capture.complete=true`、`source_complete=true`；原始来源引用保留。managed WSL 为 `not_available/no_actual_matching_skill_selector`，未创建或伪造 selector，也不计为通过 |

原错误的 `context deadline exceeded` 表示校验被截止打断，不证明被点名文件发生内容漂移。代码在每次取得 Skill runtime 时仍执行 generation 摘要及运行副本比较；复用副本只可能节省复制，因此没有证据将本次问题归为“首次缓存慢”。第二个请求使用 120 秒的显式预算，以新的 call_id 单独记录，并保留原 call 的失败事实；Core 超时语义和完整校验没有改变，成功结果也不证明原 20 秒预算已足够。内部准备可能创建运行副本或写入 Activity，与请求的业务命令是否启动分开记录。

## 合并后的独立只读复核

对 `a7f2bba` 复核，并确认 `516a3ba` 的 3 文件 guidance 增量没有改动以下路径：

- `contextCatalogSummary` 同时生成域字段、数量和目录状态；`prepareContextBootstrap` 与 `refreshContextBootstrapReference` 共用该 owner。首页复制完整 summary，详情续页沿固定快照，引用响应先重新核对来源。Context full/summary 文本协商没有另一套数量转换。
- `mcpResultSourceCapture` 按终态原调用及 owner/conversation/task/thread/workspace 校验，再匹配精确来源引用或其历史版本；宿主页不能成为新的默认源。来源完整性与页面交付完整性独立。
- `mcp_result_raw` 使用独立的原始字节、作用域校验和诊断读取进度；规范化分页或 cursor 重放不会改写 raw_result_ref 指向的来源。
- 原有精度/非等价文本回归与错误分片/重放回归同时保留，未在合并冲突中互相覆盖。

该复核没有发现上述链路的新增实现缺口。现有回归分别验证非零计数与 full/reference、域字段及 SDK full/summary；尚无单个 fixture 联合逐值比对全部三者，不能声称该组合已单独实测。本段是代码及回归覆盖审查，不新增一次测试运行记录。

## 明确限制

1. **官方 MCP SDK 的上游浮点解码仍是精度边界。** 当前 go-sdk v1.7.0 的动态 `StructuredContent`、顶层 `_meta` 和原生 content 的动态 metadata，可能在进入 Core 前已转换为 `float64`。已观察 `_meta.revision=18446744073709551615` 进入首屏时变成 `18446744073709552000`。本次修复保持 Core 自有解析和已接收精确值的后续精度，无法还原已舍入原值；缓存测试明确从 Core 精确元数据开始，不能当作上游 wire 保真证据。未创建 SDK fork、旁路 transport/session 或第二套 raw 缓存。
2. `516a3ba` 隔离候选已实际验证 Plugin Ubuntu WSL 的 cwd、私有数据目录和只读命令结果；managed WSL 因缺少实际 selector 未覆盖。`243a801` 的分类修复通过原生 Windows native/WSL 回归，但不属于该旧候选二进制的实机结果。Android broker、stdio 和假浏览器夹具仍只验证对应 owner 和协议边界，不能扩大为真实 Android、Chrome 或所有远端 MCP 服务验证。
3. 当前 Linux 沙箱缺服务账号的全仓失败仍保留。环境归因不替代最终原生 CI 结果，不据此豁免安装门禁。
4. 仓库既有 EasyEDA 实机记录对应旧候选，仅说明原功能曾执行读取、最小编辑、独立读回和清理；不能代替最终质量 head 的验收。本材料未重新操作 PCB 或生产 Core。
5. 功能 PR 检查、独立质量 PR 检查、候选构建、安装测试和发布是不同状态。完整候选须绑定最终精确 main SHA；状态缺失时保持待填。
6. 本快照的 #126/#140 新 head CI 及 `7e60922` Windows 全仓 Go 仍在运行。历史提交通过、当前暂无失败和最终全部通过分别记录；根流程须用完成后的原始结果更新，不能推定未来门禁已成功。

## 可复核交付入口与后续替换

以下条目提供当前可复核入口；正式交付时由根流程用实际结果替换“待确认”，并记录新的核对时间。

| 交付字段 | 当前可复核入口 | 最终替换条件 |
| --- | --- | --- |
| 本轮功能 PR 与合并提交 | #126 `836fb50846cfa7143bb219fd82318c71877d2aee`；#140 `898bc766c490d448306fbbe623a8ee5661e903c6`；两者当前 CI 运行中 | 核对各最终 head 的 Checks 和实际 merge commit，再填写全部功能合并结果 |
| 独立质量 PR 的 base main / head / 编号 | 本地及设备预提交质量 head `7e60922f8ea73a553de688dea261ff3a41cf55ad`；PR 尚未创建 | 全部功能合并后，从准确 main 创建独立质量 PR，并填写真实 base/head/编号 |
| 最终差异与原修复目标 | 本文问题—修复—行为表及上述精确源码 | 按最终 `base main..quality head` 逐项复核；已进入 main 的实现不重复提交 |
| 当前 Windows 全仓与最终质量门禁 | 会话 `session-5432d9402b55160d33ad2ba1`；`core-quality-7e60922-windows-full-go.log`；历史 Linux 失败范围见本文 | 先记录该命令真实终态，再分别链接最终质量 Actions、CodeQL、Windows/Linux 原生和安装检查的对应提交与结果 |
| 候选源码、构建与实机验证 | [516a3ba 机器证据](core-quality-candidate-readonly.json)；`243a801` 原生分类回归；`836fb50` 安装器定向回归 | 按最终 main 填写候选构建 run、可执行文件摘要、必要实机读取及安装门禁；历史候选不改标为最终产物 |
| 发布与资产核对 | 当前未进行本轮最终 main 发布 | 实际完成后填写标签指向的提交、发布记录、资产及摘要；测试或候选编译成功不表示已发布 |
