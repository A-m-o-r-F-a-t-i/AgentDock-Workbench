# Windows 启动目录权限

AgentDockHome 是由运行时管理的私有状态目录。现有默认工作区属于用户项目目录，启动时保留它及其子项的权限；由 AgentDock 新建的工作区仍设置私有权限。Windows 私有目录的目标 DACL 继续只允许当前用户、SYSTEM 和本地管理员完全访问。

`EnsurePrivate` 每次读取实际 DACL，严格比较保护标志、ACE 顺序、权限掩码与继承标志。权限一致时不再调用 `SetNamedSecurityInfo`，避免 Windows 将相同继承权限重复传播到已有子项。读取失败或权限发生变化时仍执行原有设置和错误处理，不使用缓存标记替代权限检查。

Core 日志在环境恢复、配置规范化和运行时初始化三个阶段记录开始、完成状态及单调时钟耗时。初始化失败仍由原调用方返回原始错误，阶段记录不复制配置值或凭据。Windows 服务健康等待保持 120 秒，安装事务和失败回滚不变。

## 验证

Windows 回归覆盖现有工作区及子文件权限保持、新建目录保护、相同 DACL 的只读重复调用、权限变化修复以及 NULL/未保护/额外授权/不同继承规则的拒绝。Unix 回归覆盖现有共享工作区权限保持和新建私有目录。所有权限写入测试仅操作临时目录。

```powershell
go test ./internal/config ./internal/fs/securepath -count=1
go test ./cmd/agentdock -run '^TestStartupTrace' -count=1
go test ./internal/fs/securepath -run '^$' -bench '^BenchmarkPrivateDACLExistingTree$' -benchtime=3x -count=1
```

性能对比使用同一个含 2048 个文件的临时目录，分别执行旧的无条件 DACL 设置与新的检查后跳过路径；这项数据不等同于整机安装耗时。
