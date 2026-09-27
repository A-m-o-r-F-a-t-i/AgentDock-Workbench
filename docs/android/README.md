# AgentDock Workbench for Android

WB07 提供 Kotlin + Jetpack Compose 原生 Workbench，并把 Linux Core 保持在外部 Termux 的 Debian PRoot 中。APK 不内嵌 Termux、RootFS、Chromium、Go Core、ADB、root、Shizuku 或无障碍自动化。

```text
Android Workbench APK
  ├─ 完整移动端管理界面
  ├─ Core HTTP/SSE 与 OAuth/PKCE 客户端
  ├─ Android Keystore 凭据与一次性本机配对
  └─ Termux RUN_COMMAND 固定桥
          ↓ 用户明确授权
外部 Termux → Debian PRoot → Linux ARM64 AgentDock Core
```

## 候选范围

- 16 个 Workbench 目的页全部保留，紧凑布局只改变导航形式，不删除功能。
- 工作区、任务、线程、对话绑定、调用树与历史、审批、权限、Skill、插件、MCP、项目文件、日志与设置均接入共享 Core 契约。
- 本机 Core 使用一次性 Android Keystore RSA 公钥配对；远程 Core 使用动态注册、PKCE S256、随机 state 和 loopback 回调。Bearer/OAuth 会话按精确 Origin 加密绑定。
- Termux 部署采用持久事务日志、签名清单门禁、断点续接、一致性数据快照、健康验证、自动回退、单恢复点保留、指数退避和熔断。
- SAF ZIP 导入导出与 Termux 路径探针分别授权，不把 Android URI 猜成 Linux 路径。
- 候选 APK 只在 GitHub Actions 生成并使用测试签名，不进入 Release。

## 验收边界

GitHub Actions 可验证源码契约、60 项 JVM 测试、41 项部署故障注入、13 项 Android 仪器测试、API 26/33/34/35/37 和 23 张规定截图。它不能替代真实 ARM64 Termux、真实 Core/OAuth、Doze/厂商保活和 30 分钟/2 小时/8 小时真机运行。

主线当前提供 Linux ARM64 归档与 SHA-256，但没有 WB07 所需的发布者签名清单和固定信任公钥。因此真实 install/update 保持 `pending_manifest`，不会下载未认证 Core。本候选不进行真实安装。
