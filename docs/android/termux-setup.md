# External Termux and Debian setup

Android Workbench 使用外部 Termux 和 Debian PRoot 承载本机 Linux ARM64 Core。

## User-controlled setup

1. 在“安装与更新”导出完整五文件桥包：`agentdock-workbench`、`agentdock_workbench.py`、`agentdock_host_executor.py`、`agentdock-workbench-bootstrap.sh`、`agentdock-core-arm64.tar.gz`。最后一项来自当前 APK 内置 Core。
2. 用户在官方外部 Termux 中手动运行 bootstrap。它安装 `proot-distro/jq/coreutils/util-linux/procps/tar/python`，部署固定桥、安装 Debian 依赖，并把内置 Core 以 `0600` 写入 Termux 私有目录。
3. 用户在 Android 设置中授予 `com.termux.permission.RUN_COMMAND`。
4. Workbench 先执行只读 `probe/status`；这些操作不会创建节点目录或凭据。
5. `bootstrap` 初始化私有节点目录与身份材料。
6. 在“安装与更新”执行 install/update。部署事务只读取桥包导入的本地 Core，不访问额外 Release 资产；内置 Core 缺失、归档结构错误或自报版本与 APK 不一致时保持原版本不变。

## Pairing

- 本机节点：Android 生成一次性 Keystore RSA 公钥，经固定桥获取 OAEP 密文并在应用内解密；Bearer 明文不经过 Intent、参数、日志或 journal。
- 远程节点：使用 Core discovery、动态注册、PKCE S256、随机 state 和 127.0.0.1 临时 callback；访问令牌绑定远程 Origin。
- 手动 Bearer 仍作为显式恢复入口。本机删除不等于服务器撤销。

## Deployment and recovery

install/update 先核对 APK 版本、内置归档摘要、安全解包结构、可执行文件与 Core 自报版本，再进入维护窗口。事务保存 source/target、阶段、快照和结果，可按原 operation ID 查询、续接或取消。健康检查失败时恢复旧版本和旧数据并保留失败数据。rollback 必须明确确认数据恢复。cleanup 必须先获取预览摘要再确认。

## Existing node

现有节点只读发现，不自动接管。adopt 要求兼容受管布局、Termux home 内路径、明确确认和可验证进程身份；不会替换程序、配置或身份。
