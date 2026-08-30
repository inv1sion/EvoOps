package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/inv1sion/evoops/internal/dataset"
	"github.com/inv1sion/evoops/internal/domain"
)

const (
	ToolMetrics   = "get_business_metrics"
	ToolInventory = "get_inventory_risk"
	ToolCampaigns = "get_campaign_performance"
	ToolKnowledge = "search_operations_knowledge"
	ToolExecute   = "execute_operation"
)

type storeInput struct {
	StoreID string `json:"store_id" jsonschema:"description=Stable store identifier"`
}

type knowledgeInput struct {
	StoreID              string  `json:"store_id" jsonschema:"description=Stable store identifier"`
	Query                string  `json:"query" jsonschema:"description=Keywords describing the observed business signals"`
	TopK                 int     `json:"top_k" jsonschema:"description=Maximum number of evidence articles"`
	CandidateK           int     `json:"candidate_k" jsonschema:"description=Hybrid retrieval candidate pool size"`
	DenseWeight          float64 `json:"dense_weight" jsonschema:"description=Dense ranking weight"`
	SparseWeight         float64 `json:"sparse_weight" jsonschema:"description=Sparse BM25 ranking weight"`
	RRFK                 int     `json:"rrf_k" jsonschema:"description=Reciprocal rank fusion constant"`
	MergeThreshold       float64 `json:"merge_threshold" jsonschema:"description=Child coverage required for parent auto-merging"`
	RelevanceThreshold   float64 `json:"relevance_threshold" jsonschema:"description=Minimum rerank score before query rewriting"`
	RerankEnabled        bool    `json:"rerank_enabled" jsonschema:"description=Enable deterministic reranking"`
	QueryRewriteStrategy string  `json:"query_rewrite_strategy" jsonschema:"description=none step_back or hyde"`
}

func RegisterLocal(ctx context.Context, registry *Registry, repo dataset.Repository) error {
	metrics, err := utils.InferTool(ToolMetrics, "Read current and baseline business metrics for a store.",
		func(ctx context.Context, input storeInput) (domain.MetricsSnapshot, error) {
			store, err := repo.Store(ctx, input.StoreID)
			return store.Metrics, err
		})
	if err != nil {
		return err
	}
	inventory, err := utils.InferTool(ToolInventory, "Read SKU stock cover, stockout duration, and contribution risk.",
		func(ctx context.Context, input storeInput) ([]domain.InventoryItem, error) {
			store, err := repo.Store(ctx, input.StoreID)
			return store.Inventory, err
		})
	if err != nil {
		return err
	}
	campaigns, err := utils.InferTool(ToolCampaigns, "Read campaign spend, revenue, ROI, budget, and status.",
		func(ctx context.Context, input storeInput) ([]domain.Campaign, error) {
			store, err := repo.Store(ctx, input.StoreID)
			return store.Campaigns, err
		})
	if err != nil {
		return err
	}
	knowledge, err := utils.InferTool(ToolKnowledge, "Run hybrid dense and BM25 retrieval, reciprocal-rank fusion, hierarchical auto-merging, reranking, and optional query rewriting over operating playbooks.",
		func(ctx context.Context, input knowledgeInput) (domain.RetrievalResult, error) {
			return repo.SearchKnowledge(ctx, input.StoreID, input.Query, domain.RetrievalConfig{
				TopK: input.TopK, CandidateK: input.CandidateK, DenseWeight: input.DenseWeight,
				SparseWeight: input.SparseWeight, RRFK: input.RRFK, MergeThreshold: input.MergeThreshold,
				RelevanceThreshold: input.RelevanceThreshold, RerankEnabled: input.RerankEnabled,
				QueryRewriteStrategy: input.QueryRewriteStrategy,
			})
		})
	if err != nil {
		return err
	}
	execute, err := utils.InferTool(ToolExecute, "Execute an approved business operation. This tool must be protected by a human approval gate.",
		func(ctx context.Context, input dataset.OperationInput) (dataset.OperationReceipt, error) {
			if input.Action == "" || input.Target == "" {
				return dataset.OperationReceipt{}, fmt.Errorf("action and target are required")
			}
			return repo.Execute(ctx, input)
		})
	if err != nil {
		return err
	}
	if err := registry.Register(ctx, metrics); err != nil {
		return err
	}
	if err := registry.Register(ctx, inventory); err != nil {
		return err
	}
	if err := registry.Register(ctx, campaigns); err != nil {
		return err
	}
	if err := registry.Register(ctx, knowledge); err != nil {
		return err
	}
	return registry.Register(ctx, execute)
}
