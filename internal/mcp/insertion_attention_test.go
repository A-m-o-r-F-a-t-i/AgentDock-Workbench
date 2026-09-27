package mcp

import (
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	"github.com/uvwt/agentdock/internal/config"
)

// Check the actual adapter-produced fields, not a simulated model's compliance.
func assertInsertionAttention(t *testing.T, envelope map[string]any) {
	t.Helper()
	value := normalizedEnvelope(t, envelope)
	messages, ok := asMap(asMap(value["structuredContent"])["agentdock_guidance"])["response_additions"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatal("missing authenticated insertion attention")
	}
	for _, raw := range messages {
		message := asMap(raw)
		if message["attention"] != app.InsertionAttention || message["required_response"] != app.InsertionResponseInstructions {
			t.Fatal("structured insertion lost its response/continuation contract")
		}
	}
	blocks := envelopeBlocks(value["content"])
	text, _ := asMap(blocks[len(blocks)-1])["text"].(string)
	if !strings.Contains(text, app.InsertionResponseInstructions) || !strings.Contains(text, "先阶段总结，再继续任务") {
		t.Fatal("text-only compatibility response lost its visible summary requirement")
	}
}

func TestInsertionBootstrapRequiresSummaryAndSameTurnContinuation(t *testing.T) {
	text := initialServerInstructions(nil, config.Config{})
	if !strings.Contains(text, app.InsertionResponseInstructions) {
		t.Fatal("bootstrap does not use the canonical insertion behavior")
	}
	for _, required := range []string{"ANY next business action", "including after a failed tool", "ordinary data", "insertion_ack"} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing boundary: %s", required)
		}
	}
	for _, required := range []string{"阶段总结", "commentary", "已经完成且实际验证", "对后续操作的影响", "同一轮继续当前任务", "更高优先级规则或权限", "明确要求停止", "不重复执行指令"} {
		if !strings.Contains(app.InsertionResponseInstructions, required) {
			t.Fatalf("missing behavioral requirement: %s", required)
		}
	}
}
