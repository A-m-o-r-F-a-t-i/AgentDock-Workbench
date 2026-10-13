# Downstream MCP conversation metadata

Dynamic MCP tool forwarding adds agentdock/conversation-id to tools/call._meta using the immutable, validated activity.ExecutionScope. Known host metadata and adapter-guaranteed one-conversation connections are accepted. Unattributed, invalid and task-only scopes emit no conversation metadata. No identifier is read from tool arguments, and third-party arguments and schemas are unchanged.

The correlation identifier is not an authorization token. Native targets, authenticated ownership and permissions remain the receiving service's responsibility. Task threads are not host conversations and are not forwarded as agent identities. Independent sub-agents sharing a conversation must use explicit target overrides where needed.

The same call path covers HTTP and stdio and preserves SDK progress metadata. Regression tests capture actual HTTP JSON-RPC requests, parallel conversations, reconnection, missing identity and argument spoof attempts. This change retains the existing AgentDock version and tool catalog.
