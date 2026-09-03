package rag

import (
	"context"
	"time"

	"github.com/inv1sion/evoops/internal/domain"
)

const (
	ScopePlatform = "platform"
	ScopeStore    = "store"

	StatusProcessing = "processing"
	StatusReady      = "ready"
	StatusFailed     = "failed"
)

type ChunkSize struct {
	Size    int `json:"size"`
	Overlap int `json:"overlap"`
}

type ChunkingConfig struct {
	L1 ChunkSize `json:"l1"`
	L2 ChunkSize `json:"l2"`
	L3 ChunkSize `json:"l3"`
}

func MedQAChunkingConfig() ChunkingConfig {
	return ChunkingConfig{
		L1: ChunkSize{Size: 1200, Overlap: 240},
		L2: ChunkSize{Size: 600, Overlap: 120},
		L3: ChunkSize{Size: 300, Overlap: 60},
	}
}

type Document struct {
	ID          string         `json:"id"`
	StoreID     string         `json:"store_id,omitempty"`
	Scope       string         `json:"scope"`
	Title       string         `json:"title"`
	SourceURI   string         `json:"source_uri"`
	MediaType   string         `json:"media_type"`
	Version     int64          `json:"version"`
	ContentHash string         `json:"content_hash"`
	Status      string         `json:"status"`
	Error       string         `json:"error,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type Chunk struct {
	ID              string         `json:"id"`
	DocID           string         `json:"doc_id"`
	StoreID         string         `json:"store_id,omitempty"`
	Scope           string         `json:"scope"`
	DocumentVersion int64          `json:"document_version"`
	Level           int            `json:"level"`
	Content         string         `json:"content"`
	ChunkIndex      int            `json:"chunk_index"`
	StartChar       int            `json:"start_char"`
	EndChar         int            `json:"end_char"`
	TotalChildren   int            `json:"total_children"`
	ParentID        string         `json:"parent_id,omitempty"`
	ParentL1ID      string         `json:"parent_l1_id,omitempty"`
	ParentL2ID      string         `json:"parent_l2_id,omitempty"`
	Title           string         `json:"title"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type ChunkTree struct {
	L1 []Chunk `json:"l1"`
	L2 []Chunk `json:"l2"`
	L3 []Chunk `json:"l3"`
}

type IngestInput struct {
	StoreID   string
	Scope     string
	Title     string
	SourceURI string
	MediaType string
	Content   string
	Metadata  map[string]any
}

type IngestResult struct {
	Document Document `json:"document"`
	L1Count  int      `json:"l1_count"`
	L2Count  int      `json:"l2_count"`
	L3Count  int      `json:"l3_count"`
	Reused   bool     `json:"reused"`
}

type LeafRecord struct {
	Chunk Chunk
	Dense []float32
}

type LeafSearchResult struct {
	Hits           []domain.RetrievalHit
	DenseRanking   []string
	SparseRanking  []string
	DenseFallback  bool
	FallbackReason string
}

type ParentStore interface {
	EnsureSchema(context.Context) error
	BeginDocument(context.Context, IngestInput, string) (Document, bool, error)
	SaveParents(context.Context, Document, []Chunk) error
	MarkDocument(context.Context, string, string, string) error
	ReadyDocuments(context.Context, string, []string) (map[string]bool, error)
	GetParent(context.Context, string, string) (Chunk, error)
	Close()
}

type LeafStore interface {
	EnsureSchema(context.Context) error
	Upsert(context.Context, []LeafRecord) error
	DeleteDocument(context.Context, string) error
	Search(context.Context, string, string, []float32, domain.RetrievalConfig) (LeafSearchResult, error)
	Close(context.Context) error
}

type ParentCache interface {
	Get(context.Context, string, string) (Chunk, bool, error)
	Set(context.Context, string, Chunk) error
	Close() error
}

type Embedder interface {
	EmbedDocuments(context.Context, []string) ([][]float32, error)
	EmbedQuery(context.Context, string) ([]float32, error)
	Model() string
}

type Reranker interface {
	Rerank(context.Context, string, []domain.RetrievalHit) ([]domain.RetrievalHit, error)
	Model() string
}
