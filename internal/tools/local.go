package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/inv1sion/evoops/internal/dataset"
	"github.com/inv1sion/evoops/internal/domain"
)

const (
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
	RerankEnabled        bool    `json:"rerank_enabled" jsonschema:"description=Enable the configured local or Qwen reranker"`
	QueryRewriteStrategy string  `json:"query_rewrite_strategy" jsonschema:"description=none step_back or hyde"`
}

func RegisterLocal(ctx context.Context, registry *Registry, repo dataset.Repository) error {
	campaigns, err := utils.InferTool(ToolCampaigns, "读取广告计划状态、消耗、成交额、当前 ROI 与历史 ROI。",
		func(ctx context.Context, input storeInput) ([]domain.Campaign, error) {
			store, err := repo.Store(ctx, input.StoreID)
			return store.Campaigns, err
		})
	if err != nil {
		return err
	}
	knowledge, err := utils.InferTool(ToolKnowledge, "检索广告投放诊断与低 ROI 处置手册；返回引用证据与完整检索轨迹。",
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
	execute, err := utils.InferTool(ToolExecute, "执行广告诊断任务或经人工审批的广告暂停操作。",
		func(ctx context.Context, input dataset.OperationInput) (dataset.OperationReceipt, error) {
			if input.Action == "" || input.Target == "" {
				return dataset.OperationReceipt{}, fmt.Errorf("action and target are required")
			}
			return repo.Execute(ctx, input)
		})
	if err != nil {
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
