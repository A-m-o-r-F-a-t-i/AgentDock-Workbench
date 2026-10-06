# Computer Use 显式路由

## 适用边界

Computer Use 插件只在任务明确要求网页或 Windows 桌面交互时进入模型工具上下文。代码、文件、Git、终端、PCB、API、纯文本及普通 MCP 任务不直接暴露该插件的 Skill 和 MCP 成员，也不参与无范围动态工具搜索。

显式交互任务按以下顺序选择能力：

```text
current-browser-control
  -> cua-driver
  -> chatgpt-native-computer-use
```

`cua-driver:launch_app` 只用于用户任务明确要求启动尚未运行的 Windows 应用或打开 URL。能力探测、会话恢复、查找已有窗口、获取 PID 和通用任务初始化不得调用该工具。

## 调用门槛

模型先通过 `plugin_load("computer-use")` 取得成员和路由说明。执行该插件拥有的动态 MCP 工具时，`mcp_tool_call` 必须额外提供：

```json
{
  "interaction_intent": "desktop_gui",
  "reason": "与当前用户任务直接对应的具体 GUI 操作原因"
}
```

缺少任一字段时，AgentDock 在请求转发至第三方 MCP 前返回 `MCP_EXPLICIT_INTENT_REQUIRED`。其他动态 MCP 工具保持原调用契约。

## 验收

- 默认 `agentdock_context` 只显示 `computer-use` 插件摘要，不显示其 Skill 和 MCP 成员。
- 无范围 `mcp_tool_search` 不返回 Computer Use 工具。
- `plugin_load("computer-use")` 和指定服务器的目录读取仍可用。
- `cua-driver:launch_app` 的所有目录与 Schema 描述均先显示适用边界。
- 缺少显式 GUI 意图的业务调用不会到达上游 MCP；带有效意图与具体原因的调用保持可用。
