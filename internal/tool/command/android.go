package command

import (
	"context"
	"io"
	"time"

	"github.com/uvwt/agentdock/internal/androidbridge"
	"github.com/uvwt/agentdock/internal/tool/command/session"
)

func (s *Service) SetAndroidBroker(b *androidbridge.Broker) { s.android = b }

func (s *Service) prepareAndroidInvocation(ctx context.Context, request ExecRequest, timeout time.Duration) (commandInvocation, error) {
	if err := ctx.Err(); err != nil {
		return commandInvocation{}, err
	}
	if request.Runtime != "" || request.WSLDistribution != "" || request.Skill != "" || request.SkillRef != "" || request.SkillEnv != "" {
		return commandInvocation{}, toolError("ANDROID_CONTEXT_CONFLICT", "Android backends require their own absolute workdir and explicit environment; host runtime/Skill paths are not forwarded", "validation")
	}
	spec := androidbridge.Spec{Backend: request.Backend, Command: request.Cmd, Workdir: request.Workdir, Env: request.Env, TimeoutMS: timeout.Milliseconds(), TTY: request.TTY}
	if err := androidbridge.ValidateSpec(spec); err != nil {
		return commandInvocation{}, toolError("ANDROID_REQUEST_INVALID", "Invalid Android command, absolute backend workdir, environment, or deadline", "validation")
	}
	if s.android == nil || !s.android.Ready(request.Backend) {
		return commandInvocation{}, toolError("ANDROID_BACKEND_UNAVAILABLE", "Enable and verify the selected backend in the paired phone Workbench; no command was dispatched", "runtime")
	}
	return commandInvocation{execution: session.ExecutionContext{Runtime: request.Backend, Workdir: request.Workdir},
		external: func(ctx context.Context, id string, out, errout io.Writer, state func(string)) (session.ExternalProcess, error) {
			return s.android.Start(ctx, id, spec, out, errout, state)
		}}, nil
}
