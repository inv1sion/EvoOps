package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/inv1sion/evoops/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Repository interface {
	SaveRun(context.Context, *domain.Run) error
	GetRun(context.Context, string) (*domain.Run, error)
	ListRuns(context.Context, int) ([]domain.Run, error)
	AddFeedback(context.Context, domain.Feedback) error
	ListFeedback(context.Context) ([]domain.Feedback, error)
	SavePolicyState(context.Context, domain.PolicyState) error
	LoadPolicyState(context.Context) (domain.PolicyState, error)
	SaveEval(context.Context, domain.EvalResult) error
	ListEvals(context.Context) ([]domain.EvalResult, error)
}

type FileRepository struct {
	root string
	mu   sync.RWMutex
}

func NewFile(root string) (*FileRepository, error) {
	for _, dir := range []string{"runs", "feedback", "evals"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			return nil, fmt.Errorf("create repository directory: %w", err)
		}
	}
	return &FileRepository{root: root}, nil
}

func (r *FileRepository) SaveRun(_ context.Context, run *domain.Run) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return writeJSON(filepath.Join(r.root, "runs", run.ID+".json"), run)
}

func (r *FileRepository) GetRun(_ context.Context, id string) (*domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var run domain.Run
	if err := readJSON(filepath.Join(r.root, "runs", id+".json"), &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *FileRepository) ListRuns(_ context.Context, limit int) ([]domain.Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries, err := os.ReadDir(filepath.Join(r.root, "runs"))
	if err != nil {
		return nil, err
	}
	runs := make([]domain.Run, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var run domain.Run
		if err := readJSON(filepath.Join(r.root, "runs", entry.Name()), &run); err == nil {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt.After(runs[j].StartedAt) })
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

func (r *FileRepository) AddFeedback(_ context.Context, feedback domain.Feedback) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return writeJSON(filepath.Join(r.root, "feedback", feedback.ID+".json"), feedback)
}

func (r *FileRepository) ListFeedback(_ context.Context) ([]domain.Feedback, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.Feedback
	if err := readDirectory(filepath.Join(r.root, "feedback"), func(path string) error {
		var item domain.Feedback
		if err := readJSON(path, &item); err != nil {
			return err
		}
		result = append(result, item)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result, nil
}

func (r *FileRepository) SavePolicyState(_ context.Context, state domain.PolicyState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return writeJSON(filepath.Join(r.root, "policies.json"), state)
}

func (r *FileRepository) LoadPolicyState(_ context.Context) (domain.PolicyState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var state domain.PolicyState
	if err := readJSON(filepath.Join(r.root, "policies.json"), &state); err != nil {
		return domain.PolicyState{}, err
	}
	return state, nil
}

func (r *FileRepository) SaveEval(_ context.Context, result domain.EvalResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return writeJSON(filepath.Join(r.root, "evals", result.ID+".json"), result)
}

func (r *FileRepository) ListEvals(_ context.Context) ([]domain.EvalResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []domain.EvalResult
	if err := readDirectory(filepath.Join(r.root, "evals"), func(path string) error {
		var item domain.EvalResult
		if err := readJSON(path, &item); err != nil {
			return err
		}
		result = append(result, item)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func readDirectory(dir string, fn func(string) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if err := fn(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".evoops-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err == nil {
		return nil
	}
	// Windows cannot always atomically replace an existing file. The lock keeps
	// readers out while the fallback replaces only EvoOps-owned runtime data.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpName, path)
}
