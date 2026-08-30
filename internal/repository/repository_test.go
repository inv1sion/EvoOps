package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inv1sion/evoops/internal/domain"
)

func TestFileRepositoryPersistsRunsAndFeedback(t *testing.T) {
	repo, err := NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run := &domain.Run{ID: "run-1", Status: domain.RunRunning, StartedAt: time.Now().UTC()}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetRun(ctx, run.ID)
	if err != nil || loaded.ID != run.ID {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	feedback := domain.Feedback{ID: "feedback-1", RunID: run.ID, Useful: true, CreatedAt: time.Now().UTC()}
	if err := repo.AddFeedback(ctx, feedback); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ListFeedback(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("feedback=%#v err=%v", items, err)
	}
	if _, err := repo.GetRun(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing error=%v", err)
	}
}
