# ACTOVO for Android

WB07 提供 Kotlin + Jetpack Compose 原生 Workbench，并把 Linux Core 保持在外部 Termux 的 Debian PRoot 中。APK 不内嵌 Termux、RootFS、Chromium、Go Core、ADB、root、Shizuku 或无障碍自动化。

```text
Android Workbench APK
  ├─ 完整移动端管理界面
  ├─ Core HTTP/SSE 与 OAuth/PKCE 客户端
  ├─ Android Keystore 凭据与一次性本机配对
  └─ Termux RUN_COMMAND 固定桥
          ↓ 用户明确授权
外部 Termux → Debian PRoot → Linux ARM64 ACTOVO Core
```

## 候选范围

- 16 个 Workbench 目的页全部保留，紧凑布局只改变导航形式，不删除功能。
- 工作区、任务、线程、对话绑定、调用树与历史、审批、权限、Skill、插件、MCP、项目文件、日志与设置均接入共享 Core 契约。
- 本机 Core 使用一次性 Android Keystore RSA 公钥配对；远程 Core 使用动态注册、PKCE S256、随机 state 和 loopback 回调。Bearer/OAuth 会话按精确 Origin 加密绑定。
- Termux 部署采用固定 Ed25519 公钥、签名 ARM64 Core 清单、持久事务日志、断点续接、一致性数据快照、健康验证、自动回退、单恢复点保留、指数退避和熔断。
- SAF ZIP 导入导出与 Termux 路径探针分别授权，不把 Android URI 猜成 Linux 路径。
- 候选 APK 只在 GitHub Actions 生成并使用测试签名，不进入 Release。
- 公开 APK 在发布阶段使用固定证书重签；证书指纹、Actions Secret 和轮换要求见 [Android 发布签名](release-signing.md)。

## 验收边界

GitHub Actions 可验证源码契约、JVM 测试、59 项部署故障注入、Android 仪器测试、API 26/33/34/35/37、规定截图以及 Core 发布资产签名绑定。它不能替代真实 ARM64 Termux、真实 Core/OAuth、Doze/厂商保活和长时真机运行。

公开 Android Release 同时提供 Linux ARM64 Core 归档、部署清单和 Ed25519 签名。五文件桥包安装固定信任公钥；应用只在签名、架构、版本、摘要、归档结构和 Core 身份全部一致时进入安装事务。真实设备的安装、更新和回退继续单独验收。
