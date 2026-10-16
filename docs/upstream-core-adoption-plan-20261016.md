# AgentDock Workbench 母仓库 Core 吸收执行计划（2026-10-16）

## 1. 对比起点与目标

本计划基于以下已核对的提交：

- Workbench `origin/main`：`b9ffb46bf7ca76b53ea40c9b828aee15c235dc27`。
- 母仓库 `uvwt/agentdock` `upstream/main`：`8c2389214b5d13172fc6060a2a70b2d1100ee440`。
- 最近一次双线合并：Workbench `fc2f93832867352de6f6a6d81119ddb85e524b1a`，纳入母仓库 `b20dc723342bd765fa0c32d172eeca44fa02a8ea`。
- 此后母仓库有 89 个独有提交；本计划只吸收经过代码核对后仍缺失的 Core 行为，不合并前端、发行界面或母仓库整条历史。

目标是修复 Plugin 审查确认、生命周期事务、运行版本隔离和 Bridge 协议等正确性问题，再补齐 Remote MCP 认证、标准文件输入、Trace 和小型性能改进。所有实现必须保持 Workbench 现有 Activity、任务/工作区绑定、Heavy Plugin、动态 MCP、权限、输出预算和跨平台执行结构。

## 2. 保留的产品边界

1. 不用母仓库 Plugin Manager 整体替换 Workbench 的 Plugin Store；现有 `plugins/<name>`、`.state`、`.config`、`.data`、Heavy 状态、成员开关和 Adapter 兼容层继续有效。
2. 不建立第二套 Activity、任务或运行分析存储；W3C Trace 和内部阶段耗时写入现有 Activity。
3. 不取消工具输出预算、活动源、结构化分页和无损续读。
4. 不把 Skill 完整描述无界塞入 `agentdock_context`；保留上下文预算，必要时增加明确的截断标记和按需读取入口。
5. 不加入 Resource Library 云备份功能，除非后续单独确认跨设备 Skill/Plugin 备份需求。
6. 不修改 Windows、macOS、Android 前端布局，也不在这些 Core PR 中发布新版本。
7. 每个实现 PR 只补与修改行为直接对应的回归用例，不新增实验框架或大规模测试工程；完成一次代码审查，以现有 GitHub Actions 编译和检查通过作为合并条件。

## 3. 开放 PR 协调

当前开放 PR 中：

- `#126` 修改 `internal/app/agentdock_context.go`、`internal/app/runtime.go`、`internal/mcp/client/*`、命令执行与 Activity。
- `#128` 修改 MCP 模型输出预算。
- `#130` 修改 `internal/mcp/client/*`、动态 MCP 结果和 Schema 投影。

因此，涉及 `agentdock_context`、`internal/app/runtime.go`、`internal/mcp/client` 和 MCP 返回适配的实现必须以 `#126/#130` 完成后的最新 `main` 为起点。Plugin Store、Skill Store、Bridge、媒体文件输入和 Browser 修正可以先独立实施。

## 4. 实施 PR 顺序

### PR 1：将 Plugin 安装确认绑定到已审查内容

**目标**

修复 `plugin_manage.confirmed` 当前只存在于 Schema 和请求结构、实际安装路径没有读取的问题，并消除 `validate` 与 `install/update` 分别准备候选造成的审查内容漂移。

**修改**

- 在 `internal/plugin/source_types.go` 的 `Review` 增加 `review_token`。
- 在 `internal/tool/plugin/request.go` 和公开 Schema 增加 `review_token` 输入。
- 令牌至少绑定 `package_digest`、Adapter/格式、规范化来源身份、warnings 和 executables；使用固定版本前缀，禁止把路径、凭据或环境值写入令牌材料。
- `validate` 返回服务端生成的 `review_token`。
- `install/update` 重新准备候选并重新计算令牌；缺少令牌返回 `PLUGIN_REVIEW_REQUIRED`，不匹配返回 `PLUGIN_REVIEW_CHANGED`。
- `confirmed` 暂时保留用于兼容旧调用，但不再具有授权作用；只传 `confirmed: true` 仍必须被拒绝。
- `confirmed_source_change` 继续只处理已安装 Plugin 的来源改绑确认。

**直接验收**

- 未提供令牌时无法安装或更新。
- 来源内容、适配结果、可执行文件或 warning 改变后，旧令牌失效。
- 同一内容重新准备后令牌稳定，安装结果保留现有来源和成员语义。

**依赖**

无，可从最新 `main` 独立开始。

### PR 2：Plugin 持久激活事务、版本读租约与运行 generation

**目标**

让 Plugin 安装、更新、删除和长任务在进程退出、并发进程和版本切换期间保持可恢复的一致状态。

**修改**

- 在 `plugins/.state/transactions/` 保存原子写入的激活事务，记录 owner、操作、旧状态摘要、候选摘要和阶段。
- Store 初始化时扫描未完成事务，依据正式状态、候选目录、备份目录和摘要恢复；无法安全判断时保留恢复材料并返回明确错误，不猜测成功。
- 首次安装和更新都在第一次目录变更前写事务；正式状态只在 Plugin MCP/Skill 激活成功后发布。
- 用 owner 与正式状态 CAS 防止另一进程完成或覆盖当前事务。
- 为运行中的 Plugin Skill、Plugin MCP 和相关命令增加具体版本读租约；更新发布新代后，旧代只在读者释放后清理。
- stdio MCP 使用 `plugins/.runtime/generation-*` 的私有运行快照，运行时写入不得污染已安装包。
- 保持用户可见安装路径、`.data`、`.config`、Heavy 和成员开关不变，不迁移为母仓库整套版本目录布局。

**主要代码位置**

- `internal/plugin/store.go`
- `internal/plugin/manager.go`
- 新增职责明确的 `transaction.go`、`lease.go`、`runtime_generation.go`
- `internal/tool/skill/scoped_reference.go`
- `internal/plugin/mcp.go`
- `internal/tool/plugin/service.go`

**直接验收**

- 在准备、目录切换、状态提交和激活阶段中断后，重启可恢复到唯一有效状态。
- Plugin Skill 返回真实释放函数，更新不能删除仍被运行任务读取的版本。
- stdio MCP 修改运行目录不会改变安装包摘要。

**依赖**

在 PR 1 合并后实施，复用其候选摘要和审查身份。

### PR 3：Skill 普通安装与切换的持久交换事务

**目标**

将普通 `skill_package install/update/activate/uninstall` 的当前调用回滚升级为可跨进程恢复的交换事务；bundled bootstrap 保持现有独立批量事务。

**修改**

- 在 Skill Store 私有状态目录保存 `SwapTransaction`，记录 Skill、旧内容摘要、候选摘要和阶段。
- 普通安装、切换和卸载在目录变更前写事务，状态提交后清理备份和事务。
- Store 初始化时恢复未完成交换，并核对当前目录、版本归档和备份内容摘要。
- 复用现有跨进程 Skill 读写锁，不建立第二套锁系统。

**直接验收**

- 旧目录已移动、候选已移动和状态已提交三个中断位置均能确定恢复。
- 摘要不匹配时停止自动恢复并保留材料。
- 现有版本历史、激活、回滚和 `.system` 语义不变。

**依赖**

无，可与 PR 1、PR 4、PR 6 并行。

### PR 4：Bridge 能力声明与正式协议契约

**目标**

修复 Bridge Hello 只声明 `artifact.read`，实际却处理 `runtime.request`、`context.local`、`resource.read` 等操作的不一致。

**修改**

- 更新 `github.com/uvwt/agentdock-protocol` 到母仓库已使用的兼容版本。
- 使用 `protocol.ConnectPath`、`protocol.PairPath`、`protocol.PairRequest` 和 `protocol.PairResponse` 替代本地硬编码路径和响应结构。
- `BridgeCapabilities` 从实际注册的 handler 生成，至少准确声明 capabilities negotiation、context local、runtime request、resource read 和 artifact read。
- Hello 只声明实际存在的 handler 能力；收到未实现 operation 时继续返回明确的 unsupported error，不新增与协议包重复的本地协商状态机。保留 Workbench 已有 tool call、取消、panic 隔离和调用排空语义。

**直接验收**

- Hello 声明与 `invoke` 可处理操作一致。
- Pair/Connect 的路径和消息结构来自协议包。
- 旧协议不兼容时返回明确错误，不静默降级为错误能力集合。

**依赖**

无，建议在 Trace PR 之前合并。

### PR 5：Remote MCP 可选环境绑定语义

**目标**

区分 `${ENV}` 必填绑定和 `${ENV:-}` 可选绑定，避免匿名可用的 Plugin MCP 因可选凭据缺失而返回认证错误。

**修改**

- 将环境引用解析改为返回 `name`、`required`、`ok`。
- `MCPComponent.RequiredEnv` 必须完整传入 `mcpclient.ServerConfig`，同时保留 `EnvBindings`。
- 可选 HTTP Header 未配置时不发送 Header；可选 stdio 环境变量未配置时传空字符串。
- 必填值缺失继续返回现有认证/凭据错误。
- 保留 Workbench 的最小环境继承、包内公开默认值、宿主覆盖顺序、同源 Header 和重定向保护。

**直接验收**

- `${TOKEN}` 缺失时失败。
- `${TOKEN:-}` 缺失时 HTTP 不发送对应 Header，stdio 得到空值。
- 必填与可选变量混用时只报告真正缺失的必填项。

**依赖**

等待 `#126/#130` 完成，以合并后的 `internal/mcp/client` 为准。

### PR 6：标准 Connector 文件对象兼容

**目标**

在保留宿主挂载路径的同时支持 OpenAI Connector 的 `download_url/file_id` 文件对象。

**修改**

- `file_publish.file` Schema 接受已挂载路径或标准文件对象，两种来源互斥且都可与本地 `path` 分支明确区分。
- 发现宿主挂载路径时继续走现有工作区解析。
- 只有没有挂载路径时才下载 `download_url`。
- 下载禁用环境代理，限制协议和重定向，拒绝非公网 DNS 结果及二次解析，限制声明与实际字节数，使用私有临时目录和 `0600` 文件，并在调用结束后清理。
- `file_id` 只作来源身份，不当成本地路径或下载地址。

**直接验收**

- 现有 binary string 和挂载对象调用保持兼容。
- 标准对象可以发布文件。
- 私网、回环、CGNAT、超限内容和不安全重定向被拒绝。

**依赖**

无，可独立实施。

### PR 7：Remote MCP 出站 OAuth

**目标**

让 AgentDock 作为客户端连接需要 OAuth 的 Remote MCP，不改动保护 AgentDock 服务端的现有入站 OAuth。

**修改**

- 新增独立 `internal/mcp/oauthclient`，负责 Protected Resource Metadata、Authorization Server Metadata、PKCE、动态客户端注册、Token 刷新和状态管理。
- OAuth 客户端注册和授权记录原子写入宿主私有目录，权限为 `0600`；不得进入 Plugin 包、工作区或活动参数。
- 支持本地回环、AgentDock 公网 Origin 和 Nexus 握手声明的 HTTPS Origin 回调。
- `state` 一次性消费并有过期时间；回调响应禁止缓存和 Referer。
- 动态 MCP Client 在收到认证挑战时返回可执行的授权状态，而不是循环重试或把长 HTML/错误正文送给模型。

**直接验收**

- 授权发现、PKCE、回调 state、Token 刷新和注销状态正确。
- Token、code、client secret 不进入日志、Activity、工具结果和 Plugin 数据。
- 入站 OAuth 现有行为不变。

**依赖**

PR 5 和 `#126/#130` 完成后实施。

### PR 8：W3C Trace 接入现有 Activity

**目标**

补齐跨 MCP HTTP、Nexus Bridge 和 Remote MCP 的因果链，不复制母仓库 Recorder 或分析页面。

**修改**

- MCP HTTP 入口解析 `traceparent/tracestate`，Nexus Bridge 读取和传播 Trace Context，Remote MCP 调用创建子 span。
- 将 `trace_id`、本地 `span_id` 和固定内部阶段写入现有 Activity 调用记录。
- 首批阶段只包括 `mcp.refresh`、`mcp.remote_call`、`command.start`、`command.foreground_wait`，单次调用数量有硬上限。
- 远程诊断投影只返回固定工具名、来源、耗时、错误码、Trace ID 和阶段；不包含参数、结果、命令、路径和错误正文。
- 保留 `request_id/call_id/parent_call_id` 的现有业务语义，Trace ID 不作为任务、对话或权限归属键。

**直接验收**

- 同一根调用跨 HTTP、Bridge 和 Remote MCP 时 Trace 连续。
- no-op tracer 不伪造本地 span。
- Activity 与远程诊断中无敏感 Payload。

**依赖**

PR 4、PR 7 以及 `#126/#130` 完成后实施。

### PR 9：Context 与 Browser 小型修正

**目标**

吸收范围小且行为确定的性能和工具契约修正。

**修改**

- `templateCapabilityIndex` 与 `memoryCapabilityIndex` 并行加载，分别保留 warning 和取消状态。
- 本地 Chromium allocator 增加 `chromedp.WSURLReadTimeout(req.Timeout)`。
- `browser_snapshot` 的注解反映截图 Artifact 发布和 `close_after` 的副作用，不再宣称完全只读。
- 如 Linux/systemd 仍在支持范围，`agentdock nexus pair/status` 增加 `--agentdock-home`，安装环境显式写入 `AGENTDOCK_HOME` 和 `AGENTDOCK_DEFAULT_DIR`。

**直接验收**

- Workflow 或 Recall 一侧失败不阻塞另一侧返回。
- Browser 启动超时同时约束进程启动和 DevTools WebSocket 地址读取。
- 工具注解与实际 Artifact/会话副作用一致。
- CLI 与服务使用同一个 AgentDock 数据目录。

**依赖**

Context 部分等待 `#126` 完成；Browser 和 Linux 子项可先拆出独立 PR。

## 5. 不纳入本轮实施

- 不实现 Resource Library 云端备份、上传、下载和设备间恢复。
- 不直接取消 Skill 路由描述长度限制；后续如确认路由信息丢失，再增加 `description_truncated` 和按需读取。
- 不回搬母仓库 ACP 卡片、会话标题或 Runtime Overview；Workbench 已有更完整的 ACP Run、SessionProjection、Activity 和管理接口。
- 不合并母仓库桌面 UI、托盘、发布、R2、SignPath、Cloudflared 和 README 更新。
- 不用一次大 PR 同时修改 Plugin、Skill、Bridge、OAuth、Trace 和 Context。

## 6. 执行和合并规则

1. 每个实现 PR 从当时最新 `main` 建立独立工作树；只有 PR 2、PR 7、PR 8 使用明确的前置依赖。
2. 同一文件被开放 PR 修改时，等待前置 PR 合并后再开始，不用长期堆叠分支解决冲突。
3. 每个 PR 在描述中列出对应母仓库提交、Workbench 保留边界、实际修改文件和未执行检查。
4. 只增加直接覆盖修改行为的回归用例；不引入新的测试框架、实验代码或与修改无关的测试矩阵。
5. 修改后做一次代码审查，确认错误状态、恢复路径、权限、敏感数据和资源清理；随后以现有 GitHub Actions 编译及检查通过作为合并条件。
6. GitHub Actions 失败时只修复与当前 PR 有关的问题；不顺带重构无关模块。

## 7. 对应母仓库提交

- Plugin 审查、持久事务和 Skill 生命周期：`c3db3f1`、`120527c`、`436d0b9`。
- Plugin 状态、Adapter 和可选环境绑定：`e121e69`。
- Bridge 能力与正式协议：`2817f77`、`ff014b5`。
- Remote MCP OAuth：`c361052`。
- Connector 文件对象：`1e1f1ec`、`2de2f3d`。
- Context 并行加载：`6635865`。
- 运行观测与 Trace：`dcb8cf0`、`d9712ed`、`0a5675e`、`2ffed4e`。
- Browser 超时和工具注解：`18e12e5`、`3001704`。
- Linux Core 数据目录：`63be28a`。
- 本轮明确不直接吸收的完整 Skill 描述和 Resource Library：`a4848d1`、`49f0801`、`8107019`、`d90df96`、`acf41f6`。

## 8. 执行清单

- [ ] PR 1：Plugin `review_token` 与确认契约。
- [ ] PR 2：Plugin 持久事务、版本租约和 runtime generation。
- [ ] PR 3：Skill 持久交换事务。
- [ ] PR 4：Bridge 能力与正式协议契约。
- [ ] 等待并整合 `#126/#130` 的最终主线状态。
- [ ] PR 5：Remote MCP 可选环境绑定。
- [ ] PR 6：标准 Connector 文件对象。
- [ ] PR 7：Remote MCP 出站 OAuth。
- [ ] PR 8：W3C Trace 接入 Activity。
- [ ] PR 9：Context、Browser 和可选 Linux service-home 修正。
