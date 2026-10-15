// Package contextguide owns the shared bootstrap/reuse guidance.
package contextguide

const Reuse = "首次进入设备或规则作用域变化时调用 agentdock_context。必要内容按不可变快照分页，delivery.continuation_required=true 时按 next_cursor 续读；同一作用域只有一个待完成序列时空参数自动返回下一页。context_ready=true 表示本序列已完整交付，随后直接执行已加载工具。同一 revision 完成后重复调用返回 unchanged，宿主丢失上下文时使用 force_refresh=true 重新交付。绑定变更本身不要求再次初始化。运行中的命令复用原 session，动态 MCP 目录变化只刷新受影响服务；宿主工具直接从宿主命名空间调用。普通日志和查询的剩余内容可选读取，不作为下一步业务操作的强制条件。"
const Description = "Return AgentDock context as immutable required-full pages. Continue with next_cursor, or use empty arguments for the unique pending sequence. context_ready becomes true on the final page. Repeated complete revisions return unchanged; use force_refresh after host context loss. Omitted workdir inherits the conversation workspace. Host tools are routed on page one; dynamic MCP search is only for external capabilities."
