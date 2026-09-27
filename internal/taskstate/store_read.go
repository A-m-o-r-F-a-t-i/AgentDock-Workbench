package taskstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
	"github.com/uvwt/agentdock/internal/fs/securepath"
)

func (s *Store) Get(id string) (Task, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return Task{}, err
	}
	defer release()
	return s.loadLocked(id)
}

func (s *Store) Delete(id string) (Task, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return Task{}, err
	}
	defer release()

	task, err := s.loadLocked(id)
	if err != nil {
		return Task{}, err
	}
	if err := s.deleteManagedLocked(task); err != nil {
		return Task{}, fmt.Errorf("delete task %s: %w", id, err)
	}
	return task, nil
}

func (s *Store) List(status Status, limit int) ([]Task, error) {
	return s.ListHistory(status, limit, false)
}

func (s *Store) ListHistory(status Status, limit int, includeArchived bool) ([]Task, error) {
	release, err := s.acquireStoreLock()
	if err != nil {
		return nil, err
	}
	defer release()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	tasks := make([]Task, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "tsk_") || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := s.readTaskStateFile(filepath.Join(s.root, entry.Name()))
		if err != nil {
			slog.Warn("skip unreadable task state", "file", entry.Name(), "error", err)
			continue
		}
		task, err := decodeTask(data, entry.Name())
		if err != nil {
			slog.Warn("skip invalid task state", "file", entry.Name(), "error", err)
			continue
		}
		task = s.applyManagementLocked(task)
		if task.TrashedAt != nil || task.ArchivedAt != nil && !includeArchived {
			continue
		}
		task, err = s.attachThreadLocked(task)
		if err != nil {
			slog.Warn("skip invalid task thread", "file", entry.Name(), "error", err)
			continue
		}
		if status == "" || task.Status == status {
			tasks = append(tasks, task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks, nil
}

func (s *Store) loadLocked(id string) (Task, error) {
	if err := validateID(id); err != nil {
		return Task{}, err
	}
	data, err := s.readTaskStateFile(filepath.Join(s.root, id+".json"))
	if os.IsNotExist(err) {
		return Task{}, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	if err != nil {
		return Task{}, err
	}
	task, err := decodeTask(data, id)
	if err != nil {
		return Task{}, err
	}
	return s.attachThreadLocked(s.applyManagementLocked(task))
}

func (s *Store) saveTaskOnlyLocked(task Task) error {
	task.ActiveThread = nil
	if err := validateID(task.ID); err != nil {
		return err
	}
	if len(task.Events) > maxTaskEvents {
		task.Events = append([]Event(nil), task.Events[len(task.Events)-maxTaskEvents:]...)
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxTaskStateFileBytes {
		return fmt.Errorf("task state exceeds %d bytes", maxTaskStateFileBytes)
	}
	target := filepath.Join(s.root, task.ID+".json")
	return atomicfile.Write(target, data, 0o600)
}

func (s *Store) readTaskStateFile(path string) ([]byte, error) {
	relative, err := filepath.Rel(s.root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, fmt.Errorf("task state path escapes its store")
	}
	data, err := securepath.ReadRegular(s.root, relative, maxTaskStateFileBytes)
	if errors.Is(err, securepath.ErrReadLimit) {
		return nil, fmt.Errorf("task state exceeds %d bytes: %w", maxTaskStateFileBytes, err)
	}
	return data, err
}

func decodeTask(data []byte, label string) (Task, error) {
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return Task{}, fmt.Errorf("decode task %s: %w", label, err)
	}
	// Legacy schema-1 indexes may contain shorter IDs. Keep them readable,
	// while requiring the identity in the record to match its actual file.
	if task.ID == "" || task.ID != strings.TrimSuffix(label, ".json") {
		return Task{}, fmt.Errorf("task identity does not match storage name %s", label)
	}
	if task.ActiveThreadID == "" {
		task.ActiveThreadID = MainThreadID
	}
	if task.Status == StatusCompleted && task.Outcome == "" {
		task.Outcome = "success"
	}
	if task.SchemaVersion != SchemaVersion {
		return Task{}, fmt.Errorf("unsupported task schema version %d", task.SchemaVersion)
	}
	if len(task.Events) > maxTaskEvents {
		task.Events = append([]Event(nil), task.Events[len(task.Events)-maxTaskEvents:]...)
	}
	return task, nil
}
