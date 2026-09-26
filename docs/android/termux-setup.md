# External Termux and Debian setup

本任务只实现与测试候选，不执行以下真实安装步骤。

## User-controlled setup

1. 在“安装与更新”导出完整三文件桥包：`agentdock-workbench`、`agentdock_workbench.py`、`agentdock-workbench-bootstrap.sh`。
2. 用户在官方外部 Termux 中手动运行 bootstrap。它安装 `proot-distro/curl/jq/coreutils/util-linux/procps/openssl-tool/tar/python`，部署固定桥并安装 Debian 依赖；不会安装 Core。
3. 用户在 Android 设置中授予 `com.termux.permission.RUN_COMMAND`。
4. Workbench 先执行只读 `probe/status`；这些操作不会创建节点目录或凭据。
5. `bootstrap` 初始化私有节点目录与身份材料。
6. 主线提供固定受信公钥和签名 ARM64 清单后，才可使用 install/update。缺失时返回 `pending_manifest`。

## Pairing

- 本机节点：Android 生成一次性 Keystore RSA 公钥，经固定桥获取 OAEP 密文并在应用内解密；Bearer 明文不经过 Intent、参数、日志或 journal。
- 远程节点：使用 Core discovery、动态注册、PKCE S256、随机 state 和 127.0.0.1 临时 callback；访问令牌绑定远程 Origin。
- 手动 Bearer 仍作为显式恢复入口。本机删除不等于服务器撤销。

## Deployment and recovery

install/update 先完成签名、平台、版本、摘要和归档验证，再进入维护窗口。事务保存 source/target、阶段、快照和结果，可按原 operation ID 查询、续接或取消。健康检查失败时恢复旧版本和旧数据并保留失败数据。rollback 必须明确确认数据恢复。cleanup 必须先获取预览摘要再确认。

## Existing node

现有节点只读发现，不自动接管。adopt 要求兼容受管布局、Termux home 内路径、明确确认和可验证进程身份；不会替换程序、配置或身份。
