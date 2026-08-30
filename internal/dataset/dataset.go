package dataset

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/inv1sion/evoops/internal/domain"
)

type Repository interface {
	Store(context.Context, string) (domain.StoreData, error)
	SearchKnowledge(context.Context, string, string, int) ([]domain.KnowledgeArticle, error)
	Execute(context.Context, OperationInput) (OperationReceipt, error)
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
	operations []OperationReceipt
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
	for _, store := range stores {
		index[store.StoreID] = store
	}
	return &MemoryRepository{stores: index}
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

func (r *MemoryRepository) SearchKnowledge(ctx context.Context, storeID, query string, topK int) ([]domain.KnowledgeArticle, error) {
	store, err := r.Store(ctx, storeID)
	if err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 3
	}
	terms := tokenize(query)
	type scored struct {
		article domain.KnowledgeArticle
		score   int
	}
	items := make([]scored, 0, len(store.Knowledge))
	for _, article := range store.Knowledge {
		haystack := strings.ToLower(article.Title + " " + article.Content + " " + strings.Join(article.Tags, " "))
		score := 0
		for _, term := range terms {
			if strings.Contains(haystack, term) {
				score++
			}
		}
		if score > 0 {
			items = append(items, scored{article: article, score: score})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	if len(items) > topK {
		items = items[:topK]
	}
	result := make([]domain.KnowledgeArticle, len(items))
	for i := range items {
		result[i] = items[i].article
	}
	return result, nil
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

func tokenize(value string) []string {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, value)
	seen := map[string]bool{}
	var terms []string
	for _, field := range strings.Fields(normalized) {
		if len([]rune(field)) < 2 || seen[field] {
			continue
		}
		seen[field] = true
		terms = append(terms, field)
	}
	return terms
}
