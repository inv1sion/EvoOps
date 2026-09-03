package dataset

import (
	"context"
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
)

func TestExternalKnowledgeSearcherKeepsStoreValidationAndToolContract(t *testing.T) {
	repo := NewMemory([]domain.StoreData{{StoreID: "store-a"}})
	searcher := &recordingSearcher{}
	repo.SetKnowledgeSearcher(searcher)

	result, err := repo.SearchKnowledge(context.Background(), "store-a", "低 ROI", domain.RetrievalConfig{TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !searcher.called || result.Trace.Backend != "external-test" {
		t.Fatalf("external searcher was not used: %+v", result)
	}
	if _, err := repo.SearchKnowledge(context.Background(), "another-store", "低 ROI", domain.RetrievalConfig{}); err == nil {
		t.Fatal("unknown store must fail before external retrieval")
	}
}

type recordingSearcher struct{ called bool }

func (s *recordingSearcher) SearchKnowledge(context.Context, string, string, domain.RetrievalConfig) (domain.RetrievalResult, error) {
	s.called = true
	return domain.RetrievalResult{Trace: domain.RetrievalTrace{Backend: "external-test"}}, nil
}
