package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/windowsfreak/leben/internal/models"
)

func TestCreateTaskDetachedContext(t *testing.T) {
	mgr := NewManager()

	// Simulate an HTTP request context that gets canceled when request finishes
	httpCtx, cancelHTTP := context.WithCancel(context.Background())

	task, taskCtx, cleanup, err := mgr.CreateTask(httpCtx, "test-tile", "en")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer cleanup()

	// Now simulate HTTP request finishing and cancelling its context
	cancelHTTP()

	// Verify taskCtx is still alive (not canceled)
	select {
	case <-taskCtx.Done():
		t.Fatalf("task context was canceled when parent HTTP context was canceled!")
	case <-time.After(50 * time.Millisecond):
		// Success: taskCtx is still alive
	}

	// Verify explicit cancel via manager works
	if !mgr.CancelTask(task.ID) {
		t.Fatalf("expected CancelTask to return true")
	}

	select {
	case <-taskCtx.Done():
		// Success: explicitly canceled by manager
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("task context was not canceled after mgr.CancelTask!")
	}

	tTask, ok := mgr.GetTask(task.ID)
	if !ok || tTask.Status != models.TaskCancelled {
		t.Fatalf("expected task status cancelled, got %v", tTask.Status)
	}
}
