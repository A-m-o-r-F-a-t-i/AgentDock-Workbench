package runtimeapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/uvwt/agentdock/internal/app"
	registry "github.com/uvwt/agentdock/internal/plugin"
	toolplugin "github.com/uvwt/agentdock/internal/tool/plugin"
)

func TestRuntimePluginConfirmationForwarding(t *testing.T) {
	tests := []struct {
		name string
		body string
		want map[string]any
	}{
		{"legacy update", `{"action":"update","name":"demo","source":"new.zip"}`, map[string]any{"action": "update", "name": "demo", "source": "new.zip"}},
		{"reviewed install", `{"action":"install","source":"new.zip","confirmed":true}`, map[string]any{"action": "install", "source": "new.zip", "confirmed": true}},
		{"reviewed update is not rebind consent", `{"action":"update","name":"demo","source":"new.zip","confirmed":true}`, map[string]any{"action": "update", "name": "demo", "source": "new.zip", "confirmed": true}},
		{"explicit source change", `{"action":"update","name":"demo","source":"new.zip","confirmed":true,"confirmed_source_change":true}`, map[string]any{"action": "update", "name": "demo", "source": "new.zip", "confirmed": true, "confirmed_source_change": true}},
		{"explicit false", `{"action":"update","name":"demo","source":"new.zip","confirmed":false,"confirmed_source_change":false}`, map[string]any{"action": "update", "name": "demo", "source": "new.zip", "confirmed": false, "confirmed_source_change": false}},
		{"null never grants consent", `{"action":"update","name":"demo","source":"new.zip","confirmed":null,"confirmed_source_change":null}`, map[string]any{"action": "update", "name": "demo", "source": "new.zip"}},
		{"ordinary member action unchanged", `{"action":"member_disable","name":"demo","member_type":"skill","member":"reader"}`, map[string]any{"action": "member_disable", "name": "demo", "member_type": "skill", "member": "reader"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &runtimeStub{}
			_, err := Dispatch(context.Background(), runtime, Request{Method: "POST", Path: "/internal/runtime/plugins", Body: []byte(tt.body)})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(runtime.pluginArgs, tt.want) {
				t.Fatalf("args = %#v, want %#v", runtime.pluginArgs, tt.want)
			}
		})
	}
}

func TestRuntimePluginConfirmationKeepsRequestBoundary(t *testing.T) {
	bodies := []string{
		`{"action":"update","name":"demo","source":"new.zip","confirmed":"true"}`,
		`{"action":"update","name":"demo","source":"new.zip","confirmed_source_change":1}`,
		`{"action":"update","name":"demo","source":"new.zip","confirmed_source_change":{}}`,
		`{"action":"update","name":"demo","source":"new.zip","confirmed_source_change":true,"unknown":true}`,
		`{"action":"update","name":"demo","source":"new.zip","task_id":"tsk_forged"}`,
		`{"action":"update","name":"demo","source":"new.zip","conversation_id":"conv_forged"}`,
		`{"action":"update","name":"demo","source":"new.zip","confirmed_source_change":true} {}`,
		strings.Repeat(" ", 64*1024) + `{"action":"update","name":"demo","source":"new.zip","confirmed_source_change":true}`,
	}
	for i, body := range bodies {
		runtime := &runtimeStub{}
		_, err := Dispatch(context.Background(), runtime, Request{Method: "POST", Path: "/internal/runtime/plugins", Body: []byte(body)})
		var toolErr *app.ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != "INVALID_PLUGIN_REQUEST" {
			t.Fatalf("case %d: error = %v", i, err)
		}
		if runtime.pluginArgs != nil {
			t.Fatalf("case %d reached the mutation handler", i)
		}
	}
}

func TestRuntimePluginConfirmationMatchesNativeToolContract(t *testing.T) {
	schema, ok := toolplugin.InputSchema(toolplugin.ToolManage)
	if !ok {
		t.Fatal("native plugin contract missing")
	}
	properties := schema["properties"].(map[string]any)
	for _, key := range []string{"confirmed", "confirmed_source_change"} {
		property, ok := properties[key].(map[string]any)
		if !ok || property["type"] != "boolean" {
			t.Fatalf("native property %s = %#v", key, properties[key])
		}
	}
	args, err := decodeRuntimePluginRequest([]byte(`{"action":"update","name":"demo","source":"new.zip","confirmed":true,"confirmed_source_change":true}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	var native toolplugin.ManageRequest
	if err = json.Unmarshal(encoded, &native); err != nil {
		t.Fatal(err)
	}
	if !native.Confirmed || !native.ConfirmedSourceChange {
		t.Fatalf("confirmation lost during native request conversion: %#v", native)
	}
}

type pluginConfirmationRuntime struct {
	runtimeStub
	service *toolplugin.Service
}

func (r *pluginConfirmationRuntime) RuntimePluginManage(ctx context.Context, args map[string]any) (app.Result, error) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var request toolplugin.ManageRequest
	if err = json.Unmarshal(encoded, &request); err != nil {
		return nil, err
	}
	result, err := r.service.Manage(ctx, request)
	return app.Result(result), err
}

func confirmationPackage(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	manifest := map[string]any{"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "confirmation-demo", "version": version}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "plugin.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRuntimePluginSourceChangeRequiresExplicitConsent(t *testing.T) {
	store, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runtime := &pluginConfirmationRuntime{service: toolplugin.New(store,
		func(string) (toolplugin.SkillItem, bool, error) { return toolplugin.SkillItem{}, false, nil },
		func(context.Context, string, bool) (toolplugin.MCPItem, bool, error) {
			return toolplugin.MCPItem{}, false, nil
		})}
	dispatch := func(args map[string]any) (app.Result, error) {
		body, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		return Dispatch(context.Background(), runtime, Request{Method: "POST", Path: "/internal/runtime/plugins", Body: body})
	}
	oldSource, newSource := confirmationPackage(t, "1.0.0"), confirmationPackage(t, "1.1.0")
	if _, err = dispatch(map[string]any{"action": "install", "source": oldSource}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetEnabled("confirmation-demo", false); err != nil {
		t.Fatal(err)
	}
	old, err := store.Get("confirmation-demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{
		{"action": "validate", "source": newSource},
		{"action": "update", "name": "confirmation-demo", "source": newSource},
		{"action": "update", "name": "confirmation-demo", "source": newSource, "confirmed": true},
		{"action": "update", "name": "confirmation-demo", "source": newSource, "confirmed_source_change": false},
	} {
		_, err = dispatch(args)
		if args["action"] == "validate" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "confirmed_source_change") {
			t.Fatalf("unconfirmed rebind should remain rejected: %v", err)
		}
		actual, readErr := store.Get("confirmation-demo")
		if readErr != nil {
			t.Fatal(readErr)
		}
		if actual.Version != "1.0.0" || actual.Enabled || !reflect.DeepEqual(actual.Source, old.Source) {
			t.Fatalf("review/rejected update changed installed state: %#v", actual)
		}
	}
	if _, err = dispatch(map[string]any{"action": "update", "name": "confirmation-demo", "source": newSource, "confirmed": true, "confirmed_source_change": true}); err != nil {
		t.Fatal(err)
	}
	actual, err := store.Get("confirmation-demo")
	if err != nil {
		t.Fatal(err)
	}
	if actual.Version != "1.1.0" || actual.Enabled || reflect.DeepEqual(actual.Source, old.Source) {
		t.Fatalf("confirmed update failed or changed enabled state: %#v", actual)
	}
	bytes, err := os.ReadFile(filepath.Join(actual.Path, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var installed map[string]any
	if err = json.Unmarshal(bytes, &installed); err != nil {
		t.Fatal(err)
	}
	if installed["version"] != "1.1.0" {
		t.Fatalf("actual package contents differ: %#v", installed)
	}
}
