package rag

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
)

func TestServiceIngestBuildsThreeLevelHierarchy(t *testing.T) {
	parents := newFakeParentStore()
	leaves := &fakeLeafStore{}
	service := NewService(parents, leaves, nil, fakeEmbedder{}, nil, 6000)

	result, err := service.Ingest(context.Background(), IngestInput{
		StoreID: "store-a", Scope: ScopeStore, Title: "低 ROI 处置手册", SourceURI: "file:///roi.md",
		MediaType: "text/markdown", Content: repeatedRunes(1500),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.L1Count != 2 || result.L2Count != 4 || result.L3Count != 9 {
		t.Fatalf("unexpected hierarchy counts: %+v", result)
	}
	if len(parents.parents) != result.L1Count+result.L2Count {
		t.Fatalf("PostgreSQL fake got %d parents", len(parents.parents))
	}
	if len(leaves.records) != result.L3Count {
		t.Fatalf("Milvus fake got %d leaves", len(leaves.records))
	}
	if parents.documents[result.Document.ID].Status != StatusReady {
		t.Fatal("document should become ready only after both stores succeed")
	}
}

func TestServiceSearchReranksAndAutoMergesL3ToL1(t *testing.T) {
	parents := newFakeParentStore()
	doc := parents.addReadyDocument("store-a", ScopeStore)
	l1 := Chunk{ID: "l1", DocID: doc.ID, StoreID: doc.StoreID, Scope: doc.Scope, DocumentVersion: 1, Level: 1, Content: "完整的广告投放处置章节", TotalChildren: 2, Title: "手册"}
	l2a := Chunk{ID: "l2-a", DocID: doc.ID, StoreID: doc.StoreID, Scope: doc.Scope, DocumentVersion: 1, Level: 2, ParentID: l1.ID, ParentL1ID: l1.ID, Content: "原因分析", TotalChildren: 2, Title: "手册"}
	l2b := Chunk{ID: "l2-b", DocID: doc.ID, StoreID: doc.StoreID, Scope: doc.Scope, DocumentVersion: 1, Level: 2, ParentID: l1.ID, ParentL1ID: l1.ID, Content: "处置步骤", TotalChildren: 2, Title: "手册"}
	parents.parents[l1.ID], parents.parents[l2a.ID], parents.parents[l2b.ID] = l1, l2a, l2b

	hits := []domain.RetrievalHit{
		leafHit(doc, "l3-a1", l1.ID, l2a.ID, .9), leafHit(doc, "l3-a2", l1.ID, l2a.ID, .8),
		leafHit(doc, "l3-b1", l1.ID, l2b.ID, .7), leafHit(doc, "l3-b2", l1.ID, l2b.ID, .6),
	}
	leaves := &fakeLeafStore{search: LeafSearchResult{Hits: hits, DenseRanking: []string{"l3-a1"}, SparseRanking: []string{"l3-b1"}}}
	cache := newFakeCache()
	service := NewService(parents, leaves, cache, fakeEmbedder{}, fakeReranker{}, 6000)

	result, err := service.SearchKnowledge(context.Background(), "store-a", "ROI 为什么下降", domain.RetrievalConfig{
		TopK: 3, CandidateK: 4, DenseWeight: .55, SparseWeight: .45, RRFK: 60,
		MergeThreshold: .5, RerankEnabled: true, QueryRewriteStrategy: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Chunk.ID != l1.ID || result.Hits[0].Chunk.Level != 1 {
		t.Fatalf("expected one merged L1 parent, got %+v", result.Hits)
	}
	if len(result.Hits[0].MergedFrom) != 2 {
		t.Fatalf("L1 should record its two merged L2 children: %+v", result.Hits[0].MergedFrom)
	}
	if result.Trace.ParentCacheMisses != 3 || result.Trace.RerankModel != "fake-reranker" {
		t.Fatalf("unexpected retrieval trace: %+v", result.Trace)
	}
	if result.Trace.Backend != "milvus_postgresql_redis" {
		t.Fatalf("unexpected backend trace: %q", result.Trace.Backend)
	}
}

func TestServicePreservesFusedHitsWhenRerankerFails(t *testing.T) {
	parents := newFakeParentStore()
	doc := parents.addReadyDocument("store-a", ScopeStore)
	hit := leafHit(doc, "leaf", "", "", .8)
	service := NewService(parents, &fakeLeafStore{search: LeafSearchResult{Hits: []domain.RetrievalHit{hit}}}, nil, fakeEmbedder{}, failingReranker{}, 6000)

	result, err := service.SearchKnowledge(context.Background(), "store-a", "query", domain.RetrievalConfig{
		TopK: 1, CandidateK: 1, RerankEnabled: true, QueryRewriteStrategy: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Chunk.ID != "leaf" {
		t.Fatalf("fused hit was lost after reranker fallback: %+v", result.Hits)
	}
	if result.Trace.FallbackReason == "" {
		t.Fatal("reranker failure should be visible in the trace")
	}
}

type fakeParentStore struct {
	documents map[string]Document
	parents   map[string]Chunk
}

func newFakeParentStore() *fakeParentStore {
	return &fakeParentStore{documents: map[string]Document{}, parents: map[string]Chunk{}}
}
func (s *fakeParentStore) EnsureSchema(context.Context) error { return nil }
func (s *fakeParentStore) BeginDocument(_ context.Context, input IngestInput, hash string) (Document, bool, error) {
	doc := Document{ID: uuid.NewString(), StoreID: input.StoreID, Scope: input.Scope, Title: input.Title, SourceURI: input.SourceURI,
		MediaType: input.MediaType, Version: 1, ContentHash: hash, Status: StatusProcessing, Metadata: input.Metadata, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.documents[doc.ID] = doc
	return doc, false, nil
}
func (s *fakeParentStore) SaveParents(_ context.Context, _ Document, chunks []Chunk) error {
	for _, chunk := range chunks {
		s.parents[chunk.ID] = chunk
	}
	return nil
}
func (s *fakeParentStore) MarkDocument(_ context.Context, id, status, message string) error {
	doc := s.documents[id]
	doc.Status, doc.Error = status, message
	s.documents[id] = doc
	return nil
}
func (s *fakeParentStore) ReadyDocuments(_ context.Context, storeID string, ids []string) (map[string]bool, error) {
	result := map[string]bool{}
	for _, id := range ids {
		doc, ok := s.documents[id]
		if ok && doc.Status == StatusReady && (doc.Scope == ScopePlatform || doc.StoreID == storeID) {
			result[id] = true
		}
	}
	return result, nil
}
func (s *fakeParentStore) GetParent(_ context.Context, storeID, id string) (Chunk, error) {
	chunk, ok := s.parents[id]
	if !ok || (chunk.Scope == ScopeStore && chunk.StoreID != storeID) {
		return Chunk{}, errors.New("not found")
	}
	return chunk, nil
}
func (s *fakeParentStore) Close() {}
func (s *fakeParentStore) addReadyDocument(storeID, scope string) Document {
	doc := Document{ID: uuid.NewString(), StoreID: storeID, Scope: scope, Version: 1, Status: StatusReady}
	s.documents[doc.ID] = doc
	return doc
}

type fakeLeafStore struct {
	records []LeafRecord
	search  LeafSearchResult
}

func (s *fakeLeafStore) EnsureSchema(context.Context) error { return nil }
func (s *fakeLeafStore) Upsert(_ context.Context, records []LeafRecord) error {
	s.records = append(s.records, records...)
	return nil
}
func (s *fakeLeafStore) DeleteDocument(context.Context, string) error { return nil }
func (s *fakeLeafStore) Search(context.Context, string, string, []float32, domain.RetrievalConfig) (LeafSearchResult, error) {
	return s.search, nil
}
func (s *fakeLeafStore) Close(context.Context) error { return nil }

type fakeEmbedder struct{}

func (fakeEmbedder) EmbedDocuments(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range result {
		result[i] = []float32{float32(i), 1}
	}
	return result, nil
}
func (fakeEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return []float32{1, 1}, nil
}
func (fakeEmbedder) Model() string { return "fake-embedding" }

type fakeReranker struct{}

func (fakeReranker) Rerank(_ context.Context, _ string, hits []domain.RetrievalHit) ([]domain.RetrievalHit, error) {
	for i := range hits {
		hits[i].RerankScore = 1 - float64(i)*.1
	}
	return hits, nil
}
func (fakeReranker) Model() string { return "fake-reranker" }

type failingReranker struct{}

func (failingReranker) Rerank(context.Context, string, []domain.RetrievalHit) ([]domain.RetrievalHit, error) {
	return nil, errors.New("temporary provider error")
}
func (failingReranker) Model() string { return "failing-reranker" }

type fakeCache struct{ values map[string]Chunk }

func newFakeCache() *fakeCache { return &fakeCache{values: map[string]Chunk{}} }
func (c *fakeCache) Get(_ context.Context, storeID, id string) (Chunk, bool, error) {
	chunk, ok := c.values[fmt.Sprintf("%s/%s", storeID, id)]
	return chunk, ok, nil
}
func (c *fakeCache) Set(_ context.Context, storeID string, chunk Chunk) error {
	c.values[fmt.Sprintf("%s/%s", storeID, chunk.ID)] = chunk
	return nil
}
func (c *fakeCache) Close() error { return nil }

func leafHit(doc Document, id, l1, l2 string, score float64) domain.RetrievalHit {
	return domain.RetrievalHit{Chunk: domain.KnowledgeChunk{ID: id, DocID: doc.ID, StoreID: doc.StoreID, Scope: doc.Scope,
		DocumentVersion: doc.Version, Level: 3, ParentID: l2, ParentL1ID: l1, ParentL2ID: l2, Text: id}, RRFScore: score}
}

func repeatedRunes(length int) string {
	runes := make([]rune, length)
	for i := range runes {
		runes[i] = rune('一' + i%20)
	}
	return string(runes)
}
