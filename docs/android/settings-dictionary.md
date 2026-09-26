# Android 设置归属词典

| 字段/组 | 默认值或范围 | 唯一有效归属 | 实际消费者与边界 |
| --- | --- | --- | --- |
| theme | system/light/dark | APK DataStore | WorkbenchTheme；Actions 有深色截图 |
| language | system | 兼容字段 | 当前只跟随系统，不显示无效语言切换入口 |
| density | comfortable/compact | APK DataStore | 页面间距与紧凑导航 |
| endpoint / remoteEndpointEnabled | loopback / false | APK 连接配置 | EndpointPolicy；远程必须 HTTPS |
| Core credential | 空 | Keystore 密文 | 精确 Origin 绑定；手动、本机配对或 OAuth 三选一 |
| notificationsEnabled | true | APK + 系统授权 | Android 13+ 请求通知权限 |
| guardianEnabled / guardianPaused | false / false | APK | FGS、WorkManager、Tile；暂停不停止 Core |
| autoRepairEnabled | false | APK 意图 + Termux 事实 | desired=running 才请求当前版本恢复；退避/熔断在 Termux 持久化 |
| bootHealthCheckEnabled | false | APK | BootReceiver 只调度延迟 WorkManager |
| guardianIntervalMinutes | 15，范围 15–1440 | APK | WorkManager 与 FGS 周期 |
| onlyOnWifi / onlyWhileCharging | false / false | APK | WorkManager 和前台守护都检查 |
| detailedCalls | false | APK | 调用详情原始字段与树视图 |
| toolOutputEnabled / MaxChars | true / 20000，范围 1000–100000 | APK + Core | payload UTF-8 游标和读取上限 |
| customPermissionEnabled | false | Core | 本地仅编辑草稿，保存带 revision |
| permission profile / approval | write/allow/none、on-request/user | Core | global/workspace 配置、workspace inherit、effective/configured 回读 |
| desiredNodeState | stopped | APK + Termux | 停止先落盘；守护不逆转 |
| projectTreeUri / artifactTreeUri | 空 | Android SAF | 用户显式目录、ZIP 导入导出；不猜测 Termux 路径 |
| allowMobileData / publicAccessEnabled / onboardingComplete | 兼容字段 | 无活动 UI | 未伪造未实现副作用 |

## Data retention

卸载 APK 会失去应用私有配置和 Keystore 材料，但不会删除外部 Termux 节点或用户工程。部署清理只删除没有 current、验证回退点或未完成事务引用的受管旧版本/快照；隔离数据、身份、工作区和用户文件不自动删除。
