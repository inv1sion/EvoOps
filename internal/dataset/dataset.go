package dataset

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
	"github.com/inv1sion/evoops/internal/retrieval"
)

type Repository interface {
	Store(context.Context, string) (domain.StoreData, error)
	SearchKnowledge(context.Context, string, string, domain.RetrievalConfig) (domain.RetrievalResult, error)
	Execute(context.Context, OperationInput) (OperationReceipt, error)
}

type KnowledgeSearcher interface {
	SearchKnowledge(context.Context, string, string, domain.RetrievalConfig) (domain.RetrievalResult, error)
}

type OperationInput struct {
	StoreID string         `json:"store_id"`
	Action  string         `json:"action"`
	Target  string         `json:"target"`
	Params  map[string]any `json:"params,omitempty"`
}

type OperationReceipt struct {
	ReceiptID string    `json:"receipt_id"`
	Status    string    `json:"status"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Executed  time.Time `json:"executed_at"`
}

type MemoryRepository struct {
	mu         sync.RWMutex
	stores     map[string]domain.StoreData
	knowledge  []domain.KnowledgeArticle
	operations []OperationReceipt
	retriever  *retrieval.Engine
	searcher   KnowledgeSearcher
}

func LoadFile(path string) (*MemoryRepository, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read demo data: %w", err)
	}
	var stores []domain.StoreData
	if err := json.Unmarshal(data, &stores); err != nil {
		return nil, fmt.Errorf("decode demo data: %w", err)
	}
	return NewMemory(stores), nil
}

func NewMemory(stores []domain.StoreData) *MemoryRepository {
	index := make(map[string]domain.StoreData, len(stores))
	var knowledge []domain.KnowledgeArticle
	for _, store := range stores {
		index[store.StoreID] = store
		if len(knowledge) == 0 && len(store.Knowledge) > 0 {
			knowledge = append([]domain.KnowledgeArticle(nil), store.Knowledge...)
		}
	}
	return &MemoryRepository{stores: index, knowledge: knowledge, retriever: retrieval.New()}
}

func (r *MemoryRepository) Store(_ context.Context, id string) (domain.StoreData, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	store, ok := r.stores[id]
	if !ok {
		return domain.StoreData{}, fmt.Errorf("store %q not found", id)
	}
	return store, nil
}

func (r *MemoryRepository) SearchKnowledge(ctx context.Context, storeID, query string, cfg domain.RetrievalConfig) (domain.RetrievalResult, error) {
	store, err := r.Store(ctx, storeID)
	if err != nil {
		return domain.RetrievalResult{}, err
	}
	r.mu.RLock()
	searcher := r.searcher
	r.mu.RUnlock()
	if searcher != nil {
		return searcher.SearchKnowledge(ctx, storeID, query, cfg)
	}
	knowledge := store.Knowledge
	if len(knowledge) == 0 {
		knowledge = r.knowledge
	}
	return r.retriever.Search(ctx, knowledge, query, cfg), nil
}

func (r *MemoryRepository) SetKnowledgeSearcher(searcher KnowledgeSearcher) {
	r.mu.Lock()
	r.searcher = searcher
	r.mu.Unlock()
}

func (r *MemoryRepository) PlatformKnowledge() []domain.KnowledgeArticle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]domain.KnowledgeArticle(nil), r.knowledge...)
}

func (r *MemoryRepository) Execute(_ context.Context, input OperationInput) (OperationReceipt, error) {
	if _, err := r.Store(context.Background(), input.StoreID); err != nil {
		return OperationReceipt{}, err
	}
	receipt := OperationReceipt{
		ReceiptID: "op-" + uuid.NewString(),
		Status:    "accepted",
		Action:    input.Action,
		Target:    input.Target,
		Executed:  time.Now().UTC(),
	}
	r.mu.Lock()
	r.operations = append(r.operations, receipt)
	r.mu.Unlock()
	return receipt, nil
}
