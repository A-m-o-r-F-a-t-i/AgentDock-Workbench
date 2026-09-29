// Package androiddevice compiles validated device operations into the existing
// Android command backend. It owns no task, process, authorization or transport.
package androiddevice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	pathpkg "path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const ReadTool = "android_device_read"
const ActTool = "android_device_act"

type Request struct {
	Action         string  `json:"action"`
	Package        string  `json:"package,omitempty"`
	Component      string  `json:"component,omitempty"`
	Namespace      string  `json:"namespace,omitempty"`
	Key            string  `json:"key,omitempty"`
	Value          *string `json:"value,omitempty"`
	Operation      string  `json:"operation,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	Permission     string  `json:"permission,omitempty"`
	Path           string  `json:"path,omitempty"`
	CaptureDir     string  `json:"capture_dir,omitempty"`
	X              *int    `json:"x,omitempty"`
	Y              *int    `json:"y,omitempty"`
	X2             *int    `json:"x2,omitempty"`
	Y2             *int    `json:"y2,omitempty"`
	DurationMS     *int    `json:"duration_ms,omitempty"`
	Keycode        *int    `json:"keycode,omitempty"`
	Text           string  `json:"text,omitempty"`
	Lines          *int    `json:"lines,omitempty"`
	TimeoutMS      *int    `json:"timeout_ms,omitempty"`
	YieldTimeMS    *int    `json:"yield_time_ms,omitempty"`
	MaxOutputBytes *int    `json:"max_output_bytes,omitempty"`
	ExecutionMode  string  `json:"execution_mode,omitempty"`
	Label          string  `json:"activity_label,omitempty"`
}

type Definition struct {
	Read             bool
	Fields, Required []string
}

var Definitions = map[string]Definition{
	"status":            {true, nil, nil},
	"packages":          {true, nil, nil},
	"package_info":      {true, []string{"package"}, []string{"package"}},
	"resolve_activity":  {true, []string{"package"}, []string{"package"}},
	"foreground":        {true, nil, nil},
	"display":           {true, nil, nil},
	"properties":        {true, []string{"key"}, nil},
	"settings_get":      {true, []string{"namespace", "key"}, []string{"namespace", "key"}},
	"appops_get":        {true, []string{"package", "operation"}, []string{"package"}},
	"logcat":            {true, []string{"lines"}, nil},
	"screenshot":        {true, []string{"capture_dir"}, []string{"capture_dir"}},
	"ui_dump":           {true, []string{"capture_dir"}, []string{"capture_dir"}},
	"app_start":         {false, []string{"component"}, []string{"component"}},
	"app_stop":          {false, []string{"package"}, []string{"package"}},
	"package_install":   {false, []string{"path"}, []string{"path"}},
	"component_enable":  {false, []string{"component"}, []string{"component"}},
	"component_disable": {false, []string{"component"}, []string{"component"}},
	"permission_grant":  {false, []string{"package", "permission"}, []string{"package", "permission"}},
	"permission_revoke": {false, []string{"package", "permission"}, []string{"package", "permission"}},
	"settings_put":      {false, []string{"namespace", "key", "value"}, []string{"namespace", "key", "value"}},
	"appops_set":        {false, []string{"package", "operation", "mode"}, []string{"package", "operation", "mode"}},
	"tap":               {false, []string{"x", "y"}, []string{"x", "y"}},
	"swipe":             {false, []string{"x", "y", "x2", "y2", "duration_ms"}, []string{"x", "y", "x2", "y2"}},
	"keyevent":          {false, []string{"keycode"}, []string{"keycode"}},
	"text":              {false, []string{"text"}, []string{"text"}},
}
var common = []string{"action", "timeout_ms", "yield_time_ms", "max_output_bytes", "execution_mode", "activity_label"}
var packageName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
var className = regexp.MustCompile(`^\.?[A-Za-z_$][A-Za-z0-9_.$]*$`)
var keyName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]{0,127}$`)
var opName = regexp.MustCompile(`^([A-Z][A-Z0-9_]{0,127}|android:[a-z_]{1,128})$`)
var sharedPath = regexp.MustCompile(`^/storage/emulated/[0-9]+/`)

type Plan struct{ Command, ArtifactPath, ArtifactType, Verification string }

func IsTool(name string) bool          { return name == ReadTool || name == ActTool }
func CaptureAction(action string) bool { return action == "screenshot" || action == "ui_dump" }
func quote(value string) string        { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func number(value *int, fallback int) int {
	if value != nil {
		return *value
	}
	return fallback
}
func absolute(value string) bool {
	return strings.HasPrefix(value, "/") && len(value) <= 4096 && value == pathpkg.Clean(value) && !strings.ContainsRune(value, 0)
}

func Build(tool string, r Request, callID string) (Plan, error) {
	def, exists := Definitions[r.Action]
	if !IsTool(tool) || !exists || def.Read != (tool == ReadTool) {
		return Plan{}, fmt.Errorf("action is not available through %s", tool)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return Plan{}, err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Plan{}, err
	}
	allowed := map[string]bool{}
	for _, name := range append(append([]string{}, common...), def.Fields...) {
		allowed[name] = true
	}
	for name, value := range fields {
		if !allowed[name] {
			return Plan{}, fmt.Errorf("field %s does not belong to action %s", name, r.Action)
		}
		if text, ok := value.(string); ok && (!utf8.ValidString(text) || strings.ContainsRune(text, 0) || len(text) > 4096) {
			return Plan{}, fmt.Errorf("invalid text field %s", name)
		}
	}
	for _, name := range def.Required {
		if _, found := fields[name]; !found {
			return Plan{}, fmt.Errorf("action %s requires %s", r.Action, name)
		}
	}
	if r.Package != "" && (len(r.Package) > 255 || !packageName.MatchString(r.Package)) {
		return Plan{}, fmt.Errorf("invalid package name")
	}
	if r.Component != "" {
		pkg, cls, ok := strings.Cut(r.Component, "/")
		if !ok || len(r.Component) > 512 || !packageName.MatchString(pkg) || !className.MatchString(cls) {
			return Plan{}, fmt.Errorf("invalid activity/component name")
		}
	}
	if r.Permission != "" && (len(r.Permission) > 255 || !packageName.MatchString(r.Permission)) {
		return Plan{}, fmt.Errorf("invalid permission name")
	}
	if r.Namespace != "" && r.Namespace != "global" && r.Namespace != "secure" && r.Namespace != "system" {
		return Plan{}, fmt.Errorf("invalid settings namespace")
	}
	if r.Key != "" && !keyName.MatchString(r.Key) {
		return Plan{}, fmt.Errorf("invalid property/settings key")
	}
	if r.Operation != "" && !opName.MatchString(r.Operation) {
		return Plan{}, fmt.Errorf("invalid appops operation")
	}
	if r.Mode != "" && r.Mode != "allow" && r.Mode != "ignore" && r.Mode != "deny" && r.Mode != "default" && r.Mode != "foreground" {
		return Plan{}, fmt.Errorf("invalid appops mode")
	}
	for _, coordinate := range []*int{r.X, r.Y, r.X2, r.Y2} {
		if coordinate != nil && (*coordinate < 0 || *coordinate > 32767) {
			return Plan{}, fmt.Errorf("coordinate outside supported bounds")
		}
	}
	for _, bound := range []struct {
		value    *int
		min, max int
	}{{r.DurationMS, 1, 5000}, {r.Keycode, 1, 1000}, {r.Lines, 1, 2000}, {r.TimeoutMS, 1, 120000}, {r.YieldTimeMS, 0, 30000}, {r.MaxOutputBytes, 1024, 1048576}} {
		if bound.value != nil && (*bound.value < bound.min || *bound.value > bound.max) {
			return Plan{}, fmt.Errorf("numeric parameter outside supported bounds")
		}
	}
	if r.ExecutionMode != "" && r.ExecutionMode != "auto" && r.ExecutionMode != "sync" && r.ExecutionMode != "async" {
		return Plan{}, fmt.Errorf("invalid execution mode")
	}
	result := Plan{Verification: "command_result_only"}
	pkg, component := quote(r.Package), quote(r.Component)
	setting := "settings "
	switch r.Action {
	case "status":
		return Plan{}, nil
	case "packages":
		result.Command = "cmd package list packages"
	case "package_info":
		result.Command = "dumpsys package " + pkg
	case "resolve_activity":
		result.Command = "cmd package resolve-activity --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -p " + pkg
	case "foreground":
		result.Command = "dumpsys window windows"
	case "display":
		result.Command = "wm size; wm density; dumpsys display"
	case "properties":
		result.Command = "getprop"
		if r.Key != "" {
			result.Command += " " + quote(r.Key)
		}
	case "settings_get":
		result.Command = setting + "get " + r.Namespace + " " + quote(r.Key)
	case "appops_get":
		result.Command = "cmd appops get " + pkg
		if r.Operation != "" {
			result.Command += " " + quote(r.Operation)
		}
	case "logcat":
		result.Command = "logcat -d -t " + strconv.Itoa(number(r.Lines, 200))
	case "app_start":
		result.Command = "am start -W -n " + component
	case "app_stop":
		result.Command = "am force-stop " + pkg
	case "component_enable", "component_disable":
		action := "enable"
		if r.Action == "component_disable" {
			action = "disable-user"
		}
		result.Command = "pm " + action + " " + component
	case "permission_grant", "permission_revoke":
		action := "grant"
		if r.Action == "permission_revoke" {
			action = "revoke"
		}
		result.Command = "pm " + action + " " + pkg + " " + quote(r.Permission)
	case "package_install":
		if !absolute(r.Path) || !strings.HasSuffix(strings.ToLower(r.Path), ".apk") {
			return Plan{}, fmt.Errorf("APK path must be an absolute readable Android path ending in .apk")
		}
		file := quote(r.Path)
		result.Command = "set -eu; test -f " + file + "; test -r " + file + "; size=$(wc -c < " + file + "); test \"$size\" -gt 0; test \"$size\" -le 536870912; pm install -r -S \"$size\" - < " + file
	case "settings_put":
		if strings.ContainsAny(*r.Value, "\r\n") {
			return Plan{}, fmt.Errorf("settings values must be a single line")
		}
		get := setting + "get " + r.Namespace + " " + quote(r.Key)
		result.Command = "set -eu; printf '%s\\n' 'BEFORE'; " + get + "; " + setting + "put " + r.Namespace + " " + quote(r.Key) + " " + quote(*r.Value) + "; after=$(" + get + "); printf '%s\\n' 'AFTER' \"$after\"; test \"$after\" = " + quote(*r.Value)
		result.Verification = "exact_settings_readback"
	case "appops_set":
		get := "cmd appops get " + pkg + " " + quote(r.Operation)
		result.Command = "set -e; printf '%s\\n' 'BEFORE'; " + get + "; cmd appops set " + pkg + " " + quote(r.Operation) + " " + r.Mode + "; printf '%s\\n' 'AFTER'; " + get
		result.Verification = "appops_readback_in_stdout"
	case "tap":
		result.Command = fmt.Sprintf("input tap %d %d", *r.X, *r.Y)
	case "swipe":
		result.Command = fmt.Sprintf("input swipe %d %d %d %d %d", *r.X, *r.Y, *r.X2, *r.Y2, number(r.DurationMS, 300))
	case "keyevent":
		result.Command = "input keyevent " + strconv.Itoa(*r.Keycode)
	case "text":
		if len(r.Text) > 2048 || strings.Contains(r.Text, "%s") {
			return Plan{}, fmt.Errorf("input text is bounded ASCII and cannot contain the reserved %%s sequence")
		}
		for _, char := range r.Text {
			if char < 32 || char > 126 {
				return Plan{}, fmt.Errorf("Android input text backend requires printable ASCII; no silent Unicode loss")
			}
		}
		result.Command = "input text " + quote(strings.ReplaceAll(r.Text, " ", "%s"))
	case "screenshot", "ui_dump":
		if !absolute(r.CaptureDir) || !(strings.HasPrefix(r.CaptureDir, "/sdcard/") || sharedPath.MatchString(r.CaptureDir)) || callID == "" {
			return Plan{}, fmt.Errorf("capture_dir must be an explicit shared Android directory")
		}
		digest := sha256.Sum256([]byte(callID))
		suffix := ".png"
		result.ArtifactType = "image/png"
		if r.Action == "ui_dump" {
			suffix = ".xml"
			result.ArtifactType = "application/xml"
		}
		result.ArtifactPath = pathpkg.Join(r.CaptureDir, "agentdock-"+hex.EncodeToString(digest[:12])+suffix)
		file := quote(result.ArtifactPath)
		prefix := "set -eu; mkdir -p " + quote(r.CaptureDir) + "; test ! -e " + file + "; test ! -L " + file + "; "
		if r.Action == "screenshot" {
			result.Command = prefix + "set -C; screencap -p > " + file + "; test \"$(od -An -tx1 -N8 " + file + " | tr -d ' \\n')\" = 89504e470d0a1a0a"
		} else {
			result.Command = prefix + "uiautomator dump " + file + "; test -s " + file
		}
		result.Verification = "capture_file_checked"
	}
	if len(result.Command) > 16384 {
		return Plan{}, fmt.Errorf("compiled Android request is too large")
	}
	return result, nil
}
