package plugin

import (
	"encoding/json"
	"testing"
)

func TestManageInputSchemaPublishesUpdateReviewToken(t *testing.T) {
	schema, ok := InputSchema(ToolManage)
	if !ok {
		t.Fatal("plugin_manage input schema is unavailable")
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["review_token"]; !ok {
		t.Fatal("plugin_manage omitted the review_token returned by validate")
	}

	var request ManageRequest
	if err := json.Unmarshal([]byte(`{"action":"update","name":"demo","source":"new.zip","review_token":"review-v1:test","confirmed_source_change":true}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Action != "update" || request.ReviewToken != "review-v1:test" || !request.ConfirmedSourceChange {
		t.Fatalf("update authorization was not preserved: %#v", request)
	}
}
