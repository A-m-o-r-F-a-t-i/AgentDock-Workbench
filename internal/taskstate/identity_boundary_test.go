package taskstate

import (
	"encoding/json"
	"testing"
)

func TestSecurityTaskIdentityMustMatchStorageName(t *testing.T) {
	data, err := json.Marshal(Task{SchemaVersion: SchemaVersion, ID: "tsk_2222222222222222"})
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"tsk_1111111111111111", "tsk_1111111111111111.json"} {
		if _, err := decodeTask(data, label); err == nil {
			t.Errorf("accepted task identity belonging to another storage name: %s", label)
		}
	}
}
