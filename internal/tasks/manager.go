package tasks

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/windowsfreak/leben/internal/models"
)

type Manager struct {
	tasks sync.Map // map[string]*taskEntry
	locks sync.Map // map[string]*sync.Mutex
}

type taskEntry struct {
	task       *models.TranslationTask
	cancel     context.CancelFunc
	cancelLock sync.Mutex
}

func NewManager() *Manager {
	return &Manager{}
}

// AcquireLock acquires a lock for a given resource key (e.g. tile name or file name).
// Returns an unlock function and an error if already locked.
func (m *Manager) AcquireLock(key string) (func(), error) {
	val, _ := m.locks.LoadOrStore(key, &sync.Mutex{})
	mtx := val.(*sync.Mutex)

	if !mtx.TryLock() {
		return nil, fmt.Errorf("resource '%s' is locked by another task", key)
	}

	return func() {
		mtx.Unlock()
	}, nil
}

// IsLocked checks if a resource key is currently locked
func (m *Manager) IsLocked(key string) bool {
	val, ok := m.locks.Load(key)
	if !ok {
		return false
	}
	mtx := val.(*sync.Mutex)
	if mtx.TryLock() {
		mtx.Unlock()
		return false
	}
	return true
}

// CreateTask registers a new background translation task and returns task info, context, and a cleanup function
func (m *Manager) CreateTask(parentCtx context.Context, tileName, targetLang string) (*models.TranslationTask, context.Context, func(), error) {
	lockKey := fmt.Sprintf("tile:%s", tileName)
	unlock, err := m.AcquireLock(lockKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot start task for card '%s': task is already running for this card", tileName)
	}

	taskID := uuid.New().String()
	bgCtx := parentCtx
	if bgCtx == nil {
		bgCtx = context.Background()
	} else {
		bgCtx = context.WithoutCancel(bgCtx)
	}
	ctx, cancel := context.WithCancel(bgCtx)

	task := &models.TranslationTask{
		ID:         taskID,
		TileName:   tileName,
		TargetLang: targetLang,
		Status:     models.TaskRunning,
		Progress:   "Initializing task...",
		StartedAt:  time.Now(),
	}

	entry := &taskEntry{
		task:   task,
		cancel: cancel,
	}

	m.tasks.Store(taskID, entry)

	cleanup := func() {
		unlock()
	}

	return task, ctx, cleanup, nil
}

func (m *Manager) UpdateTaskProgress(id string, progress string) {
	if val, ok := m.tasks.Load(id); ok {
		entry := val.(*taskEntry)
		entry.task.Progress = progress
	}
}

func (m *Manager) CompleteTask(id string, result any) {
	if val, ok := m.tasks.Load(id); ok {
		entry := val.(*taskEntry)
		entry.task.Status = models.TaskCompleted
		entry.task.Progress = "Completed successfully."
		now := time.Now()
		entry.task.CompletedAt = &now
		entry.task.Result = result
	}
}

func (m *Manager) FailTask(id string, err error) {
	if val, ok := m.tasks.Load(id); ok {
		entry := val.(*taskEntry)
		entry.task.Status = models.TaskFailed
		entry.task.Progress = "Failed"
		entry.task.Error = err.Error()
		now := time.Now()
		entry.task.CompletedAt = &now
	}
}

func (m *Manager) CancelTask(id string) bool {
	if val, ok := m.tasks.Load(id); ok {
		entry := val.(*taskEntry)
		entry.cancelLock.Lock()
		defer entry.cancelLock.Unlock()

		if entry.task.Status == models.TaskRunning || entry.task.Status == models.TaskPending {
			entry.cancel()
			entry.task.Status = models.TaskCancelled
			entry.task.Progress = "Cancelled by user."
			now := time.Now()
			entry.task.CompletedAt = &now
			return true
		}
	}
	return false
}

func (m *Manager) GetTask(id string) (*models.TranslationTask, bool) {
	if val, ok := m.tasks.Load(id); ok {
		entry := val.(*taskEntry)
		// Return copy
		t := *entry.task
		return &t, true
	}
	return nil, false
}

func (m *Manager) GetTasks() []*models.TranslationTask {
	var list []*models.TranslationTask
	m.tasks.Range(func(key, val any) bool {
		entry := val.(*taskEntry)
		t := *entry.task
		list = append(list, &t)
		return true
	})
	sort.Slice(list, func(i, j int) bool {
		return list[i].StartedAt.After(list[j].StartedAt)
	})
	return list
}
