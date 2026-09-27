# Android 与 Windows Workbench 功能矩阵

状态含义：**已实现**表示 Android 客户端与共享契约已接线；**Actions**表示可由隔离 CI 验证；**真机边界**表示仍需外部 Termux、真实 Core 或 Android 设备，不降低源码完成度，也不得伪报通过。

| ID | 功能 | Android 实现 | 自动化证据 | 真机/外部边界 |
| --- | --- | --- | --- | --- |
| WB-A01 | 节点总览 | 分域快照、错误与空态分离 | 首页/错误 Fixture | 真实 Core 身份 |
| WB-A02 | 工作区 | 列表、详情、登记、revision 更新、resolve | WorkspacePage | 真实路径副作用 |
| WB-A03 | 对话列表 | view/status/search/workspace/tag 分页 | ManagementContractTest | 大历史真实负载 |
| WB-A04 | 独立任务中心 | 详情、生命周期、步骤、全部线程动作 | TaskConversationPanels | 真实任务副作用 |
| WB-A05 | 元数据管理 | 置顶、标签、重命名、归档、回收站、恢复、永久删除确认 | 批量契约测试 | 真实 Core 逐项结果 |
| WB-A06 | 对话任务绑定 | link-task、current-task revision、attach/detach、resume | 绑定请求构造测试 | 并发冲突联调 |
| WB-A07 | 活动流 | SSE 游标、去重、断线边界和快照刷新 | BoundedSseParserTest | 长时断网恢复 |
| WB-A08 | 调用详情 | 详情、父子调用、事件和 UTF-8 游标输出 | CallManagementPage | 大输出/深树联调 |
| WB-A09 | 调用管理 | archive/isolate/trash/restore/delete、显式范围导出 | 命令测试 | 真实导出内容 |
| WB-A10 | 插入回执 | 草稿、稳定 submission_id、服务端回执、取消/有限重投 | 导航与 Fixture | 实际领取/过期时序 |
| WB-A11 | 停止与恢复 | conversation terminate/resume、call stop；无伪造 retry | 写隔离回归 | 真实运行中调用 |
| WB-A12 | 审批 | pending/history、详情、规则预览、批准/拒绝 | ApprovalManagementPage | 过期/冲突并发 |
| WB-A13 | 权限 | 全局/工作区/对话模式、配置继承、有效值回读 | 权限请求契约 | 真实 revision 冲突 |
| WB-A14 | Skill | detail/files/file、standalone/plugin member 启停 | SkillManagementPage | 实际 Skill 文件 |
| WB-A15 | 插件/MCP | 插件完整管理；MCP 增删刷新和环境变量 | PluginMcpManagementPage | 外部 MCP/OAuth |
| WB-A16 | Core 连接 | 手动、Origin 绑定、一次性本机配对、远程 OAuth/PKCE | OAuth/Keystore 测试 | 浏览器与真实 Core |
| WB-A17 | 安装更新 | 持久事务、签名门禁、续接、快照、回退、保留、熔断 | 41 项部署测试 | 签名清单和 ARM64 |
| WB-A18 | 工程文件 | SAF 导入导出、树/预览、Termux 探针/创建 | ProjectArchivePolicyTest | 真实文档提供方 |
| WB-A19 | 日志诊断 | 有界脱敏日志、游标搜索、诊断预览/导出、清理预览确认 | 部署与桥测试 | 真实日志内容 |
| WB-A20 | 外观输出 | 主题、密度、详细调用、Core 输出 revision 设置 | 深色/大字体/横屏/展开截图 | 厂商字体渲染 |
| WB-A21 | Android 守护 | WorkManager、可见 FGS、Tile、desired state、Wi-Fi/充电、熔断 | GuardianConstraintPolicyTest | Doze/厂商策略/长时 |
| WB-A22 | 返回恢复 | SavedStateHandle、Activity back、筛选/选中/草稿恢复 | recreation/back 测试 | 系统手势/进程杀死 |
| WB-A23 | 错误空态 | 稳定错误、鉴权终止、业务拒绝不作成功、Fixture 写隔离 | HTTP/SSE/导航测试 | 真实服务降级 |
| WB-A24 | 回执安全 | operation/request/nonce、年龄、终态不可回退、秘密字段拒绝 | ResultValidator/Store 测试 | 实际丢回执链路 |

## 验收解释

Actions 证明客户端逻辑、协议边界和模拟器兼容性。真实安装、真实 ARM64 PRoot、生产凭据、长期保活和发布者签名材料必须单独取证。本任务禁止以真实安装补足这些证据。
