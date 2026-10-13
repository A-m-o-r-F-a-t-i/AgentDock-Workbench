package client

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uvwt/agentdock/internal/activity"
)

// conversationMetadata forwards only the immutable, validated ingress scope.
// Correlation never grants authorization. Business arguments and task threads
// cannot replace the host conversation identity.
func conversationMetadata(ctx context.Context) mcpsdk.Meta {
	scope := activity.ExecutionScopeFromContext(ctx)
	if scope.ConversationID == "" || scope.Validate() != nil {
		return nil
	}
	switch scope.BindingQuality {
	case "host_metadata", "connection_fallback":
		// connection_fallback exists only for adapters guaranteeing one conversation
		// per connection; generic transport sessions remain unattributed upstream.
		return mcpsdk.Meta{"agentdock/conversation-id": scope.ConversationID}
	default:
		return nil
	}
}
