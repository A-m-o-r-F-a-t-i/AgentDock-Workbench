# Android architecture

## Product boundary

| Component | Responsibility | Trust boundary |
| --- | --- | --- |
| Android APK | Compose UI、设置、Core 客户端、凭据、生命周期和 Termux 调度 | Android 应用沙箱 |
| AgentDock Core | 任务、对话、调用、审批、权限、能力和插入的唯一权威 | Linux 进程与 Core 数据根 |
| Termux bridge | 固定操作、PRoot 生命周期、部署事务、回退和诊断 | Termux 私有 home |
| Debian PRoot | Linux ARM64 Core 运行环境 | PRoot 文件系统 |
| GitHub Actions | 编译、Lint、测试、截图、摘要与证据 | 临时 CI runner |

UI 点击不构成成功。Core 写操作以 Core 响应为准；Termux 操作以绑定 operation ID、request ID、nonce 的结构化回执为准。Android 不创建第二套任务、审批、权限或活动状态机。

## Android source areas

- `data/`：有界 HTTP/SSE、管理命令、SAF、DataStore、Keystore、OAuth/PKCE。
- `model/`：页面、快照、精确时间语义和显式 Debug Fixture。
- `termux/`：RUN_COMMAND、一次性本机配对、持久操作记录和回执验证。
- `lifecycle/`：WorkManager、可见前台守护、通知、开机检查和 Tile。
- `ui/`：响应式外壳及各域唯一页面实现。
- `mobile/android/termux/`：由用户导出到外部 Termux 的固定桥、部署模块和 bootstrap。

## Core transport and credentials

默认 Origin 为 `http://127.0.0.1:8765`。明文只允许字面 loopback；远程必须显式启用 HTTPS。客户端拒绝 Origin 路径、查询、片段、userinfo、代理和重定向，并限制请求、响应和 SSE 事件大小。

手动 Bearer 与 scheme、host、有效端口一起加密绑定。本机配对使用 Android Keystore 一次性 RSA-OAEP 公钥，Termux 仅返回密文，持久回执不保存密文。远程配对使用动态注册公共客户端、PKCE S256、随机 state、资源指示器和 `127.0.0.1` 临时回调；只保存 Origin 绑定的访问令牌，不保存客户端秘密或刷新令牌。

## Termux transport and deployment

APK 使用显式 Termux package/component、固定脚本路径、封闭操作集、五个固定参数、有界 JSON stdin 和可重复接收 ACK/最终回执的显式 PendingIntent；终态或回调过期时回收。操作摘要原子持久化，终态不可退回 running，未决记录不会为容量被清理。

部署模块在任何维护窗口前验证受信公钥、清单签名、Linux/ARM64、产品版本、SHA-256、归档结构和可执行文件版本。事务阶段写入 journal，可按原 operation ID 续接或取消。切换前停止受管进程并创建带摘要的一致性数据快照；新版本身份、管理鉴权、版本和工作目录探针失败时恢复旧指针与旧数据，并隔离失败数据。清理保护 current、验证回退点和未完成事务引用。

自动恢复仅在 desired=`running` 时启动当前版本，不执行更新；失败次数、指数退避和熔断持久化。用户停止先写 desired=`stopped`，守护不会逆转停止意图。

## Optional phone execution adapter

The explicitly enabled APK connects outbound to the paired local Core and does not follow the management UI remote selection. Local registration creates a restricted executor lease stored with Keystore. No APK network listener is introduced.

Explicit mobile backends require absolute backend-local working directories. They do not inherit PRoot runtime/Skill paths. Core commands, permissions, activity, session observation and cancellation remain authoritative. A start is offered once; uncertain delivery is observed by its original ID. Output offsets, input sequences and response revisions deduplicate retries. The Termux supervisor enforces deadlines independently and is separate from the deployment transaction bridge.


## Optional system execution and structured device tools

Shizuku is an independent opt-in backend of the same outbound execution adapter. Its protected Provider and versioned UserService perform native authorization and short control calls; independent supervised processes enforce deadlines and cancellation. The APK does not pair wireless debugging or open a public command listener. The actual UID is reported by a successful probe.

`internal/tool/androiddevice/` owns pure action validation and closed read/write schemas. `specs_android_device.go` registers the tools and reuses the command service under the original binding. Generated commands keep the structured tool name in the journal and share session ownership, stopping and asynchronous completion. System paths are never rewritten as PRoot workspaces. Capture files use explicit shared storage and are returned as paths only after their completion state is checked. See [device-tools.md](device-tools.md) and [shizuku-provider.md](shizuku-provider.md).
