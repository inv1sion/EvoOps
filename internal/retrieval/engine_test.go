package retrieval

import (
	"context"
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
)

func TestHybridSearchRanksAndMergesRelevantPlaybook(t *testing.T) {
	articles := []domain.KnowledgeArticle{
		{ID: "ads", Title: "Campaign ROI", Content: "Verify campaign tracking and marginal ROI. Pause inefficient advertising only after approval.", Tags: []string{"campaign", "roi"}},
		{ID: "stock", Title: "Inventory", Content: "Replenish inventory based on stock cover days. Protect hero products from stockout.", Tags: []string{"stockout"}},
	}
	result := New().Search(context.Background(), articles, "campaign roi low", domain.RetrievalConfig{
		TopK: 2, CandidateK: 8, DenseWeight: .55, SparseWeight: .45, RRFK: 60,
		MergeThreshold: .5, RelevanceThreshold: .3, RerankEnabled: true, QueryRewriteStrategy: "step_back",
	})
	if len(result.Hits) == 0 || result.Hits[0].Chunk.DocID != "ads" {
		t.Fatalf("unexpected ranking: %#v", result)
	}
	if len(result.Trace.DenseRanking) == 0 || len(result.Trace.SparseRanking) == 0 || len(result.Trace.FinalRanking) == 0 {
		t.Fatalf("retrieval trace is incomplete: %#v", result.Trace)
	}
	if result.Cost <= 0 {
		t.Fatalf("cost=%f, want positive", result.Cost)
	}
}

func TestLowRelevanceTriggersRewrite(t *testing.T) {
	result := New().Search(context.Background(), []domain.KnowledgeArticle{{ID: "generic", Title: "Operations", Content: "General business playbook."}}, "unknown anomaly", domain.RetrievalConfig{
		TopK: 1, CandidateK: 4, DenseWeight: .5, SparseWeight: .5, RelevanceThreshold: .99,
		RerankEnabled: true, QueryRewriteStrategy: "hyde",
	})
	if !result.Trace.RewriteUsed {
		t.Fatalf("expected rewrite trace: %#v", result.Trace)
	}
}

func TestBusinessPhraseRerankPreservesMultiSignalIntent(t *testing.T) {
	articles := []domain.KnowledgeArticle{
		{ID: "conversion", Title: "Conversion drop", Content: "Inspect traffic campaign and spend before changing the checkout funnel.", Tags: []string{"conversion drop"}},
		{ID: "traffic", Title: "Traffic drop", Content: "Compare paid and organic cohorts.", Tags: []string{"traffic drop"}},
		{ID: "refund", Title: "Refund rate", Content: "Split refunds by product-quality and fulfillment causes.", Tags: []string{"refund rate spike"}},
		{ID: "ads", Title: "Campaign ROI", Content: "Verify marginal advertising efficiency.", Tags: []string{"campaign roi low"}},
	}
	result := New().Search(context.Background(), articles, "traffic drop refund rate spike campaign roi low", domain.RetrievalConfig{
		TopK: 3, CandidateK: 12, DenseWeight: .55, SparseWeight: .45, RRFK: 60,
		MergeThreshold: .5, RelevanceThreshold: .3, RerankEnabled: true, QueryRewriteStrategy: "step_back",
	})
	got := map[string]bool{}
	for _, hit := range result.Hits {
		got[hit.Chunk.DocID] = true
	}
	for _, want := range []string{"traffic", "refund", "ads"} {
		if !got[want] {
			t.Fatalf("missing %s from final ranking %v", want, result.Trace.FinalRanking)
		}
	}
}
