package rag

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inv1sion/evoops/internal/domain"
)

// TestDashScopeLive is intentionally opt-in: ordinary CI remains offline and
// deterministic, while maintainers can verify the configured cloud contract
// without putting credentials in source control.
func TestDashScopeLive(t *testing.T) {
	if os.Getenv("EVOOPS_LIVE_RAG_TEST") != "1" {
		t.Skip("set EVOOPS_LIVE_RAG_TEST=1 to call DashScope")
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Fatal("OPENAI_API_KEY is required")
	}
	baseURL := valueOr(os.Getenv("EVOOPS_RAG_EMBEDDING_BASE_URL"), "https://dashscope.aliyuncs.com/compatible-mode/v1")
	embedder, err := NewDashScopeEmbedder(key, baseURL, valueOr(os.Getenv("EVOOPS_RAG_EMBEDDING_MODEL"), "text-embedding-v3"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	vectors, err := embedder.EmbedDocuments(ctx, []string{"广告 ROI 下降需要先核验归因口径", "暂停广告必须经过人工审批"})
	if err != nil {
		t.Fatalf("embedding live call: %v", err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 1024 {
		t.Fatalf("unexpected embedding shape: rows=%d dimensions=%d", len(vectors), len(vectors[0]))
	}

	rerankURL := strings.TrimSpace(os.Getenv("EVOOPS_RAG_RERANK_URL"))
	if rerankURL == "" {
		t.Fatal("EVOOPS_RAG_RERANK_URL must use the workspace-specific compatible-api endpoint")
	}
	reranker, err := NewDashScopeReranker(key, rerankURL, valueOr(os.Getenv("EVOOPS_RAG_RERANK_MODEL"), "qwen3-rerank"))
	if err != nil {
		t.Fatal(err)
	}
	hits := []domain.RetrievalHit{
		{Chunk: domain.KnowledgeChunk{ID: "approval", Text: "暂停广告必须经过人工审批。"}},
		{Chunk: domain.KnowledgeChunk{ID: "unrelated", Text: "健康计划保持稳定投放。"}},
	}
	reranked, err := reranker.Rerank(ctx, "暂停广告需要审批吗", hits)
	if err != nil {
		t.Fatalf("rerank live call: %v", err)
	}
	if len(reranked) != 2 || reranked[0].Chunk.ID != "approval" {
		t.Fatalf("unexpected rerank result: %+v", reranked)
	}
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
