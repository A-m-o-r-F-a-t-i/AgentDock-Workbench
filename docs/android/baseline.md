# WB07 baseline

| 项目 | 基线 |
| --- | --- |
| 产品名 | ACTOVO |
| Android UI | Kotlin + Jetpack Compose |
| min / target / compile SDK | 26 / 37 / 37 |
| Java | 17 |
| Core | 外部 Termux + Debian PRoot 中的 Linux ARM64 Core |
| 数据权威 | 现有 ACTOVO Core 域服务 |
| 构建环境 | GitHub Actions；手机工作区只编辑与静态检查 |
| 候选签名 | Android Debug/Test key，仅候选证据 |
| 真实安装 | 本任务禁止 |

## 验收基线

- 60 项 JVM 测试。
- 59 项部署测试，其中 13 项逐阶段中断恢复。
- 13 项 Android 仪器测试。
- API 26、33、34、35、37。
- 每个 API 23 张规定截图。
- 所有证据绑定精确 source SHA、run ID 与 attempt。

公开 Android Release 使用独立 Ed25519 身份签署 Linux ARM64 Core 部署清单；固定公钥进入五文件 Termux 桥包，归档、清单和签名必须绑定同一版本与提交。设备上的实际 install/update/rollback 仍作为独立真机验收。
