# Android security model

## Credentials and pairing

- Core Bearer/OAuth 会话使用 Android Keystore AES-GCM 包封，密文记录绑定精确 Origin。
- 凭据不进入 DataStore、日志、Intent 参数、RUN_COMMAND 参数、操作摘要、备份或设备迁移。
- 本机配对为一次性 Keystore RSA-2048 OAEP（SHA-256/MGF1-SHA1）；操作失败、超时和应用启动清理临时密钥。
- Termux 只在即时回执中返回 OAEP 密文，持久 journal 只记录 `ciphertext_delivered=true`。
- 远程 OAuth 要求同源 issuer/端点、动态注册、公共客户端 `none`、授权码、PKCE S256、资源指示器、Bearer header 和精确 loopback redirect。
- state、verifier、授权码、访问令牌和 callback 均有长度、字符、超时和重放边界。刷新令牌和客户端秘密不持久化。

## Network policy

- 明文 HTTP 只允许 `localhost`、`127.0.0.1`、`::1` 和完整 IPv6 loopback；其余地址必须显式启用 HTTPS。
- Android Network Security Config 默认拒绝明文，只为上述四个字面主机建立不含子域的例外；Manifest 不设置全局明文开关。
- Origin 不得含 path、query、fragment 或 userinfo。
- 系统代理和 HTTP 重定向被拒绝。
- 普通管理读取只允许 `/internal/runtime/`；公开例外仅为两个精确 OAuth discovery 路径。

## Release trust

真实部署要求固定受信 Ed25519 公钥、已签名 Linux/ARM64 清单、资产 SHA-256、受限归档结构、Core 自报版本和启动后管理鉴权。主线当前只有归档摘要，没有 WB07 要求的发布者签名清单，因此真实 install/update 返回 `pending_manifest`，且不下载 Core。

## Process and filesystem boundary

- 进程记录绑定 PID、session/group、启动时间和 boot ID；未知身份不收信号。
- 归档拒绝遍历、绝对路径、反斜线、链接、特殊文件、重复成员、文件/目录冲突和超限展开。
- 数据快照包含非跟随清单和树摘要；损坏快照不恢复。
- SAF 与 Termux 路径分别授权；APK 不申请广域共享存储权限。
- 基础 Core、部署和守护不依赖 root、ADB 或 Shizuku。可选 Shizuku 系统执行使用显式授权、受保护 Provider、独立 UserService 与既有 Core 权限；没有无线 ADB 配对实现或隐式提权回退。
- 不使用 Accessibility、悬浮窗、静音音频、WakeLock 循环或一分钟精确闹钟保活。Shizuku 不用于绕过用户停止节点的意图。
- Shizuku UserService 中的独立受管进程组具有远端期限，实际身份通过探测报告。Binder 丢失保留未知状态，详情见 shizuku-provider.md。
