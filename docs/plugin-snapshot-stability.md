# 插件快照稳定性修复

## 范围与提交策略

以当前主线 `3809da0a4bcf12e8033b9e269ada1c8167fe99ca` 为起点，在 OPPO 的独立工作树完成修复并提交 PR。只修改插件目录、AGENTS 与 Skill 快照及配套回归，不合入其他开发分支，不修改 MCP 注册，不安装或替换两端运行中的 Core，不发布 Release。

## 根因

`Files.Revision()` 在 watcher unhealthy 时将每 250 ms 改变的重验证时间桶混入源修订号。插件、规则和 Skill 构建将该值用于前后比较，因而稳定目录的扫描跨过时间边界也会返回 directory/source changed。已有三次重试无法消除这一语义错误。

## 执行计划

1. 核对远端 main、原 1.1.8 及最新开发分支的相关实现，保持本修复提交独立。
2. 将真实 source revision 与周期 cache revision 分离。一致性检查只比较 source，cache 仅控制缓存重建。插件对外发布捕获的 cache generation，使下游索引跟随重验证更新。
3. 增加虚拟时间单元测试，以及插件、AGENTS、普通 Skill、公共 Skill 的事件/时钟注入回归。验证真实变更、停用状态与请求取消仍然生效，不以忽略错误或复用旧权限状态获取成功。
4. 在 GitHub Actions 的 Windows、Linux amd64/arm64 执行故障复现、定向重复回归、受影响包测试、race、vet 和隔离构建。按精确提交读取结果，创建面向 main 的 PR。

## 验收规则

无文件写入时，跨越任意数量的重验证时间桶不能改变 source revision；不健康监视器仍应定期触发缓存重建。真实事件必须改变 source revision。重建后的插件公开 revision 必须变化，以刷新下游 Skill 数据。插件停用、目录删除、权限变更和 caller cancellation 必须保留现有检查。并发请求继续遵守原快照缓存的合并与数量上限。

## 不包含的变更

本修复不重建或重新注册 MCP，不新增 Last Known Good 权限回退，不隐藏真实目录变化。监视器初次进入 unhealthy 的具体系统原因需独立诊断；本补丁消除已确认的时间桶误判，并保留监视器异常时的磁盘重验证。
