# Agent 命令工具增量修复

日期：2026-09-28。基线：`5e9b092ea0112785c2324ba1e428b4db9a60419f`，即 v1.1.8 加既有上下文传输修复。开发与本地验证在 OPPO 的独立工作树进行，分支为 `fix/20260928-context-transport`。

## 已复现的问题与修复

### 1. 请求已经取消或准备已经超时，仍然启动命令

`Service.Exec` 在完成 Skill/工作目录/环境准备后只检查返回错误，没有检查准备上下文的状态。准备函数在取消边界返回有效租约时，后续从独立的命令上下文启动进程，因此准备前取消、准备中取消和准备超时均可能真实执行命令。

新增用例在原实现上均复现了 `late-start.txt` 被实际创建。修复在入口、准备完成和最终启动前检查取消；准备上下文的错误在主动释放它之前读取；成功获取但不再可派发的 Skill 租约仍被正确释放，未启动的 reservation/start 计数归还。

已经启动的进程仍使用独立生命周期。新增文件屏障测试分别覆盖 async、auto、sync：确认进程启动后才取消请求，再解除屏障，命令正常执行到终点。没有把 HTTP/MCP 请求结束改成杀死长命令。

### 2. session_act write 吞掉输入管道关闭错误

运行中会话的输入写入返回 `io.ErrClosedPipe` 或 `os.ErrClosed` 时，原实现会返回正常的 running 结果，即使输入没有送达。现在运行中写入的失败会以保留原始错误链的错误返回。写入期间进程已完成时仍返回实际终态，不自动重试输入。

### 3. 停止或写入操作消费最终输出

运行中的普通写入使用 Peek，但已结束会话的写入/停止与运行中停止的最终结果使用 Snapshot，提前推进观察游标，使之后的 session_observe status 得不到尚未观察的 stdout/stderr。

修复沿用同一终态结果构造函数，明确区分预览和消费：mutation 只 Peek，status 才推进游标。真实子进程回归覆盖已完成写入、已完成停止、运行中停止，同时检查下一次 status 得到完整待读输出、再下一次只返回空增量。

### 4. 异常完成仍标记 command_ok=true

原成功条件仅检查退出码 0 和没有超时。对于被终止但退出码为 0、Wait 返回 I/O 错误但退出码为 0，以及未完成的内部快照，可能产生错误成功标志。

成功现在同时要求：已完成、退出码 0、Wait 无错误、未超时且未请求终止。没有伪造新的退出码或覆盖原错误。

## 测试与验证

原实现上的首轮复现包含 4 个顶层测试、15 个子场景：11 个缺陷场景失败，4 个对照场景通过。原始日志保存为 `before-fix.jsonl`。

修复后新增 3 个已启动命令存活场景，共 5 个顶层测试、18 个子场景。手机端使用缓存 Go 1.26.5 运行 3 轮，15 次顶层执行、54 次子场景执行全部通过。日志为 `after-fix.jsonl`。

```sh
go test -p 2 ./internal/tool/command/... -run '^TestAgentTools' -count=3 -timeout=120s
```

完整受影响套件 `go test -p 2 ./internal/tool/command/... ./internal/app ./internal/mcp -count=1 -timeout=8m` 已通过：312 个顶层测试、176 个子测试通过，2 个顶层测试跳过。四个包均成功，日志为 `full-affected.jsonl`。同范围 `go vet -p 2` 已通过。

手机端 race 二进制能够构建，但 ThreadSanitizer 在启动时报告 `unsupported VMA range`（实际 39、支持 48），未进入测试。因此本地 race 结果属于环境不支持，不能计为测试通过；详细信息保存在 `race.jsonl`，标准 Linux CI 单独验证这一项。

本地证据目录：

```text
/storage/emulated/0/Termux/AgentDock/Artifacts/context-transport/tsk_0b2a4a426d540914/
```

现有 `context-transport.yml` 增加命令回归、完整命令包、Linux race 与 vet，沿用 Windows、Linux amd64、Linux arm64 三平台矩阵及 Windows 纯模型测试。完整包和 CI 的执行状态以本轮实际日志、精确提交对应的 Actions 结果为准，不沿用上一轮提交的绿色结果。

## 边界

本轮未启动安装器或桌面程序，未升级、重启或修改手机生产 Core，也未修改 Release、主分支或上游仓库。生产手机报告版本仍为 1.1.6；源码修复与生产部署是不同交付状态。

手机系统 `/usr/bin/go` 在版本检查时发生段错误，本轮使用已缓存且实际验证可工作的 Go 1.26.5。该环境问题没有作为 AgentDock 源码缺陷处理。
