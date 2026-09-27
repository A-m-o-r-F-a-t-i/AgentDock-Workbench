package task

import (
	"errors"
	"reflect"
	"testing"

	"github.com/uvwt/agentdock/internal/taskstate"
)

func TestTaskToolErrorProvidesRecoveryGuidance(t *testing.T) {
	for _, test := range []struct {
		name         string
		message      string
		failureClass string
		nextAction   string
		detailKey    string
		detailValue  any
	}{
		{
			name:         "resume requires summary",
			message:      "resume summary is required",
			failureClass: "AGENT_INPUT_INVALID",
			nextAction:   "resume",
			detailKey:    "required_fields",
			detailValue:  []string{"summary"},
		},
		{
			name:         "complete requires review",
			message:      "final_review must pass before complete",
			failureClass: "AGENT_STATE_INVALID",
			nextAction:   "final_review",
			detailKey:    "allowed_actions",
			detailValue:  []string{"get", "checkpoint", "final_review"},
		},
		{
			name:         "review requires completed steps",
			message:      "passing final review requires all task steps completed: S4",
			failureClass: "AGENT_STATE_INVALID",
			nextAction:   "checkpoint",
			detailKey:    "required_state",
			detailValue:  "all_steps_completed",
		},
		{
			name:         "completed task is immutable",
			message:      "completed tasks are immutable",
			failureClass: "AGENT_STATE_INVALID",
			nextAction:   "get",
			detailKey:    "required_state",
			detailValue:  "task_already_completed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := taskToolError(errors.New(test.message))
			var toolErr *ToolError
			if !errors.As(err, &toolErr) || toolErr.Code != "TASK_STATE_ERROR" {
				t.Fatalf("taskToolError() = %#v, want TASK_STATE_ERROR", err)
			}
			if toolErr.Details["retryable"] != false || toolErr.Details["failure_class"] != test.failureClass || toolErr.Details["next_action"] != test.nextAction {
				t.Fatalf("guidance details = %#v", toolErr.Details)
			}
			if !reflect.DeepEqual(toolErr.Details[test.detailKey], test.detailValue) {
				t.Fatalf("details[%q] = %#v, want %#v", test.detailKey, toolErr.Details[test.detailKey], test.detailValue)
			}
		})
	}
}

func TestTaskNotFoundGuidesAgentBackToTaskList(t *testing.T) {
	err := taskToolError(taskstate.ErrTaskNotFound)
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "TASK_NOT_FOUND" {
		t.Fatalf("taskToolError() = %#v, want TASK_NOT_FOUND", err)
	}
	if toolErr.Details["failure_class"] != "AGENT_STATE_STALE" || toolErr.Details["next_action"] != "list" || toolErr.Details["retryable"] != false {
		t.Fatalf("task not found details = %#v", toolErr.Details)
	}
}
