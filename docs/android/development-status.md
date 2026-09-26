# WB07 开发与验收状态

本文件描述候选源码能力。最终通过状态必须绑定具体 Git 提交和对应 GitHub Actions run；测试签名 APK、Fixture 截图和页面可打开均不等于真机安装验收。

## 已实现

| 模块 | 当前实现 | 自动化证据 |
| --- | --- | --- |
| 页面与导航 | 16 个 Workbench 目的页、SavedState 返回栈、紧凑/展开布局、深色、大字体和横屏 | WorkbenchNavigationTest、23 张截图 |
| 工作区 | 列表、详情、登记、按 revision 更新、目标解析 | WorkspacePage、WorkbenchCommandsTest |
| 任务与对话 | 有界筛选/分页、批量管理、任务生命周期、线程全流程、对话绑定/关联/恢复/终止 | TaskConversationPanels、ManagementContractTest |
| 调用 | 历史游标、父子调用、事件、输出分段、停止、批量管理、范围导出 | CallManagementPage、CoreClientHttpTest |
| 插入 | 稳定 submission_id、服务端回执字段、取消与有限重投、草稿恢复 | 插入页面与导航回归 |
| 审批 | 待处理及历史状态、详情、规则预览、一次性/工作区批准、拒绝 | ApprovalManagementPage |
| 权限 | global/workspace/conversation 模式、workspace 配置继承、configured/effective 回读、revision 与 full 确认 | PermissionManagementPage |
| Skill/插件/MCP | Skill 文件浏览与启停；插件安装/更新/成员/heavy；MCP 增删、刷新、环境变量 | SkillManagementPage、PluginMcpManagementPage |
| Core 配对 | 手动 Origin 绑定；本机一次性 RSA-OAEP；远程 OAuth 动态注册 + PKCE S256 + loopback | OAuth/Keystore 单元与仪器测试 |
| Termux 部署 | journal、续接/取消、签名门禁、安全解包、schema 快照、健康回退、保留、恢复退避/熔断 | 41 项 test_deployment.py、桥契约测试 |
| 项目文件 | SAF 有界 ZIP 导入导出、目录树/预览、Termux 路径探针和创建工程 | ProjectArchivePolicyTest、项目 Fixture |
| 后台与设置 | WorkManager/FGS/Tile、Wi-Fi/充电约束、通知、主题、密度、Core 输出设置 | GuardianConstraintPolicyTest、适配截图 |
| 脚本治理 | 6 个 WB07 CI/测试脚本进入共享清单；候选工作流只允许新增这些登记，不允许删除旧条目或扩大跨线路径 | TestScriptGovernanceInventoryCoversWorkspaceScripts、Source and bridge contracts |

## 自动化门禁

- Android compileSdk/targetSdk 37，minSdk 26，JDK 17。
- Lint、Debug/Release-shaped/Test APK 构建及签名核验。
- JVM 测试不少于 60 项，全部通过且无跳过。
- 部署测试不少于 41 项，包含 13 个逐阶段中断恢复案例和真实 OpenSSL 签名验证。
- Android 仪器测试不少于 13 项，全部通过且无跳过。
- API 26、33、34、35、37 每个模拟器均需 23 张有效 PNG、真实 API 一致且单次运行成功。
- Ubuntu/Windows 通用 Go 检查覆盖共享脚本清单；Ubuntu 还执行完整 Linux 脚本入口回归。

## 仅真机或外部发布线可验证

- 外部 Termux `RUN_COMMAND` 权限、Debian PRoot 和真实 Linux ARM64 Core 启动。
- 真实 Core 的管理副作用、本机一次性配对和远程浏览器 OAuth 完整链路。
- 主线尚未提供的发布者签名 ARM64 清单/公钥；缺失时 install/update 必须保持 `pending_manifest`。
- SAF 真实文档提供方、系统返回手势、进程被杀、重启、Doze 和厂商电池策略。
- 30 分钟、2 小时和 8 小时运行观察。
- 生产签名、真实安装、Release、标签和发布。本任务明确不执行这些操作。
