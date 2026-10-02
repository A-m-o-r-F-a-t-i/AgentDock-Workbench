# WB07 candidate validation plan

所有依赖解析、Android 编译、Lint、JVM 测试、仪器测试、截图和 APK 打包只在 GitHub Actions 执行。手机工作区只做源码编辑、Git、XML/Python/Shell 静态语法检查；不安装 APK、Termux 或 Core。

## Contract job

- 校验精确 source SHA 和 WB07 文件归属。
- 解析 Android XML、Python AST 和 Shell 语法。
- 执行固定桥契约、安全归档和部署测试。
- 部署门禁不少于 41 项，全部通过且无跳过；13 个事务阶段分别作为独立测试。
- 扫描自动 root/ADB、Accessibility/WakeLock 等禁止机制。可选 Shizuku 单独校验受保护 Provider、候选包 authority 与默认关闭；不再将明确授权的 Shizuku 模块混同于保活机制。

## Build job

- JDK 17、Gradle 9.4.1、SDK/Build Tools 37。
- `lintDebug`、`testDebugUnitTest`、Debug、Release-shaped 和 AndroidTest APK。
- JVM 报告至少 60 个唯一 testcase，全部通过且无跳过。
- APK 使用 Android Debug/Test key；逐个 `apksigner verify`，输出 SHA-256 和候选元数据。

## Emulator matrix

API 26、33、34、35、37 各执行一次 `connectedDebugAndroidTest`，不以重跑转绿。每个 API 必须：

- 真实 `ro.build.version.sdk` 与矩阵一致；
- 至少 13 个唯一仪器 testcase 全部通过且无跳过；
- 23 张规定 PNG 均存在、PNG/IHDR 有效且尺寸非零；
- 覆盖全部页面、空态、错误态、深色、1.3× 字号、横屏、展开宽度、紧凑密度、OAuth discovery、Keystore 本机配对和 Fixture 写隔离。

## Not proven by CI

- 真实 ARM64 Termux、Debian PRoot、RUN_COMMAND 和 Core。
- 发布者签名清单/固定公钥以及真实 install/update/rollback。
- 实际浏览器 OAuth、SAF 文档提供方和真实 Core 管理副作用。
- 屏幕关闭、Doze、厂商进程策略、重启及 30 分钟/2 小时/8 小时运行。
- 生产签名、发布、真实安装或生产环境修改。

## Android tool upgrade regression additions

- Termux 官方成功码、早期 ACK、迟到/重复回执和截断长度类型。
- Core 执行租约、节点身份、输出游标、输入序号、失联未知状态及原调用取消。
- Shizuku 绑定代次、协议参数、原生 PTY、stdin、独立进程组、远端期限及输出上限。模拟器在普通应用 UID 下执行真实 native process 测试，不能记作真实 Shizuku 授权。
- 每项结构化设备动作的闭合 Schema、参数引用、读写分离、生成截图路径、原 Call/Session 归属和取消回执。
- 诊断报告只复制允许字段，凭据、命令、环境变量和原始错误不得进入报告。
- 真机单列：授权、release-shaped UserService 绑定、OPPO ARM64 操作、手机重启后的 Shizuku 恢复及移除电脑/ADB中继后的运行。没有真实执行时保留未验收。
