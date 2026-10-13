package taskstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/fs/filelock"
)

func TestTaskIndexBoundedRecoveryAndWarnings(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 14; i++ {
		task, err := s.Create("long recovery title", "goal", []string{"verified"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateThread(task.ID, "main", "checkpoint", ThreadInput{Summary: strings.Repeat("\"", 7000), NextAction: strings.Repeat("\"", 7000), WorkspaceID: "wsp_test"}); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			if _, err = s.Block(task.ID, "external dependency"); err != nil {
				t.Fatal(err)
			}
		}
		if i == 13 {
			if _, err = s.Cancel(task.ID, "done"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Archive(task.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = os.WriteFile(filepath.Join(s.Root(), "tsk_1234567890abcdef.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	index, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > MaxTaskIndexBytes || index.ReturnedCount > 8 || !index.Truncated || index.ActiveCount != 10 || index.BlockedCount != 3 || len(index.Warnings) != 1 {
		t.Fatalf("index %d bytes: %+v", len(data), index)
	}
	for _, item := range index.Items {
		if item.ActiveThread == nil || item.ActiveThread.ID != "main" || item.WorkspaceID != "wsp_test" {
			t.Fatalf("recovery lost: %+v", item)
		}
	}
}

func TestEmptyTaskIndexContainsArray(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	index, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(index)
	if !strings.Contains(string(data), `"items":[]`) || index.Truncated || index.ActiveCount != 0 {
		t.Fatal(string(data))
	}
}

func TestTaskIndexLockAdmissionHonorsCancellation(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release, err := filelock.Acquire(t.Context(), filepath.Join(s.Root(), ".store.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Discover(ctx)
		done <- err
	}()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled lock admission returned %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		release()
		<-done
		t.Fatal("canceled task index request remained queued behind the store lock")
	}
}

func TestTaskIndexKeepsBoundTaskAndPrefersWorkspace(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bound, err := s.Create("bound current task", "goal", []string{"verified"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetWorkspace(bound.ID, "wsp_current"); err != nil {
		t.Fatal(err)
	}
	related, err := s.Create("related workspace task", "goal", []string{"verified"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetWorkspace(related.ID, "wsp_current"); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		other, createErr := s.Create("newer unrelated task", "goal", []string{"verified"}, nil, nil)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = s.SetWorkspace(other.ID, "wsp_other"); createErr != nil {
			t.Fatal(createErr)
		}
	}
	ctx := activity.WithExecutionScope(context.Background(), activity.Binding{TaskID: bound.ID, ThreadID: MainThreadID, WorkspaceID: "wsp_current"})
	index, err := s.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Items) != MaxTaskIndexItems || index.Items[0].TaskID != bound.ID || index.Items[1].TaskID != related.ID {
		t.Fatalf("context recovery priority lost: %+v", index.Items)
	}
}

func TestTaskIndexWorkspaceScopeExcludesUnrelatedCandidates(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bound, err := s.Create("bound current task", "goal", []string{"verified"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetWorkspace(bound.ID, "wsp_current"); err != nil {
		t.Fatal(err)
	}
	related, err := s.Create("related workspace task", "goal", []string{"verified"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetWorkspace(related.ID, "wsp_current"); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		other, createErr := s.Create("unrelated task", "goal", []string{"verified"}, nil, nil)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, createErr = s.SetWorkspace(other.ID, "wsp_other"); createErr != nil {
			t.Fatal(createErr)
		}
	}
	ctx := activity.WithExecutionScope(context.Background(), activity.Binding{TaskID: bound.ID, ThreadID: MainThreadID, WorkspaceID: "wsp_current"})
	index, err := s.DiscoverForWorkspace(ctx, "wsp_current")
	if err != nil {
		t.Fatal(err)
	}
	if index.ActiveCount != 10 || index.BlockedCount != 0 || !index.Truncated || index.ReturnedCount != 2 {
		t.Fatalf("workspace index metadata = %+v", index)
	}
	if len(index.Items) != 2 || index.Items[0].TaskID != bound.ID || index.Items[1].TaskID != related.ID {
		t.Fatalf("workspace index leaked unrelated tasks: %+v", index.Items)
	}
	for _, item := range index.Items {
		if item.WorkspaceID != "wsp_current" {
			t.Fatalf("workspace index returned foreign item: %+v", item)
		}
	}
}

func TestTaskIndexRestoresConversationBoundThread(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Create("multi-thread task", "goal", []string{"verified"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := s.CreateThread(task.ID, MainThreadID, false, ThreadInput{Title: "conversation branch", WorkspaceID: "wsp_branch"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := activity.WithExecutionScope(context.Background(), activity.Binding{TaskID: task.ID, ThreadID: branch.ID, WorkspaceID: "wsp_branch"})
	index, err := s.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Items) == 0 || index.Items[0].TaskID != task.ID || index.Items[0].ActiveThread == nil {
		t.Fatalf("bound task missing: %+v", index.Items)
	}
	if index.Items[0].ActiveThread.ID != branch.ID || index.Items[0].WorkspaceID != "wsp_branch" {
		t.Fatalf("bound thread not restored: %+v", index.Items[0])
	}
}
