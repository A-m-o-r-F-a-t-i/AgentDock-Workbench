# Core 集成质量审查与验证（1.1.9，PR #143 修复验证快照）

> **状态截止：2026-10-17 12:46 UTC。** 本轮 Core 功能 PR 全部合并后，已单独建立 [质量 PR #143](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/pull/143)。Context、Common watcher、scope 容量及别名修复已提交并推送为 `101aca389ce2fbf88f11770fdc3a0bbed8bbb641`。该 head 的 35 项 CI 已结束：32 success、2 skipped、1 failure；失败为 macOS 四种 Common symlink 改指场景。额外原生 Windows junction 对照证实新实现存在同 mtime 修改漏监听：主线四个 owner 文件的 overlay 通过，`101aca3` 失败。两项后续修复的 12 个源码文件已经独立复审、冻结并按原文件摘要写回设备；最后 Linux 定向 race 与原生 Windows junction/Context 回归均通过。本文记录该增量提交前验证，新 head 的全仓/CI 及合并仍待实际完成，PR 继续 Draft。本材料保留每次真实失败和七项 Windows symlink 权限跳过；最终 quality head、CI、合并与发布以 PR 正文及最终报告的后续实际记录为准。

> **后续记录（2026-10-17 14:21 UTC 核对）：** 本文保留截至 12:46 UTC 的修复验证快照。#143 最终检查与合并、#144 后续平台门禁修复，以及原品牌 1.1.9 正式发行的待办，见 [Core 合并后的发行验证与原品牌恢复](final-release-integration.md)。该后续记录不改变本文各历史源码的失败、跳过与验证范围。

## 目的与基线

本记录依据《AgentDock-Workbench-GPT-Codex-Agent执行架构改进计划-来源校正版-20261015.md》及[实施说明](../../gpt-codex-execution-architecture.md)，检查多个 Core PR 合并后，原有功能目标是否仍由实际执行链路实现。修复沿用现有输入、输出、Activity、命令、安装器和插件责任边界；本记录不把编译成功等同于运行正确，也不把某个功能分支的检查等同于独立质量 PR 的检查。

| 项目 | 本文记录的实际状态 |
| --- | --- |
| 功能合并 main / 质量代码汇合 | main 为 `34270eca8286867ec47930ac1023acd1ecf9e4f8`；创建 PR 前的质量汇合提交为 `0b68ee4d05edbf9323eec74a7778b04021a4be8f`，tree 与原 `14a9997` 完全相同；随后文档提交 `1fec364` 成为 PR 创建 head |
| 验证源码 | 历史 Windows 全仓诊断与环境复验绑定 `7e60922f8ea73a553de688dea261ff3a41cf55ad`，包含 `243a801` 分类修复和 `836fb50` 安装器修复；历史实机 smoke 二进制对应 `516a3ba`。Context/watcher 定向验证为 `1fec364` 加按 manifest 哈希写回的 12 个冻结文件，该源码随后提交为 `101aca3`；后续 junction/alias 修复另记实际验证 |
| 功能 PR 合并结果 | #126 head `836fb50846cfa7143bb219fd82318c71877d2aee` 于 09:01:37 UTC 合并为 `043d7316cf77b1fa09c9bf2025cbc1b88bf1f752`；#140 head `898bc766c490d448306fbbe623a8ee5661e903c6` 合并为 `34270eca`；#142 已通过 `2fe6b93` 并入本轮主线来源 |
| 功能 PR 检查 | #126 与 #140 各自最终为 37 success、2 skipped，均无未解决 review threads；#140 单次原门槛复验及原失败详见下文 |
| 历史 Linux 全仓 Go | `a7f2bba` 已执行；只有 installer 的服务账号夹具失败，整体命令仍为失败，详见验证状态 |
| PR 创建前 Windows 诊断与环境复验 | `7e60922` 原 20 分钟外层超时保留；同源 JSON 全仓正常结束但 exit 1：63 包 PASS、8 包无测试 skip、3 包失败。仅对三个实际失败包设置本次进程的 `GOFLAGS=-buildvcs=false` 复验后全部通过；不记为单次全仓 PASS，也不替代 #143 CI |
| 独立质量 PR | [#143](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/pull/143) 已在 #126/#140 全部合并之后建立；base `34270eca8286867ec47930ac1023acd1ecf9e4f8`，创建 head `1fec3643fab618ee55bbbe1e168c54b46deda5e9` |
| #143 创建 head 的首轮 CI 终态 | 27 success、2 skipped、5 failure（同一旧 Context 断言）、1 cancelled。Windows 全仓 Go 作业因超过 35 分钟预算取消；API 与 gh 均未取得日志，具体测试与原因不能归因于 watcher。后续工作树定向通过不改写此 CI 终态 |
| 顶层 Context 重新验证修复 | 已随 `101aca3` 提交的三文件补丁；真实完整 source 交付后，规则/Common 同 mtime 变更及重新验证周期推进的四项回归修前全部失败，修后与既有 cursor/revision 边界定向通过，详见下文 |
| 最终质量 head / 合并 main | **待修复提交、对应 CI 和质量合并实际完成后，由 PR 正文及最终报告填写** |
| 最终 CI、构建、安装与发布 | **待按最终提交分别填写**；生产 Core 未替换，隔离候选已停止，发布前备份已完成 |

本文同时记录已回送功能分支的交互修复和仍在独立质量分支中的修改。创建 PR 前 `34270eca..0b68ee4` 的差异为 66 个文件、2614 行新增、197 行删除，与原审查树相同；随后文档变更及 #143 后续修复分别记录。正式差异以 PR 实际 `base main..quality head` 为准，不重复提交功能分支已经吸收的实现。例如 direct guidance 和 Windows 升级停止修复已进入 main，其目标继续在质量汇合集保留。

## 功能目标与审查范围

| 来源 | 必须保留的目标 | 本次核对的交互边界 |
| --- | --- | --- |
| #125、#126 执行架构 | Canonical Result 校验和留存先于模型投影；原调用恢复受作用域与一次性消费约束 | 动态 JSON 参数、原请求读回、Schema 数值、命令结果与 Activity 来源 |
| #127/#128 的参数和输出目标，以及 #130/#137 的后续主线实现 | 完整参数可审查，保留非等价文本、原生内容、领域 cursor；宿主续页归入原调用 | 去重只移除可证明等价的副本；首页、续页、诊断读取不替换完整来源 |
| #134 媒体预算、#138 文件对象 | 对最终 SDK envelope 计量；保留有效原生 content 与正式文件输入 | 原生媒体计量与完整来源回退仍使用已有实现 |
| #135 审阅绑定、#139 Skill 事务、#140 Plugin 生命周期 | 执行固定审阅内容、generation、digest 与运行副本；释放归属明确 | 恢复资格、WSL 环境、稳定目录缓存身份、旧连接在途调用的 runtime lease |
| #141 Linux/browser | 服务、Core、Skill 使用同一运行目录；请求超时覆盖浏览器启动 | 已提交 Home 的恢复顺序及浏览器调试连接等待 |
| #142 工具域 | 设备内置工具直接调用，动态 MCP 通过 qualified selector；错误指引不重新混淆域 | Context、目录/Schema 引用和 `next_required` 保留相同域与调用方式 |

#133 的公共名称兼容与旧安装识别夹具作为既有基线保留。本次没有用重复实现替换已被主线覆盖的旧 PR；公开发行说明仍描述相对 v1.1.8 的完整用户变化。当前仍开放的 #131 属于 1.2.0 图标工作，#129 是旧品牌的零差异重复项，均在本批 Core 合并范围之外。

## 已复现问题、修复责任与保留行为

| 问题 | 实际复现或代码失配 | 现有责任边界中的修复 | 必须保留的结果 |
| --- | --- | --- | --- |
| 精确 JSON 与恢复参数 | 动态解码/参数克隆/缓存读回先转 `float64`；相邻大整数可能被当作同一 JSON 文本，目录 digest 可能在重启后变化 | 公共 `tool/core.DecodeJSON`、`Remarshal` 与保守的 `JSONTextEquivalent`；Core 输入、克隆、恢复读取、目录缓存和文本去重复用它们 | Core 自有边界保留 `json.Number`；类型化字段仍执行原范围检查；非等价文本及 native metadata 不被去除 |
| 超大指数导致验证器 panic | `1e1000001` 可以通过 JSON 解码，但底层数值比较遇到不可表示的 `big.Rat` 后 panic | `tool/contract/validator.go` 在现有 Schema 编译和值归一化边界同时检查数值可表示性 | 明确拒绝不可表示的值和 Schema 约束；保留正常大整数、精确小数及可表示的 `1e309`，不用 recover 吞 panic 或退回浮点数 |
| 完整来源被宿主页覆盖 | 默认 Activity `OutputSource` 变成首个约 5.7 KB 的宿主短页；来源保存失败时却出现 `source_complete=true` | `app/specs_mcp.go` 先留存原响应；`prepareModelOutput` 和 `RetainResponseSource` 复用原 MCP owner 的完整源，禁止把 host page 再存成默认源 | 完整错误 envelope、`_meta`、非等价文本和原生内容可独立读回；保存失败不改变实际业务状态，也不重跑业务 |
| 子 Activity 续页丢失来源状态 | 续页折叠为 child 后触发通用提前返回，`capture` 与 `source_complete` 消失 | 只让真实 `mcp_result_continue` host page 进入对应投影；`mcpResultOrigin` 校验原调用作用域，按精确 `raw_result_ref` 读取原来源或保留版本 | 所有成功页引用相同来源；来源失败继续为 false；末页完成只表示交付完成；child 不创建新的 OutputSource |
| Context 工具数量错误 | `Remarshal` 改为 `UseNumber` 后，旧的 `tool_count.(float64)` 把实际非零目录报为 0；旧转换还会截断小数或使越界值变负 | `contextCatalogSummary` 使用现有严格整数 helper；限定数值类型并检查负数、单项范围和合计溢出；reference 直接保留 `Snapshot.ToolCount` | full/reference 使用同一数量和目录状态规则；非法值令数量未知，不伪造已知 0；#142 域字段在同一 summary owner 中生成 |
| Common Skill 缺失根的监听范围过大（#143 后续修复） | Windows 补全续页后仍出现 `Context immutable snapshot changed`；四次 identity 仅 `files[2]` 从 0→2→4→8 变化，八条 nil-filter 事件均为 `WRITE testRoot/home` | Common owner 一次解析选择根与稳定 UserHome 父根，复用既有 tree watcher，以登记范围过滤无关事件；最终 Linux race 和 Windows 定向通过，Windows symlink 子例的权限跳过不计覆盖 | 普通 Core 记账不使固定 Context 来源失效；真正公共 Skill 变更须使当前 Context 重新构建，规则与安全绑定变化仍由原 guard 检查续读资格。Windows 不回退为 nested handles；外部目标使用直接稳定父根，不承诺稳定根以上任意祖先的物理 rename |
| 新增 scope 容量失稳（普通子目录修后回归通过） | 已选 Common 根覆盖普通子路径，但 `NewScopedFiles.Add` 仍将每条路径计入 scope LRU；2057 项相同登记重复三遍，无文件变更却使 source 为 `9-unhealthy → 2066-unhealthy → 4123-unhealthy`，实际仅 3 个目录监听句柄 | 普通 lexical/resolved 均被已选范围覆盖时复用并更新覆盖项使用顺序，仍执行原平台订阅；正式 `covered_descendants` 修后回归通过，目录 scope 为 1，重复扫描 revision 稳定 | 冗余范围登记不制造 source 变化；外部别名与真实不同范围仍受登记、容量及失效约束。普通模式超过 2048 个真实监听句柄的原有边界及独立外部目标容量策略未改；不能据周期重新验证保证任意规模的构建成功 |
| 已覆盖别名目标变更未更新 canonical 映射 | 别名从 A 改指已受其他 scope 覆盖的 B 后，覆盖判断提前返回，原 canonical 映射未更新；修前回归 FAIL，snapshot 0.008s | 修复先更新已有 canonical 映射，仅在观察到目标变化时推进 revision；最终 Linux race 已包含修后回归并通过，两个包的 Windows 交叉编译通过；Windows symlink 权限跳过不计该场景覆盖 | 新目标继续按实际映射处理；旧的独立 canonical scope 按既有有界历史选择保留，可能保守地多失效，不宣称所有旧目标立即退订；原外部目标容量边界保持 |
| 顶层 Context 绕过下层重新验证（#143 后续本地修复） | 无 watcher 事件、保持大小与 mtime 的真实规则/Common 内容变更后，下层重新验证周期已推进，顶层完整缓存仍返回旧内容，已交付路径仍返回旧 reference | `context_delivery.go` 将现有 tracker 的 cache revision 与稳定 source identity 分开；完整缓存及当前引用使用 cache eligibility，`required_output.go` 保存同一资格供 retained source 去重 | 周期推进触发重新构建；实际内容变化才改变 `ContextRevision`。纯时钟变化不改变稳定 source identity 或续页 guard；旧 source 失去当前引用资格后仍按原授权、规则 guard、TTL 留存，不因缓存过期直接删除 |
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
| scope 容量与重复登记 | 临时 Go overlay `TestScopedFilesRepeatedCoveredSelectionsKeepSourceStable`；`scope-capacity-overlay_test.go`、`scope-capacity-overlay.json`、`scope-capacity-before.log`；作者随后采用正式 `covered_descendants` 回归 | 修前：真实 Home/.agents/skills 根，2056 个缺失子路径；三遍相同登记的 source 为 `9-unhealthy → 2066-unhealthy → 4123-unhealthy`，scopes 恒 2048、真实 directory watches 恒 3，元数据未变且根仍为空，**FAIL，snapshot 0.235s**。作者正式容量回归的独立修前运行也 FAIL，snapshot 0.082s；修后 **PASS，snapshot 3.303s**，目录 scope 为 1，重复扫描 revision 稳定；最终 Linux race 和 Windows 定向另行记录 |
| 已覆盖别名从 A 改指 B | `common-covered-alias-before.log`；目标 B 已由既有 scope 覆盖的真实别名变更回归 | 修前 **FAIL，snapshot 0.008s**；修后纳入 `common-scoped-tree-final-race.log`，最终 Linux race 通过。Windows amd64 交叉编译退出 0；Windows 原生的 symlink 权限跳过不能记为此场景实际通过 |
| 顶层 Context 重新验证与不可变来源 | `TestContextRevalidationRefreshesDeliveredContext`，覆盖 rules/Common × 完整缓存/已交付 reference；`context-top-revalidation-before.log` / `context-top-revalidation-after.log` / `context-top-revalidation-boundaries.log` | 通过真实 Runtime 读取 source 至 EOF，再同大小、同 mtime 修改文件并仅推进既有可控周期：修前四项全部 FAIL，app 0.537s；修后全部 `TestContextRevalidation` PASS，app 0.981s。加入既有 cursor、目录/.git/规则边界、`ContextRevision` 与 source identity 回归后 PASS，app 1.516s；纯时钟推进的内容 revision 不变。仅为当前未提交补丁的本地 Linux 定向结果 |
| 命令与 Android 输出 | `TestCommandOutputContractRejectsMissingSessionFacts` 与 `TestAndroidDevice*`；`command-contract-before.log`、`command-contract-after.log`、`command-contract-android-after.log` | 修前三工具空输出全部错误通过；命令 owner 定向 app 0.101s，Android owner 收敛后 app 0.362s 通过；旧 packages 日志中的两个 Android 失败不作成功证据 |
| WSL / Android 平台边界 | `TestBuildWSLProcessEnvProtectsRuntimeDataPathForwarding`、`TestAndroidExecRejectsRecoveryOriginBeforeDispatch`、`TestPrepareWSLSkillInvocationUsesPrivateDataDirectory`；`plugin-backend-skill-env-targeted.log` | overlay 已复现错误 WSLENV 修饰和 Android queued session；共享 SkillEnv 后 app 0.241s、command 0.016s 定向通过；Windows 62fc9b6 已执行 plugin/managed/environment_only invocation 三场景，未启动真实 WSL |
| 命令准备失败与启动后失败 | `TestCommandSkillPreparationFailureReportsNoExecution`、`TestCommandFailureAfterStartRetainsExecutedOutcome`；`core-command-preparation-before.log` / `core-command-preparation-after.log` | 修前实际 Runtime 返回及 Activity 均错误报告业务已执行；修后本地 app 1.186s、command 0.123s 定向通过，包含既有取消、完成通知和 unknown settlement 回归。`243a801` 同步设备后原生 Windows app 6.896s 通过，覆盖 native/WSL 准备失败与启动后写 marker、exit 7 的真实终态；未改变命令超时和 Plugin 校验 |
| 隔离候选真实 Context / Plugin WSL | [机器证据](core-quality-candidate-readonly.json)：`516a3ba` 标准 MCP 探针、工具域检查，以及 Ubuntu `exec_command` 的完整 response 独立读回 | Context 三页交付完整、summary 协商正确；direct/dynamic 和错 namespace 指引通过；Plugin Skill 的真实 cwd 与私有数据目录均为可读 POSIX 目录，退出 0。原 20 秒准备失败保留；另一个 120 秒固定单次请求成功。未提供 managed Skill selector，记为 `not_available`，不计通过 |
| Plugin 恢复接线 | `TestPluginRecoverySkillRequiresOriginBeforeCommandStarts` 与既有 Plugin 组件/私有数据目录回归 | app 0.240s 通过；无 origin 时零命令启动、无 marker、lease 正常释放，保留恢复资格与 generation/digest 绑定 |
| 稳定目录缓存 | `TestLazyPluginCatalogSurvivesRuntimeRebuild`、`TestPluginCatalogIdentityRetainsDefinitionAndOverlayBoundaries`、`TestCatalogCachePreservesNumbersAndUnchangedRefreshRevision` | 旧 catalog identity overlay 实际 fresh/preparations=1；修后 cached/0，首业务使用当前 Root，覆盖/升级失效；定向 client 0.225s、plugin 0.068s、tool/plugin 0.044s 通过，另有 62fc9b6 Windows 实际目录删除重建证据 |
| 旧连接 runtime lease | `TestRetiredMCPCallRetainsPluginRuntimeDuringReconcile`；`plugin-retired-lease-before.log` / `plugin-retired-lease-after.log` | 真实阻塞 stdio 的 update/remove/member_disable 修前 3 项 ENOENT，修后 tool/plugin 0.428s 通过；连接关闭前保留原文件，关闭后清理 |
| lease 并发 | 同一实现的 `plugin-retired-lease-race.log`、`plugin-retired-lease-dispatch-race.log` | tool/plugin 1.853s、client 1.271s 与 client dispatch 1.238s 的 race 定向通过；不把局部 race 等同全仓 race |
| Linux Home | `TestEngineLinuxUpgradeKeepsServiceDataDirectories` 的自定义 Home、新装、中断 trial 三场景；既有 install shell 回归 | 修前 3 场景失败，修后 installer 0.393s 及 shell 测试通过；使用临时目录中的实际 Engine 事务，不是用户设备安装证据 |
| Windows 升级父子进程与 PowerShell 兼容 | `TestSetupRuntimeStopWaitsForLauncherChildren`：`tray_after_empty_generation_scan`、`tray_during_launcher_stop`、`core_during_launcher_stop`，并保留既有结构回归 | 同一 Windows 真实父子测试使用旧 `ddf5ff7` 时 3.418s 失败并遗留 Tray；`32e697f` 验证发现 PowerShell 5.1 可选 `[ref]=$null` 绑定问题；修至 `836fb50` 后完整 3 case 加 3 个既有 structural tests **PASS，18.727s**。无观察引用的 Cloudflared 诊断也通过；随后 #126 `836fb50` 的历史版本升级检查随全部 Actions 通过 |
| Browser timeout | `TestLocalExecAllocatorOptionsUseRequestTimeout` | 定向 0.210s 通过；假浏览器 allocator 不代表真实 Chrome 全流程启动 |
| #142 direct 域 | `TestAgentDockContextSeparatesDirectBuiltinsFromNestedMCP`、`TestMCPSchemaReferencesRepeatWithinTrustedConversation`、`TestBuiltinDiscoveryHandoffDoesNotExecuteOrHideCalls`、`TestBuiltinErrorGuidanceRejectsForgedProvenance` | 修前日志 `core126-main142-route-before.log` 留有 reference 丢路由证据；Context 测试已先完成必要续页再断言 reference，不修改运行时来迎合旧预期；direct guidance 本地定向 app 0.603s，见 `core126-direct-guidance-tests.log` |

### 汇合集及功能分支的检查状态

| 提交与环境 | 已确认结果 | 不能由此推断的状态 |
| --- | --- | --- |
| `62fc9b6`，Windows 独立质量工作树 | WSL invocation、Plugin Lazy identity、Android/command 输出、MCP 续页/来源失败定向通过：command 6.233s、client 3.842s、app 7.629s | 不是 Windows 全量、真实 WSL 进程或后来新增 lease 修复的验证 |
| `a7f2bba`，本地 Linux 全仓 Go | app 32.117s、mcp 6.721s、client 0.672s、activity 2.053s、plugin 1.269s、command 3.642s 等其余包通过；只有 installer 失败，整体命令为失败 | 不能写成全仓全部通过，不能代替原生 CI/安装门禁 |
| #126 历史 `ddf5ff7`，Windows | 五个定向测试通过，app 7.323s；该 head 的历史 1.1.9 升级及 aggregate 失败已触发本轮安装器修复 | 旧 head 的失败原样保留；`836fb50` 的最终检查与实际合并另行记录，不用定向结果替代门禁 |
| #140 历史 `c517f1b`，Windows / Actions | 完整受影响包通过：app 194.908s、tool/plugin 3.279s、plugin 33.529s、tool/skill 1.131s、mcp/client 2.173s；该 head CI 37 success、2 skipped，已完成 | `898bc76` 的检查与合并另行记录；该旧 head 的绿色结果不代表独立质量增量已全部通过 |
| `516a3ba`，Windows 独立质量工作树 | 新 lease、Context 数量、来源、direct、WSL invocation 和精确 JSON 定向全部通过：tool/plugin 5.268s、app 7.729s、mcp/client 2.390s、command 0.225s、tool/core 0.644s；候选编译及 version 校验通过，并已实际启动隔离候选，完成 Context、工具域及 Plugin Ubuntu WSL 只读 smoke 后停止 | 不是 Windows 全量；managed WSL 未覆盖。该实机二进制尚不含 `243a801` 分类修复；未安装到生产，最终 CI 和主线构建仍待补 |
| `243a801`，Windows 独立质量工作树 | Skill 准备失败分类修复已同步设备；原生 Windows app 定向 6.896s 通过，涵盖 native/WSL 准备失败的返回、Activity 和启动后 exit 7 保留 | 新分类通过原生回归；没有将旧 `516a3ba` smoke 记作此提交的实机执行，也不是最终 main 产物或发布验收 |
| 安装器累计修复至 `836fb50`，Windows | 真实父子进程 3 case 与 3 项既有结构回归 PASS，18.727s；无观察引用的 Cloudflared 诊断通过。随后 #126 历史版本升级检查及两个功能 head 的全部 Actions 通过，修复已并入 main | 定向回归与原升级门禁分别记录；AccessDenied 的唯一根因仍未由原 artifact 证明 |
| #126 `836fb50`，最终 Actions | 37 success、2 skipped；Ready、clean 状态和无未解决 review threads 已核对，09:01:37 UTC 合并为 `043d7316` | 功能检查通过不替代独立质量增量及最终 main 发布门禁 |
| #140 `898bc76`，最终 Actions | 原冷查询失败作业按相同门槛仅复验一次后通过；最终 37 success、2 skipped，无未解决 review threads，合并为 `34270eca` | 原失败及复验独立保留，不把旧 head 或一次复验表述为所有质量增量已经验证 |
| `7e60922`，Windows 原全仓请求 | 08:47:46 UTC 外层 20 分钟工具超时，exit 1，仅确认 8 包 PASS；原日志与会话保留 | 超时原因仍未知，不能从后续 VCS 失败反推原超时原因，也不能记为全仓通过 |
| `7e60922`，Windows JSON 全仓诊断 | 09:02:49–09:06:40 UTC 正常跑完，exit 1：63 包 PASS、8 包无测试 skip、3 包失败；app PASS 220.748s，真实搜索/目录分页回归 PASS（80/120，14.57s） | 四个失败用例均为嵌套 `go build` 的 VCS 状态 128；这是完整诊断结果，仍为失败命令 |
| `7e60922`，三个失败包的环境复验 | 09:09:39–09:10:27 UTC，仅为本次进程设置 `GOFLAGS=-buildvcs=false`，exit 0：installer 20.302s、desktopruntime 23.390s、scripts 24.748s 全部 PASS，四个原失败用例均实际通过 | 源码、父级 Git、全局缓存、CI 和断言未改；应表述为“全仓诊断加三个失败包的环境复验通过”，不是单次全仓 PASS |
| #143 创建 head `1fec364`，首轮 CI 终态 | base `34270eca`；27 success、2 skipped、5 failure（同一旧 Context 断言）、1 cancelled。Windows full Go run `39020827940` / job `117362145082` 超过 35 分钟作业预算取消 | API 与 gh 均未取得该 Windows 作业日志，不知道停在哪个测试或调用栈，不能归因于 watcher。后续修复的本地结果不改写此 head 的 CI 终态 |
| #143 Context / watcher 冻结修复的提交前验证 | 12 文件按 manifest 哈希写回 Windows 独立工作树，`git diff --check` 通过；Linux 定向 race：app 6.033s、snapshot 41.335s；两个包 Windows amd64 交叉编译退出 0。原生 Windows 定向退出 0：app 21.8s、snapshot 128.348s，七项 symlink 权限跳过单独列出 | 源码随后进入 `101aca3`；这些局部结果不等同原生全仓或 PR head CI，跳过项不计覆盖 |
| #143 `101aca3`，第二轮 CI | 32 success、2 skipped、1 failure；Ubuntu/Windows 全量 Go、CodeQL、安装器和 Linux/ARM/Windows snapshot 等通过，macOS 四种 symlink 改指失败 | 下文记录真实失败与额外 junction 对照；整体 CI 仍未通过，未合并或发布 |

本地全仓日志：`core-quality-tool-domain-full-go.log`。唯一失败为 `TestEngineInstallsLinuxLayoutWithoutStartingServices`，`engine_test.go:64` 在既有测试准备阶段直接 `activateLinux` 时执行 `user.Lookup("agentdock")`，当前 root 沙箱缺少该服务账号，返回 `unknown user agentdock`。失败发生在本轮 Home hydrate 路径之前；没有跳过或放宽该用例，仍需最终 Linux 原生 CI 确认正式门禁。

#140 的失败记录为 run `39017442152` attempt 1、job `117352358095`：`macos-15-intel` 冷查询 `2.103916905s > 2s`。Activity、六个实际依赖、`go.mod`、`go.sum` 和 workflow 在 `c517`、`836`、`898` 对象中完全相同。仅对该作业做一次原门槛复验：attempt 2、job `117358829248`，09:03:41–09:05:09 UTC 通过，Activity 包 PASS，三次 `cold_ms` 为 1237.453、1203.533、1295.379。原失败日志保留；相同源码与复验结果不证明某个唯一环境根因，也没有放宽 2 秒门槛。

Windows 原超时请求为 `session-5432d9402b55160d33ad2ba1`，日志 `core-quality-7e60922-windows-full-go.log`；后续确认原 PID 及其父进程均已不存在。JSON 诊断请求为 `session-a4e915842ff4139d4e2a66c0`，PID `34372`，使用 `-count=1 -json -timeout=8m -p 2`；三个失败包的环境复验请求为 `session-b147357713e11da9d01ba424`。日志前缀分别为 `core-quality-7e60922-windows-count1-json` 与 `core-quality-7e60922-windows-vcs-env`，各保留 `.jsonl`、`.stderr.log` 和 `.result.json`。四个原失败用例涉及 desktopruntime 的两个 Tailscale 场景、installer 的 generation Core 和 scripts 的 Home 迁移 CLI，均在嵌套 `go build` 读取 VCS 状态时返回 128；环境复验逐项确认通过，没有失败用例。最终质量 CI 将在正常 Git checkout 上运行既有全仓流程，不携带本次手工 `GOFLAGS`。

本地日志根为 `/tmp/agentdock-core-toolchain`。Windows 62fc9b6 定向日志为 `E:\PLAYGROUND\AGENTDOCK\artifacts\source-corrected-20261015\core-quality-62fc9b6-windows-contracts.log`。上述路径只定位既有证据，不表示它们已作为本 PR 的 CI 附件发布。

历史 Windows 升级 artifact 支持“目标 generation Tray 文件访问被拒绝（AccessDenied）”这一事实，但没有导致占用的 PID 证据，因此不能将父子进程停止竞态断言为该历史失败的唯一原因。定向回归证明了真实可重现的停止缺口及修复，随后 `836fb50` 的历史升级门禁通过；门禁通过仍不提供原占用 PID 或唯一根因证据。原故障后的最终 rollback 状态实际为 `healthy/rolled_back`，本轮没有改写该状态机或将成功回滚误记为失败。停止 helper 固定精确路径对应的进程句柄，先等待 stable launcher 再读取新的 generation 进程集合；rollback 不传观察引用，保持原来的 Core/Tray 运行快照。共享 15 秒预算限制等待和重试，不能据此宣称 `Get-CimInstance`/WMI 本身有硬截止。

Windows `516a3ba` 的真实旧连接回归分别通过 update 1.12s、remove 0.94s、member_disable 1.55s，三个场景合计 3.62s；它们验证阻塞 stdio 调用仍能读取原 generation。该源码编译得到 `candidate-quality-516a3ba-core.exe`，大小为 `35807232` bytes，SHA256 为 `9bf00605a85610032c43c1026a6c52425b2f976b4b9bbab9a624b07a3fe18c24`，已执行 version 校验并运行以下隔离 smoke，未安装到生产。该文件是质量分支历史候选，不能作为尚未完成质量 PR 合并的最终 main 发布产物。

### PR #143 首轮 Context 失败与后续诊断

[PR #143](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/pull/143) 在本批功能 PR 完成合并后独立建立，创建 head 为 `1fec3643fab618ee55bbbe1e168c54b46deda5e9`。首轮已读到五项 Context 相同断言失败；这批失败的处理包含两个不同层次，不能归并成“只是旧测试问题”。

该 head 的首轮 CI 已结束，35 项检查为 27 success、2 skipped、5 failure、1 cancelled。Windows full Go run `39020827940` attempt 1 / job `117362145082` 于 09:21:55–10:01:55 UTC 运行后取消；唯一 annotation 原文为 `The job has exceeded the maximum execution time of 35m0s`。API 与 gh 均未取得作业日志，无法定位具体测试或调用栈；目前只确认作业超过规定预算，不能据此归因于 Common watcher 或下文的缓存缺口。

旧计数测试只消费字符串 `next_cursor`，漏掉 `structured_output.continue_read` 的合法来源续读；没有 cursor 并不证明整份 Context 已交付。补齐该消费路径后，继续要求同一不可变来源完成交付并进入 reference，未放宽运行时绑定或来源变化校验。

| 验证/诊断 | 已确认事实 | 当前边界 |
| --- | --- | --- |
| 补全续页后的 Linux 常规定向 | 已取得 exit 0 | 最终耗时未读回，不填写推测耗时；不代替 Windows 或完整 CI |
| alias 路径复验 | 首次触发 Home symlink 祖先的安全拒绝，原结果保留 | 路径规范化后的请求未被 exec-server 创建，记为未执行；不计通过，也不放宽安全边界 |
| Windows 新测试首次复验 | 测试文件摘要 `24864981…53e4e`；第 117 行报 `Context immutable snapshot changed`，计数用例 0.51s、app 6.160s，FAIL | 已补齐续页仍失败，表明还存在真实来源失效问题 |
| 一次 Go overlay 诊断 | 同一断言继续 FAIL，计数用例 0.48s、app 6.136s；四次 identity 仅 `files[2]` 为 0→2→4→8，八条 nil-filter 事件均为 `WRITE testRoot/home` | overlay 只增加诊断，不修改生产文件；定位缺失 Common 根的 watcher 回退到 UserHome，将 Core 自身写入误判为公共 Skill 变更 |
| owner 范围及稳定 tree 修复 | Common owner 统一选定扫描根与稳定 UserHome 父根，外部解析目标由有界直接稳定父根覆盖；复用既有 tree watcher 与登记范围过滤，不回退 Windows nested handles | 最终 Linux race 和真实 Windows 定向退出 0；普通祖先替换、Close/burst 等非 skip 场景通过。七项 symlink 因权限不足跳过，不能把 package PASS 写成这些子例通过 |
| 新增 scope 容量诊断与修后回归 | 临时 Go overlay 连续登记相同 2057 项，无文件变化仍每遍推进 source；仅 3 个真实目录监听句柄，修前 snapshot 0.235s，FAIL。作者正式 `covered_descendants` 修后 PASS，snapshot 3.303s，scope 为 1，重复扫描 revision 稳定 | 最终 Linux race 和 Windows 定向均包含实际容量场景；结果覆盖普通冗余子范围，不证明任意独立目标数量均可构建 |
| 已覆盖别名目标变更 | A→已覆盖 B 的回归修前 FAIL，snapshot 0.008s，见 `common-covered-alias-before.log`；canonical 映射与对应 revision 更新已修复，最终 Linux race 含修后回归并通过 | 两个包 Windows 交叉编译退出 0；Windows 原生该 symlink 场景因权限不足跳过。旧独立 canonical scope 保持原有有界历史选择，不保证旧目标即时退订 |

Windows 证据保存在设备日志 `pr143-context-source-windows-final.log` 和 `pr143-context-source-diagnostic.log`，后者 14,740 bytes。这些记录支持将 watcher 范围过大列为实际生产路径缺陷；它与旧测试遗漏来源续读分开修正。此前 `7e60922` 的全仓诊断、三个 VCS 环境失败包复验以及旧候选实机来源仍保留，不作为这个新增缺陷已消除的证据。

scope 容量回归的元数据和目录内容断言证明扫描期间没有真实文件变化；source 推进来自新增 scope LRU 的主动淘汰。该逻辑发生在平台订阅分派之前，因此 Windows 即使由一个稳定递归 tree 覆盖全部普通子路径，也会经过原有冗余 scope 路径；此结论来自共同调用路径，本次容量运行环境仍是 Linux。当前修复在 lexical/resolved 两侧都受已有范围覆盖时复用 scope，继续执行平台订阅；正式普通子目录回归已证明 scope 保持为 1、重复扫描 revision 稳定。

后续别名回归另行暴露 A→已覆盖 B 时 canonical 映射未更新，修前结果独立保留。实现现已更新该映射，并只在实际观察到目标变化时推进 revision；最终 Linux race 包含修后回归并通过，Windows symlink 权限跳过独立记录。旧的独立 canonical scope 作为既有有界历史选择继续保留，旧目标上的事件可能保守地增加失效，不宣称它们全部即时退订。普通监听模式超过 2048 个真实句柄、或大量独立外部目标的原有容量策略未在本次改为新策略，仍可能导致有界构建冲突；周期重新验证不会保证任何规模都能完成一致构建。本次修复不扩大容量、不放宽内容一致性，也不删除真实变更校验。

### 顶层 Context 缓存资格与来源保留

独立复审还确认一个与 watcher 范围修复不同的缺口：下层规则、Skill 和 Common 缓存在 watcher 不健康时使用既有重新验证周期，但 `executeContextHandler` 的完整缓存键、已交付引用和 retained source 去重只看稳定 source stamp。真实文件内容在大小与 mtime 均未改变、没有 watcher 事件的条件下变化时，顶层可以阻止下层重新读取，并继续把旧内容作为当前 Context。

`101aca3` 所含的这项补丁仅修改 `internal/app/context_delivery.go`、`internal/app/context_revalidation_test.go` 和 `internal/app/required_output.go`。同一次 tracker `Revisions()` 读取同时取得稳定源标识与 cache revision，避免新增扫描、store 或 runtime。完整缓存、当前 reference 和 retained source 去重复用包含 cache revision 的资格；构建前后的一致性仍只比较稳定 source identity，`ContextRevision` 仍由实际内容决定。`ContextGuardStamp` 不含重新验证时钟；旧 source 不再满足当前引用资格时仅退出引用候选，原有 TTL、权限和规则 guard 继续决定其独立读取资格，不直接销毁已留存来源。

新增回归通过正式 `Runtime.Call` 完成 source 逐页读取至 EOF，并在有对话的场景先确认同一内容实际进入 reference；随后对真实规则或 Common 描述做同大小、同 mtime 修改，仅推进现有可控重新验证周期。修前四项全部失败，分别读到旧内容或旧 reference；修后全部通过，且只有时钟推进、内容未变时 `ContextRevision` 保持不变。既有目录/.git 变化后续页、规则变化拒绝以及 source identity 回归同时通过。上述本地前后对比已独立只读复核；随后冻结集合的 Linux race 与原生 Windows 定向另按其实际覆盖记录；验证当时尚未提交，后来进入 `101aca3`，并不等于该 head 的完整 CI。

### `101aca3` 提交前的 Linux 与原生 Windows 验证

该阶段来源为创建 head `1fec3643fab618ee55bbbe1e168c54b46deda5e9` 加 12 个冻结源码文件：Common 稳定 tree、scope、alias 及 Context source 测试的 9 文件集合（`pr143-common-scoped-watch-freeze.json`），以及顶层 Context cache eligibility 的 3 文件修复。所有文件按 manifest 哈希写回 Windows 独立工作树，`git diff --check` 通过；验证当时尚未 commit/push，后来进入 `101aca3`。

| 验证层次 | 已确认终态 | 范围与限制 |
| --- | --- | --- |
| 最终 Linux race | `common-scoped-tree-final-race.log`：app 6.033s、snapshot 41.335s，退出 0 | 包含正式 scope 容量、别名修后与 Context 重新验证回归；属于定向 race，不是全仓 race |
| Windows amd64 交叉编译 | app、snapshot 两个包均退出 0 | 证明可编译，不替代原生执行 |
| 首个 Windows 测试启动请求 | `call_6ff...` 因根流程引号拼接错误在解析阶段退出；`ParseInput` 确认 1 个 missing-quote 错误，没有测试 log/result | 此请求未启动 Core 测试，不计为 Core 回归失败，也不计为已执行测试 |
| 修正后的真实 Windows 定向 | 10:59:35.7707389–11:01:47.0505740 UTC，退出 0；app 21.8s、snapshot 128.348s；日志 `pr143-final-context-watch-native.log` | JSON 独立归并为 291 events、67,878 bytes，`active_tests={}`、`failed_tests={}`、2 个 package PASS；七项 symlink 权限跳过如下，未计覆盖 |

实际 Windows short/case paths、普通祖先替换、Close/burst、Context counts/revalidation 及容量 case 均为非 skip 并通过。以下七项因缺少 symlink privilege 跳过，package PASS 不代表这些场景已在 Windows 验证：

- `app/TestCommonContextCacheTracksSymlinkTargets/ancestor`
- `app/TestCommonContextCacheTracksSymlinkTargets/root`
- `app/TestCommonContextCacheTracksSymlinkTargets/package`
- `app/TestCommonContextCacheTracksSymlinkTargets/document`
- `app/TestContextCursorSurvivesCatalogAndGitWritesButRejectsChangedRules/marker_symlink_target`
- `snapshot/TestScopedWatchUpdatesAliasWhenTargetBecomesCovered`
- `snapshot/TestScopedWatchWindowsAllowsExternalAliasTargetReplacement`

上述 12 个源码文件随后与两个文档文件一并提交并推送为 `101aca3`，设备工作树已独立确认干净。后续 junction 结果见下一节，不改写这七项 symlink SKIP。原 `1fec364` 首轮 CI 的 35 分钟取消以及各项 before 失败全部保留。

### `101aca3` 的 CI 与 junction 前后对照

该 head 的 35 项检查最终为 **32 success、2 skipped、1 failure**。Ubuntu 和 Windows 全量 Go、CodeQL、Windows PowerShell 5.1 安装器、Linux/ARM 和 Windows snapshot 等作业通过；macOS Core 作业失败，不能宣称整体 CI 通过。Windows full Go 的成功作业耗时 549 秒，只说明新 head 的该门禁完成，不证明旧 `1fec364` 35 分钟取消的唯一根因。

| 实际执行 | 结果与证据 | 对修复的要求 |
| --- | --- | --- |
| macOS Core，run `39027581724` / job `117381503466` | `TestCommonContextCacheTracksSymlinkTargets` 的 ancestor/root/package/document 四个子例均在 `context_snapshot_regression_test.go:240` 报 `retargeted Common link was not observed`；app 60.660s，FAIL。完整日志 `pr143-101aca-macos-job-http1.log`，23,350 bytes | 四例原目标内容修改已被观察，漏的是删除并重建 lexical link。fsnotify v1.10.1 的 kqueue 目标句柄和目录成员登记会吞掉这类改指；应在既有读取屏障核实已知 alias，不能通过延长等待或取消 source 断言解决 |
| Windows junction 目标替换与 A→已覆盖 B，临时 overlay | 12:01:08–12:01:31 UTC，exit 1；app 17.318s、snapshot 10.937s 均 FAIL。`GODEBUG` 未设置，默认 Go 下 junction 为 `ModeIrregular`；外部目标的 rename、rebuild、同大小/mtime 写入均未推进 revision，索引保留旧描述 | junction 能被 Common 实际读取。不能将真实旧缓存归为“不支持链接”，也不能把 junction 探针计作上述 symlink 用例通过 |
| 相同 junction 同 mtime 用例，主线 owner overlay | `TestCommonContextJunctionSameStampOverlay`，12:08:28–12:08:38 UTC；源为 `34270eca` 的 `files.go`、`context_snapshots.go`、`common_skill_snapshot.go`、`common_skills.go` 原文，并隐藏新增 `scoped_tree.go`；PASS，app 5.964s，source `1→4` | 这是四个准确 owner 文件的差异对照，不冒充旧主线完整候选。原地修改保持所选根 stamp、正文长度和 mtime 不变，避免旧嵌套句柄阻止 rename 的另一问题干扰 |
| 同一用例，`101aca3` 当前 owner | 12:08:38–12:08:50 UTC；FAIL，app 7.984s，source `0→0`，BuildID 仍为 1、CacheHit=true、描述仍为 before | 证实 scoped tree 变更新增了漏监听路径：`EvalSymlinks` 将 junction 词法路径误当实际路径，且祖先枚举没有识别 mount-point tag；必须为真实目标建立原有稳定父级 tree |

两套前后对照的测试原文 SHA256 均为 `6d6f0ebd5f812e49850678b5d57655cf8fbac223ae8ad4a1f5bd78ae3eff25ca`。对照日志保存于设备 `pr143-junction-baseline-101aca` 目录；原始替换探针保存于 `pr143-junction-probe-101aca`。这些步骤只写入自身临时目录，未修改被测 checkout，未设置 DeveloperMode、symlink 权限或 `GODEBUG` 兼容模式。

macOS 作业日志的两次直接下载与一次 `gh run view` 返回 EOF，失败诊断分别保留；读取同一已完成作业的独立 HTTP 客户端随后成功。日志下载失败不是 Core 测试失败，也没有据此重跑 CI 作业。

### junction 与 alias 读取屏障的后续修复

这项增量继续由 snapshot owner 管理真实监听位置、已登记 alias 和旧订阅退役。Windows 通过短生命周期、共享读写删除的句柄取得实际 DOS/UNC 目标，并准确识别 mount-point reparse tag；缺失后代仅在已证明为目录的祖先下解析，权限、循环和其他未知错误保留原 cause。Common 仅在既有扫描点补登记精确 `SKILL.md` 路径，不新增目录扫描入口。

macOS/kqueue 与 Windows 在既有 `Files.Sync(ctx)` 屏障重验已知 alias。该表独立于逻辑 scope 合并，并受原 2048 项容量约束；它不读取 Skill 正文。确认改指后，旧的父目录与目标订阅先按现有退役流程移除，再由同一个 owner 挂接实际新目标。Windows 保持互不嵌套的稳定父级 tree；平台事件读取 goroutine 不取得 operation lock，也不执行路径 I/O。Linux 沿用目录事件通知，不增加常态 alias 重验。

独立审查进一步补齐取消边界：operation lock 使用仓库已有的 token-lock 模式，等待者可响应 context 取消，无轮询或后台等待者。alias 状态只在重挂成功并再次确认请求有效后提交；Add 已观察改指或重挂开始时保留 `pendingRearm`。这样即使中途取消，目标又改回原值和相同元数据，下一次有效 Sync 仍会完成挂接。未知错误使 watcher 进入原有不健康重新验证路径，不把旧映射当作已验证的新目标。

| 本次增量验证 | 实际结果与限制 |
| --- | --- |
| 取消等待 operation lock，修前 | 受控持锁、实际 `Files.Sync` 截止测试 FAIL 1.004s，日志 `pr143-alias-lock-cancel-before.log`；保留原失败，不通过放宽时间断言解决 |
| 第一轮联合 Linux race | snapshot PASS 41.524s；较宽正则额外包含的 `TestScopedCommandsAndPatchesUseSelectedWorkspace` 因 `/root/.cache/agentdock/file-locks` 只读导致 app FAIL 8.506s，日志 `pr143-alias-read-barrier-race.log`。这次命令整体仍为失败 |
| 收窄后的相关 Linux race | app PASS 8.685s、snapshot PASS 1.156s，日志 `pr143-alias-read-barrier-final-race.log`；仅该进程的 XDG cache 指向可写临时目录，未修改 HOME、机器配置或源码断言 |
| 最后冻结源码的取消及同元数据 ABA 边界 | snapshot race PASS 1.156s，日志 `pr143-alias-read-barrier-final-boundaries.log`；最终 app 窄 race PASS 8.738s，日志 `pr143-alias-final-app-context-race.log`，对应 Go session 均 exit 0。用例先证明链接元数据与旧 state 相同，再检查 pending rearm、正确 scope/新目标监听及连续 Sync 的稳定 source |
| 最终 Windows amd64 联合编译 | app、snapshot 两包进程均 exit 0，包含最后新增的 `TestScopedWatchWindowsJunctionReadBarrier`；Darwin arm64 snapshot 交叉编译也 exit 0，不计为 macOS 原生运行 |
| 原生 Windows 冻结源码回归 | 12:44:10.1802280–12:44:33.6363321 UTC，exit 0；snapshot PASS 6.105s、app PASS 16.096s。JSON 日志 `pr143-alias-junction-fixed-native.jsonl` 共 32,648 bytes / 130 events，独立汇总无 active tests、无 failed tests；进程 GODEBUG 未设置 |
| 原生新场景覆盖 | 四项 `TestWatchPathWindows*`、JunctionRetarget、JunctionReadBarrier 与 CommonContextCacheTracksJunctionReplacement 共七个顶层用例全部实际 PASS。包括默认 Go 语义、目标改名/重建、同长度同 mtime 修改、A→已覆盖 B、只靠 Sync 的无通知改指、反复 Sync 稳定及删除 junction 后目标仍在 |
| 本组明确未覆盖 | 五个既有 app symlink 子例因系统权限 SKIP：Common ancestor/root/package/document 和 cursor marker_symlink_target。先前 101aca 组另有两个 snapshot symlink SKIP，本次窄正则未选择它们；所有历史七项仍保留，不被 junction PASS 代替 |
| 新提交全仓与 CI | 待后续准确 head 实际运行。`101aca3` 的 macOS/junction FAIL 与原 before overlay 仍为历史失败，不能由编译或新定向用例改记为该旧执行的通过 |

本节记录相对 `101aca3` 的修复和提交前验证。12 个源码文件由 `pr143-alias-junction-fixed-source-manifest.json` 绑定，写回请求 `call_fcb8de59cb6f743fc28a118338d5dfed` exit 0；原生请求 `call_938a66cdd4267c1dda93306ecfc286e9` / `session-599176481090c74d617e41b6` 的终态与 JSON 独立汇总均已确认。原 Activity 恢复、完整来源归属、Context 内容 revision 以及旧 source 的权限/规则 guard 没有因该读取屏障而改为另一套判定。正式交付以相同冻结文件的原生结果和后续准确 head 的 CI 为准。


### 最终产物核对与只读候选启动准备

发布核对与只读候选 helper 三文件已写入设备 artifact。`candidate-final-lifecycle.ps1` 已在真实 Windows PowerShell `5.1.22621.6133` 执行 `ParseFile`，结果为 **0 syntax errors**；此步骤只检查语法，尚未执行候选启动、环境隔离、监听归属检查或终止流程。最终 release artifact 的摘要、完整 commit/build run 绑定和独立只读 smoke 均待实际执行；下节 `516a3ba` 的历史实机证据不因此改记为最终产物验证。

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

## 历史合并后的独立只读复核

对 `a7f2bba` 复核，并确认 `516a3ba` 的 3 文件 guidance 增量没有改动以下路径：

- `contextCatalogSummary` 同时生成域字段、数量和目录状态；`prepareContextBootstrap` 与 `refreshContextBootstrapReference` 共用该 owner。首页复制完整 summary，详情续页沿固定快照，引用响应先重新核对来源。Context full/summary 文本协商没有另一套数量转换。
- `mcpResultSourceCapture` 按终态原调用及 owner/conversation/task/thread/workspace 校验，再匹配精确来源引用或其历史版本；宿主页不能成为新的默认源。来源完整性与页面交付完整性独立。
- `mcp_result_raw` 使用独立的原始字节、作用域校验和诊断读取进度；规范化分页或 cursor 重放不会改写 raw_result_ref 指向的来源。
- 原有精度/非等价文本回归与错误分片/重放回归同时保留，未在合并冲突中互相覆盖。

当时的该范围复核没有发现上述链路的新增实现缺口，回归分别验证非零计数与 full/reference、域字段及 SDK full/summary；当时没有单个 fixture 联合逐值比对全部三者。本段保留为历史代码审查记录，不新增测试运行，也不覆盖后来 #143 通过 Windows 诊断确认的 Common Skill watcher 缺陷。

## 明确限制

1. **官方 MCP SDK 的上游浮点解码仍是精度边界。** 当前 go-sdk v1.7.0 的动态 `StructuredContent`、顶层 `_meta` 和原生 content 的动态 metadata，可能在进入 Core 前已转换为 `float64`。已观察 `_meta.revision=18446744073709551615` 进入首屏时变成 `18446744073709552000`。本次修复保持 Core 自有解析和已接收精确值的后续精度，无法还原已舍入原值；缓存测试明确从 Core 精确元数据开始，不能当作上游 wire 保真证据。未创建 SDK fork、旁路 transport/session 或第二套 raw 缓存。
2. `516a3ba` 隔离候选已实际验证 Plugin Ubuntu WSL 的 cwd、私有数据目录和只读命令结果；managed WSL 因缺少实际 selector 未覆盖。`243a801` 的分类修复通过原生 Windows native/WSL 回归，但不属于该旧候选二进制的实机结果。Android broker、stdio 和假浏览器夹具仍只验证对应 owner 和协议边界，不能扩大为真实 Android、Chrome 或所有远端 MCP 服务验证。
3. 当前 Linux 沙箱缺服务账号的全仓失败仍保留。环境归因不替代最终原生 CI 结果，不据此豁免安装门禁。
4. 仓库既有 EasyEDA 实机记录对应旧候选，仅说明原功能曾执行读取、最小编辑、独立读回和清理；不能代替最终质量 head 的验收。本材料未重新操作 PCB 或生产 Core。
5. 功能 PR 检查、独立质量 PR 检查、候选构建、安装测试和发布是不同状态。完整候选须绑定最终精确 main SHA；状态缺失时保持待填。
6. #126/#140 的最终检查与合并已完成；#143 创建 head 的首轮 CI 定格为 27 success、2 skipped、5 failure、1 cancelled。Windows 作业超过 35 分钟预算，但日志未取得，不能进一步归因。`101aca3` 的 CI 为 32 success、2 skipped、1 failure；后续冻结修复的 Windows junction/Context 定向已实际通过，macOS retarget 仍待新 head 的原生 CI。七项历史 Windows symlink 权限跳过及本次实际五项跳过都不计覆盖。`7e60922` 的原超时、JSON 全仓失败以及三个实际失败包的环境复验分别保留，不能拼接成单次全仓 PASS，也不能替代 #143 最终 head 的正常 checkout 检查。
7. 发布 helper 写入和 Windows PowerShell `ParseFile` 通过仅证明文件已落地及脚本语法可解析，不证明最终产物、候选隔离、启动/停止或业务 smoke 已验证。本轮尚未启动最终 release artifact 候选。
8. scope 合并只消除被已有 lexical/canonical 范围覆盖的冗余项。旧独立 canonical 选择有界保留，允许保守的额外失效；原有超过 2048 个物理句柄或大量独立外部目标的容量边界仍存在，可能触发有界构建冲突。未通过周期重新验证承诺任意规模成功，也未改用无限容量或第二套监听策略。Windows 无 nested fallback，外部目标用直接稳定父根；不承诺稳定根以上任意祖先可物理 rename。

## 可复核交付入口与后续替换

以下条目提供当前可复核入口；正式交付时由根流程用实际结果替换“待确认”，并记录新的核对时间。

| 交付字段 | 当前可复核入口 | 最终替换条件 |
| --- | --- | --- |
| 本轮功能 PR 与合并提交 | #126 `043d7316cf77b1fa09c9bf2025cbc1b88bf1f752`；#140 `34270eca8286867ec47930ac1023acd1ecf9e4f8`；各自最终 37 success、2 skipped | 本批 Core 功能合并已完成；保留对应 head、原失败与复验记录 |
| 独立质量 PR 的 base main / 创建 head / 编号 | [#143](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/pull/143)；base `34270eca8286867ec47930ac1023acd1ecf9e4f8`，创建 head `1fec3643fab618ee55bbbe1e168c54b46deda5e9` | 本文保留创建及首轮审查快照；后续最终 head、CI 与合并 SHA 由 PR 正文和最终报告按实际结果填写 |
| 最终差异与原修复目标 | 本文问题—修复—行为表及上述精确源码 | 按 PR 实际 `base main..quality head` 逐项复核；已进入 main 的实现不重复提交 |
| Windows 诊断与最终质量门禁 | `7e60922` 历史终态及环境复验保留；#143 旧 Windows 新测试和 overlay 失败、首轮 full Go 35 分钟预算取消且无日志均保留。12 文件修复已完成 Linux race、Windows 交叉编译及真实定向，七项 symlink SKIP 独立列出 | 本节后续 12 文件修复已通过原生 Windows junction/Context 与最终 Linux 窄 race；提交后链接准确新 head 的 Actions、CodeQL、Windows/Linux 全仓和安装检查，不改写旧失败及跳过结果 |
| 候选源码、构建与实机验证 | [516a3ba 机器证据](core-quality-candidate-readonly.json)；`243a801` 原生分类回归；`836fb50` 安装器定向回归；隔离候选已停止 | 按质量 PR 合并后的最终 main 填写候选构建 run、可执行文件摘要、必要实机读取及安装门禁；历史候选不改标为最终产物 |
| 发布与资产核对 | 发布前备份已完成，生产 Core 未替换；helper 三文件已写入设备 artifact，生命周期脚本仅完成 Windows PowerShell 5.1 语法检查，未执行最终候选或本轮最终 main 发布 | 实际完成后填写精确源码与 build run、资产及摘要、隔离候选证据、标签和发布记录；脚本语法或候选编译成功不表示已启动或发布 |
