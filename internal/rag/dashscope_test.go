package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inv1sion/evoops/internal/domain"
)

func TestDashScopeEmbedderBatchesAndValidatesDimensions(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request %s auth=%s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var request struct {
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		response := map[string]any{"data": []any{}}
		data := make([]any, len(request.Input))
		for index := range request.Input {
			data[index] = map[string]any{"index": index, "embedding": []float32{1, 0, 0}}
		}
		response["data"] = data
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	embedder, err := NewDashScopeEmbedder("test-key", server.URL+"/v1", "text-embedding-v3", 3)
	if err != nil {
		t.Fatal(err)
	}
	inputs := make([]string, 11)
	for index := range inputs {
		inputs[index] = "document"
	}
	vectors, err := embedder.EmbedDocuments(context.Background(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 11 || calls != 2 {
		t.Fatalf("vectors=%d calls=%d", len(vectors), calls)
	}
}

func TestDashScopeRerankerPreservesHitIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{
			map[string]any{"index": 1, "relevance_score": 0.9},
			map[string]any{"index": 0, "relevance_score": 0.2},
		}})
	}))
	defer server.Close()
	reranker, err := NewDashScopeReranker("test-key", server.URL, "qwen3-rerank")
	if err != nil {
		t.Fatal(err)
	}
	hits := []domain.RetrievalHit{
		{Chunk: domain.KnowledgeChunk{ID: "a", Text: "A"}},
		{Chunk: domain.KnowledgeChunk{ID: "b", Text: "B"}},
	}
	result, err := reranker.Rerank(context.Background(), "query", hits)
	if err != nil {
		t.Fatal(err)
	}
	if result[0].Chunk.ID != "b" || result[0].RerankScore != 0.9 {
		t.Fatalf("unexpected rerank result: %#v", result)
	}
}
