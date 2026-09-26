# WB07 candidate validation plan

所有依赖解析、Android 编译、Lint、JVM 测试、仪器测试、截图和 APK 打包只在 GitHub Actions 执行。手机工作区只做源码编辑、Git、XML/Python/Shell 静态语法检查；不安装 APK、Termux 或 Core。

## Contract job

- 校验精确 source SHA 和 WB07 文件归属。
- 解析 Android XML、Python AST 和 Shell 语法。
- 执行固定桥契约、安全归档和部署测试。
- 部署门禁不少于 41 项，全部通过且无跳过；13 个事务阶段分别作为独立测试。
- 扫描 root/ADB/Shizuku/Accessibility/WakeLock 等禁止机制。

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
