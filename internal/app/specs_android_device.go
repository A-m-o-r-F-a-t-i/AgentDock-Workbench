package app

import (
	"context"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/config"
	"github.com/uvwt/agentdock/internal/tool/androiddevice"
	toolcommand "github.com/uvwt/agentdock/internal/tool/command"
)

func androidDeviceToolSpecs() []ToolSpec {
	specs := []ToolSpec{}
	for _, name := range []string{androiddevice.ReadTool, androiddevice.ActTool} {
		annotations := mutatingToolAnnotations(true, true)
		description := "Perform one validated Android system action through the verified Shizuku backend. Uses existing Core permissions and sessions. Input coordinates refer to the primary display; inspect geometry first. Text input is printable ASCII only. No automatic retry, application restart or backend fallback. Nonzero/unknown results must be inspected before another mutation."
		if name == androiddevice.ReadTool {
			annotations = readOnlyToolAnnotations(false)
			description = "Read Android system state using the paired phone Shizuku backend. status reads only Core metadata and works offline. Screenshot/UI dump write a unique file in the explicit Android capture_dir; images are not embedded in Binder JSON. Observe a returned running session before using artifact_path, then use existing file/image tools when that shared path is accessible. Browser DOM control remains preferred for web pages."
		}
		specs = append(specs, ToolSpec{Name: name, Contract: androidDeviceToolContract, Title: name,
			Description: description, Annotations: annotations,
			Handler: typedToolHandler(name, func(ctx context.Context, r *Runtime, request androiddevice.Request) (Result, error) {
				return r.executeAndroidDevice(ctx, name, request)
			}),
		})
	}
	return specs
}

func androidDeviceToolContract(name string, _ config.Config) (ToolContract, bool) {
	return staticToolContract(name, androiddevice.InputSchema, androiddevice.OutputSchema)
}

// These handlers borrow the existing command lifetime under the original root
// Call. They must not emit a second call.completed while a session is running.
func isCommandExecutionTool(name string) bool {
	return name == "exec_command" || androiddevice.IsTool(name)
}

func (r *Runtime) executeAndroidDevice(ctx context.Context, name string, request androiddevice.Request) (Result, error) {
	binding := activity.FromContext(ctx)
	plan, err := androiddevice.Build(name, request, binding.CallID)
	if err != nil {
		return nil, toolError("ANDROID_REQUEST_INVALID", err.Error(), "validation")
	}
	if request.Action == "status" {
		return Result{"action": request.Action, "backend": "android_shizuku", "device_status": r.androidExecutor.Status()}, nil
	}
	timeout := 30000
	if request.TimeoutMS != nil {
		timeout = *request.TimeoutMS
	}
	secrets := []string{}
	if request.Value != nil {
		secrets = append(secrets, *request.Value)
	}
	if request.Text != "" {
		secrets = append(secrets, request.Text)
	}
	result, err := r.command.Exec(ctx, toolcommand.ExecRequest{
		Binding: binding, AuditToolName: name, AuditSecrets: secrets,
		Backend: "android_shizuku", Cmd: plan.Command, Workdir: "/", TimeoutMS: &timeout,
		YieldTimeMS: request.YieldTimeMS, MaxOutputBytes: request.MaxOutputBytes, ExecutionMode: request.ExecutionMode,
	})
	if result == nil {
		return nil, err
	}
	result["action"], result["backend"] = request.Action, "android_shizuku"
	result["verification"] = "pending_or_unconfirmed"
	if result["command_ok"] == true {
		result["verification"] = plan.Verification
	}
	if plan.ArtifactPath != "" {
		result["artifact_path"], result["artifact_type"] = plan.ArtifactPath, plan.ArtifactType
		result["artifact_state"] = "unconfirmed"
		if result["status"] == "running" || result["status"] == "queued" {
			result["artifact_state"] = "pending"
		}
		if err == nil && result["command_ok"] == true {
			result["artifact_state"] = "ready"
		}
	}
	return result, err
}
