# Core 合并后的发行验证与原品牌恢复

本文接续 [Core 集成质量验证快照](core-integration-quality.md)，记录功能和质量 PR 合并后发现的实际问题，以及恢复原品牌的边界。构建、安装、发布分别验收；历史失败和跳过保留其原始来源。

## 1. 已完成的 Core 质量合并

功能 PR #126、#140 以及本轮其他 Core 功能合并后，单独建立的 [质量 PR #143](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/pull/143) 已于 2026-10-17 13:32:49 UTC 合并。

| 核对项 | 实际结果 |
| --- | --- |
| 最终质量 head | `21e777da70c50279c4284a93125ebf9de630d03a` |
| 合并提交 | `2ea6d7d6ad71f79ae2fb09cd7843172e1f84068d` |
| 合并树 | `fd2c8d720fe3299c934033f728a1707b429f0352`，与已验证质量 head 完全一致 |
| 最终 PR 检查 | 35 项：33 success、2 既有条件 skipped；9 个工作流成功 |
| 原生 Windows 全仓 | `go test -count=1 -json -timeout=8m -p=2 ./...`，退出 0，66 包 PASS、8 包无测试 SKIP。该本地命令使用进程范围 `GOFLAGS=-buildvcs=false`；Actions 使用正常 checkout。121 个平台或可选条件的用例 SKIP 未计为覆盖 |

Windows Snapshot 第一次执行因 20 分钟作业预算取消，原始失败记录保留；未取得其具体停滞测试或调用栈，不能声称已确定唯一根因。同一 head、断言、包集合和预算的一次复验成功，全部六包、race、vet/build 和桌面回归均完成。最终成功不改写第一次取消的事实。

质量修复继续由既有 owner 负责：Canonical Result 与完整来源先于模型投影，Context 内容 revision 与缓存重新验证资格分开，Plugin generation/lease 保护进行中的调用，原 Activity 恢复保持一次性约束，别名读取屏障服从取消与既有目录生命周期。品牌恢复不撤销这些实现。

## 2. 最终平台门禁暴露的容量夹具问题

[构建 39037158169](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/actions/runs/39037158169) 绑定 `2ea6d7d`，没有启用 Intel 性能阈值例外。两个 macOS 架构均在 `TestScopedWatchBoundsSelectedDirectories` 报告：

```text
selected=2048 handles=3 healthy=false
```

scope 上限及超限不健康状态符合原合同。失败项是夹具要求一个目录登记，而未规范化的系统路径别名使现有 owner 还需要监听别名父目录与目标父目录。用例没有调用 `Sync`，耗时也不是它的失败断言。

独立 Linux overlay 用真实目录和符号链接模拟相同拓扑：选择次数为 1、1024、2048 时，raw alias 的登记数始终为 3，canonical 路径始终为 1；第 2049 和 2050 次选择后，两者均保留 2048 个 scope、不健康状态，并逐次推进 source。完整归因命令退出 0，snapshot 10.180s。它证明登记数有界，不冒充 macOS 原生执行。

[PR #144](https://github.com/A-m-o-r-F-a-t-i/AgentDock-Workbench/pull/144) 的修复仅规范化容量夹具的两个临时 Home，并把 macOS ARM64 和 Intel 加入现有 Snapshot PR 矩阵。`handles == 1`、`selected == 2048`、超限失效、后续淘汰推进 revision 和 covered-descendants 稳定性断言完整保留。生产 watcher 和容量预算没有改变，别名/junction/改指回归仍独立执行。

冻结修复在原生 Windows 完整 Snapshot 包退出 0，36.249s，44 个用例终态 PASS、2 个符号链接权限 SKIP；容量用例实际通过。PR head `2dcaf2d72118ea1908e40c5350b0fd2a4d4164a6` 的五个平台 Snapshot 作业全部成功，包含全部六个受影响包及原有 race 检查。最终 28 项 PR 检查为 26 success、2 既有 fork 条件 skipped，5 个工作流成功，包含 Windows PowerShell 5.1 与 v1.1.0–v1.1.9 历史 Setup 升级。跳过项不计覆盖。

PR #144 于 14:17:30 UTC 合并为 `8d517f5171b2e025ca86f66f65e4bbfc30e2898b`，合并树 `b9dc9da505e66b3d09d45d8e1abb61ff4b7c8459` 与已验证 head 相同。原品牌恢复在独立工作树从此提交继续；新的发行来源在全部恢复修改合并后重新确定。

## 3. 原名称、图标与兼容边界

用户随后要求撤回改名与图标变更，保留 AgentDock 原名称，并将本轮作为 **1.1.9 正式版**发布。改名来自 PR #133，合并提交为 `d43a6c63914094addeb881fcb09251bf5b3b13b2`，第一父提交为 `18510746f92e16dad8fa83fd9304a146e3e06e50`。恢复依据是这两个提交的品牌差异，以及其后真实功能变更。

未合并的旧改名 PR #129 与图标 PR #131 已于 14:00 UTC 撤回，分支保留。PR #142 是设备内置工具与动态 MCP 工具域边界，与品牌无关，其实现完整保留。

当前图标与改名前相同，且没有包含 #131 的新设计：

| 原资源 | SHA-256 |
| --- | --- |
| `packaging/assets/agentdock.png`、`docs/assets/agentdock-logo.png` | `15fe167aa395b2b63ad0998041ae7336bf0e533c82f54326cafde441318f0f80` |
| `packaging/windows/assets/agentdock.ico` | `0b4c20e7099bb49145f4159d8c50d9aca6892f810160e5aa2831bc29de9b7058` |
| `mobile/android/app/src/main/res/drawable/ic_stat_agentdock.xml` | `fc776bdba68528411ead0e5d3847288e528d5e5fa4aa5daca9f5559799afc7c9` |

恢复范围包括原短名 AgentDock、完整产品名 AgentDock Workbench、对应安装资产名、界面和发布文案。稳定的 CLI/协议名、数据目录、Windows AppId、macOS Bundle ID、Android applicationId 与签名身份保持原值。更新器需要继续精确识别已经存在的候选品牌输出和历史安装包；兼容输入不能被全局文本替换删除，也不能成为新产物的显示名称。

恢复不能覆盖后续的 Linux Home 继承、Windows 安装事务、Patch 定位、活动技术详情或 Context/Plugin 修复。Android 1.1.9 的稳定签名和内嵌 Core 同源校验属于发行安全边界，不能与改名开关一起撤销。

## 4. 发布阶段的 Android 签名输入隔离

独立复审发布 workflow 时发现，原 build 的 `workbench-android-release-${SOURCE_RUN}` 会被既有下载模式带入 `input/unix`；正式 publication 又在 `input/android-release` 生成签名 APK、sidecar 与报告。两次合法签名的字节不同，便会触发现有 `locate` 对同名报告或 APK 的冲突拒绝。

修复由下载输入边界承担：在签名前把准确原签名目录整体移到 `input` 外，单独保存为原构建审计 artifact；正式组装仍严格扫描 `input`，只消费本 publication 的签名结果。原证据缺失、链接或审计目标已占用时停止。既有 `locate` 冲突拒绝、固定证书、原 candidate、内嵌 Core 同源、Release 替换和 Latest 校验均保留。

新增回归使用真实目录、不同 ZIP 字节与两份报告，先复现旧布局的两类冲突，再执行 workflow 的实际 Bash 片段，并由完整 assembler 确认当前签名结果被采用、原三件证据摘要未变、源 APK 与 Core 绑定一致。缺失输入、覆盖审计目录和其他 Core 载荷冲突继续拒绝。这是输入选择回归，不代表本地执行了真实 APK 签名。定向用例退出 0（0.035s），发布器完整 104 个隔离用例通过（0.870s）；最终发布还须核对实际签名与上传结果。

## 5. 正式发行的独立验收

正式发行必须来自全部修复与品牌恢复合并后的完整 main SHA。旧候选的通过项可用于定位问题，不能替代新源码的原生构建和安装结果，更不能仅重命名旧安装包并沿用其摘要。

新的完整构建需核对所有平台原生门禁、发行 manifest、逐项 SHA-256、Android 签名与内嵌 Core 来源，然后用该构建的 Windows 载荷运行隔离的只读候选验收并清理。生产安装不是隔离候选验收的一部分。

发布前重新核对已备份的 v1.1.9 Release、标签、现存资产和 Latest，按用户授权执行同版本正式发布。发布后以独立 API 读回确认准确标签、源码与构建关联、8 个公开安装资产、最终签名 APK、canonical Release 正文以及 Latest 指向。实际构建 run、发布 run、最终摘要和这些状态在完成后写入 PR 正文与最终报告；本文不提前宣称正式发布成功。
