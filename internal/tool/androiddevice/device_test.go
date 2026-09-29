package androiddevice

import (
	"encoding/json"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func ptr[T any](v T) *T { return &v }
func sample(action string) Request {
	r := Request{Action: action}
	for _, key := range Definitions[action].Required {
		switch key {
		case "package":
			r.Package = "com.example.test"
		case "component":
			r.Component = "com.example.test/.MainActivity"
		case "namespace":
			r.Namespace = "system"
		case "key":
			r.Key = "screen_brightness"
		case "value":
			r.Value = ptr("120")
		case "operation":
			r.Operation = "CAMERA"
		case "mode":
			r.Mode = "allow"
		case "permission":
			r.Permission = "android.permission.CAMERA"
		case "path":
			r.Path = "/sdcard/test package.apk"
		case "capture_dir":
			r.CaptureDir = "/storage/emulated/0/Termux/AgentDock/Captures"
		case "x":
			r.X = ptr(0)
		case "y":
			r.Y = ptr(1)
		case "x2":
			r.X2 = ptr(10)
		case "y2":
			r.Y2 = ptr(20)
		case "keycode":
			r.Keycode = ptr(4)
		case "text":
			r.Text = "hello world"
		}
	}
	return r
}
func compileSchema(t *testing.T, tool string) *jsonschema.Schema {
	t.Helper()
	schema, _ := InputSchema(tool)
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var normalized any
	if err = json.Unmarshal(raw, &normalized); err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	id := "urn:androiddevice:" + tool
	if err = c.AddResource(id, normalized); err != nil {
		t.Fatal(err)
	}
	result, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestEveryDeviceActionHasMatchingBoundedContract(t *testing.T) {
	schemas := map[string]*jsonschema.Schema{ReadTool: compileSchema(t, ReadTool), ActTool: compileSchema(t, ActTool)}
	for action, definition := range Definitions {
		t.Run(action, func(t *testing.T) {
			tool := ActTool
			if definition.Read {
				tool = ReadTool
			}
			r := sample(action)
			plan, err := Build(tool, r, "call_01234567890123456789012345678901")
			if err != nil {
				t.Fatal(err)
			}
			if action != "status" && plan.Command == "" {
				t.Fatal("missing command")
			}
			raw, _ := json.Marshal(r)
			var value map[string]any
			if err = json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			if err = schemas[tool].Validate(value); err != nil {
				t.Fatal(err)
			}
			value["cmd"] = "echo injected"
			if schemas[tool].Validate(value) == nil {
				t.Fatal("unexpected shell field admitted")
			}
			delete(value, "cmd")
			value["backend"] = "default"
			if schemas[tool].Validate(value) == nil {
				t.Fatal("backend substitution admitted")
			}
		})
	}
}
func TestReadSurfaceCannotCompileMutations(t *testing.T) {
	for action, definition := range Definitions {
		if !definition.Read {
			if _, err := Build(ReadTool, sample(action), "call-test"); err == nil {
				t.Fatal(action)
			}
		}
	}
	r := sample("packages")
	r.Value = ptr("unexpected")
	if _, err := Build(ReadTool, r, "call-test"); err == nil {
		t.Fatal("unrelated value accepted")
	}
}
func TestSelectorsCannotInjectShellOrEscapeCaptureRoot(t *testing.T) {
	bad := []Request{
		{Action: "package_info", Package: "com.test; touch /sdcard/x"},
		{Action: "app_start", Component: "com.test/.Main$(id)"},
		{Action: "settings_get", Namespace: "global;id", Key: "k"},
		{Action: "appops_set", Package: "com.test", Operation: "CAMERA", Mode: "allow;id"},
		{Action: "screenshot", CaptureDir: "/storage/emulated/0/../private"},
		{Action: "screenshot", CaptureDir: "/data/local/tmp"},
		{Action: "package_install", Path: "/a/../b.apk"},
		{Action: "text", Text: "中文"}, {Action: "text", Text: "literal%s"},
		{Action: "settings_put", Namespace: "system", Key: "k", Value: ptr("a\nb")},
		{Action: "tap", X: ptr(-1), Y: ptr(0)},
	}
	for _, r := range bad {
		tool := ActTool
		if Definitions[r.Action].Read {
			tool = ReadTool
		}
		if _, err := Build(tool, r, "call-test"); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
}
func TestValuesRemainLiteralAndReadbackIsExplicit(t *testing.T) {
	value := "x'; $(printf injection); #"
	if quote(value) != "'x'\\''; $(printf injection); #'" {
		t.Fatalf("wrong shell quoting %q", quote(value))
	}
	r := sample("settings_put")
	r.Value = &value
	plan, err := Build(ActTool, r, "call-test")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(plan.Command, quote(value)) != 2 || plan.Verification != "exact_settings_readback" {
		t.Fatal(plan)
	}
	plan, err = Build(ActTool, Request{Action: "text", Text: "a'b c"}, "call-test")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Command != "input text 'a'\\''b%sc'" {
		t.Fatal(plan.Command)
	}
}
func TestCaptureNamesAreOriginalCallBoundAndNeverOverwrite(t *testing.T) {
	r := sample("screenshot")
	a, err := Build(ReadTool, r, "call-a")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Build(ReadTool, r, "call-a")
	b, _ := Build(ReadTool, r, "call-b")
	if a.ArtifactPath != again.ArtifactPath || a.ArtifactPath == b.ArtifactPath {
		t.Fatal("capture identity drift")
	}
	if !strings.Contains(a.Command, "set -C") || !strings.Contains(a.Command, "89504e470d0a1a0a") || a.ArtifactType != "image/png" {
		t.Fatal(a)
	}
	if _, err = Build(ReadTool, r, ""); err == nil {
		t.Fatal("capture without root call")
	}
}
