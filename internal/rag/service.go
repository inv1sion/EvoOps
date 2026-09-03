package rag

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/inv1sion/evoops/internal/domain"
)

type Service struct {
	parents         ParentStore
	leaves          LeafStore
	cache           ParentCache
	embedder        Embedder
	reranker        Reranker
	chunking        ChunkingConfig
	maxContextChars int
}

func NewService(parents ParentStore, leaves LeafStore, cache ParentCache, embedder Embedder, reranker Reranker, maxContextChars int) *Service {
	if maxContextChars <= 0 {
		maxContextChars = 6000
	}
	return &Service{parents: parents, leaves: leaves, cache: cache, embedder: embedder, reranker: reranker,
		chunking: MedQAChunkingConfig(), maxContextChars: maxContextChars}
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	if err := s.parents.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("initialize PostgreSQL RAG schema: %w", err)
	}
	if err := s.leaves.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("initialize Milvus RAG schema: %w", err)
	}
	return nil
}

func (s *Service) Ingest(ctx context.Context, input IngestInput) (result IngestResult, err error) {
	input.Content = strings.TrimSpace(input.Content)
	if input.Scope != ScopePlatform && input.Scope != ScopeStore {
		return result, fmt.Errorf("scope must be platform or store")
	}
	if input.Scope == ScopeStore && strings.TrimSpace(input.StoreID) == "" {
		return result, fmt.Errorf("store scope requires store_id")
	}
	if input.Scope == ScopePlatform {
		input.StoreID = ""
	}
	if input.Title == "" || input.SourceURI == "" || input.Content == "" {
		return result, fmt.Errorf("title, source URI and content are required")
	}
	digest := sha256.Sum256([]byte(input.Content))
	document, reused, err := s.parents.BeginDocument(ctx, input, fmt.Sprintf("%x", digest))
	if err != nil {
		return result, err
	}
	result.Document, result.Reused = document, reused
	if reused {
		return result, nil
	}
	failed := true
	defer func() {
		if !failed {
			return
		}
		message := "ingestion failed"
		if err != nil {
			message = err.Error()
		}
		if len(message) > 1000 {
			message = message[:1000]
		}
		_ = s.parents.MarkDocument(context.Background(), document.ID, StatusFailed, message)
		_ = s.leaves.DeleteDocument(context.Background(), document.ID)
	}()
	tree, err := ChunkDocument(document, input.Content, s.chunking)
	if err != nil {
		return result, err
	}
	texts := make([]string, len(tree.L3))
	for index := range tree.L3 {
		texts[index] = tree.L3[index].Content
	}
	vectors, err := s.embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return result, err
	}
	if len(vectors) != len(tree.L3) {
		return result, fmt.Errorf("embedding result count %d does not match L3 chunk count %d", len(vectors), len(tree.L3))
	}
	parents := append(append([]Chunk{}, tree.L1...), tree.L2...)
	if err = s.parents.SaveParents(ctx, document, parents); err != nil {
		return result, err
	}
	records := make([]LeafRecord, len(tree.L3))
	for index := range tree.L3 {
		records[index] = LeafRecord{Chunk: tree.L3[index], Dense: vectors[index]}
	}
	if err = s.leaves.Upsert(ctx, records); err != nil {
		return result, err
	}
	if err = s.parents.MarkDocument(ctx, document.ID, StatusReady, ""); err != nil {
		return result, err
	}
	failed = false
	result.Document.Status = StatusReady
	result.L1Count, result.L2Count, result.L3Count = len(tree.L1), len(tree.L2), len(tree.L3)
	return result, nil
}

func (s *Service) SearchKnowledge(ctx context.Context, storeID, query string, cfg domain.RetrievalConfig) (domain.RetrievalResult, error) {
	started := time.Now()
	query = strings.TrimSpace(query)
	if storeID == "" || query == "" {
		return domain.RetrievalResult{}, fmt.Errorf("store_id and query are required")
	}
	cfg = normalizeRetrieval(cfg)
	result, err := s.searchOnce(ctx, storeID, query, cfg)
	if err != nil {
		return result, err
	}
	result.Trace.OriginalQuery, result.Trace.EffectiveQuery = query, query
	if cfg.QueryRewriteStrategy != "none" && (len(result.Hits) == 0 || result.Hits[0].RerankScore < cfg.RelevanceThreshold) {
		rewritten := rewriteQuery(query, cfg.QueryRewriteStrategy)
		candidate, candidateErr := s.searchOnce(ctx, storeID, rewritten, cfg)
		if candidateErr == nil && bestRerank(candidate.Hits) > bestRerank(result.Hits) {
			result = candidate
			result.Trace.OriginalQuery, result.Trace.EffectiveQuery = query, rewritten
			result.Trace.RewriteUsed = true
			result.Trace.RewriteReason = "top rerank score was below the policy threshold"
		}
	}
	result.Trace.DurationMS = time.Since(started).Milliseconds()
	result.Cost = round(0.5 + float64(cfg.CandidateK)*0.01)
	if s.reranker != nil && cfg.RerankEnabled {
		result.Cost = round(result.Cost + 0.2)
	}
	if result.Trace.RewriteUsed {
		result.Cost = round(result.Cost + 0.3)
	}
	return result, nil
}

func (s *Service) searchOnce(ctx context.Context, storeID, query string, cfg domain.RetrievalConfig) (domain.RetrievalResult, error) {
	vector, err := s.embedder.EmbedQuery(ctx, query)
	if err != nil {
		return domain.RetrievalResult{}, fmt.Errorf("embed query: %w", err)
	}
	leaves, err := s.leaves.Search(ctx, storeID, query, vector, cfg)
	if err != nil {
		return domain.RetrievalResult{}, err
	}
	docIDs := make([]string, 0, len(leaves.Hits))
	seenDocs := map[string]bool{}
	for _, hit := range leaves.Hits {
		if !seenDocs[hit.Chunk.DocID] {
			docIDs = append(docIDs, hit.Chunk.DocID)
			seenDocs[hit.Chunk.DocID] = true
		}
	}
	ready, err := s.parents.ReadyDocuments(ctx, storeID, docIDs)
	if err != nil {
		return domain.RetrievalResult{}, fmt.Errorf("verify document state: %w", err)
	}
	filtered := make([]domain.RetrievalHit, 0, len(leaves.Hits))
	for _, hit := range leaves.Hits {
		if ready[hit.Chunk.DocID] {
			filtered = append(filtered, hit)
		}
	}
	fallbackReason := leaves.FallbackReason
	if s.reranker != nil && cfg.RerankEnabled && len(filtered) > 0 {
		reranked, rerankErr := s.reranker.Rerank(ctx, query, filtered)
		if rerankErr != nil {
			fallbackReason = joinReason(fallbackReason, "rerank fallback: "+rerankErr.Error())
			normalizeRRF(filtered)
		} else {
			filtered = reranked
		}
	} else {
		normalizeRRF(filtered)
	}
	stats := mergeStats{}
	filtered = s.autoMerge(ctx, storeID, filtered, cfg.MergeThreshold, &stats)
	filtered = enforceContextBudget(filtered, cfg.TopK, s.maxContextChars)
	trace := domain.RetrievalTrace{Backend: "milvus_postgresql_redis", EmbeddingModel: s.embedder.Model(), DenseRanking: leaves.DenseRanking,
		SparseRanking: leaves.SparseRanking, FusedRanking: hitIDs(leaves.Hits), MergedIDs: stats.merged, FinalRanking: hitIDs(filtered),
		DenseOnlyFallback: leaves.DenseFallback, FallbackReason: fallbackReason, ParentCacheHits: stats.cacheHits,
		ParentCacheMisses: stats.cacheMisses, ContextCharacters: contextCharacters(filtered)}
	if s.reranker != nil && cfg.RerankEnabled {
		trace.RerankModel = s.reranker.Model()
	}
	return domain.RetrievalResult{Hits: filtered, Trace: trace}, nil
}

type mergeStats struct {
	merged                 []string
	cacheHits, cacheMisses int
}

func (s *Service) autoMerge(ctx context.Context, storeID string, hits []domain.RetrievalHit, threshold float64, stats *mergeStats) []domain.RetrievalHit {
	hits = s.mergeGroups(ctx, storeID, hits, threshold, 3, func(hit domain.RetrievalHit) string { return hit.Chunk.ParentL2ID }, stats)
	return s.mergeGroups(ctx, storeID, hits, threshold, 2, func(hit domain.RetrievalHit) string {
		if hit.Chunk.Level == 2 {
			return hit.Chunk.ParentID
		}
		return ""
	}, stats)
}

func (s *Service) mergeGroups(ctx context.Context, storeID string, hits []domain.RetrievalHit, threshold float64, childLevel int, parentID func(domain.RetrievalHit) string, stats *mergeStats) []domain.RetrievalHit {
	groups := map[string][]int{}
	for index, hit := range hits {
		if hit.Chunk.Level == childLevel && parentID(hit) != "" {
			groups[parentID(hit)] = append(groups[parentID(hit)], index)
		}
	}
	replacements := map[string]domain.RetrievalHit{}
	for id, indexes := range groups {
		if len(indexes) < 2 {
			continue
		}
		parent, ok := s.loadParent(ctx, storeID, id, stats)
		if !ok || parent.TotalChildren <= 0 || float64(len(indexes))/float64(parent.TotalChildren) < threshold {
			continue
		}
		best := hits[indexes[0]]
		mergedFrom := make([]string, 0, len(indexes))
		for _, index := range indexes {
			mergedFrom = append(mergedFrom, hits[index].Chunk.ID)
			if hits[index].RerankScore > best.RerankScore {
				best = hits[index]
			}
		}
		best.Chunk = domainChunk(parent)
		best.MergedFrom = mergedFrom
		replacements[id] = best
		stats.merged = append(stats.merged, id)
	}
	seen := map[string]bool{}
	result := make([]domain.RetrievalHit, 0, len(hits))
	for _, hit := range hits {
		id := parentID(hit)
		if replacement, ok := replacements[id]; ok {
			if !seen[id] {
				result = append(result, replacement)
				seen[id] = true
			}
			continue
		}
		result = append(result, hit)
	}
	return result
}

func (s *Service) loadParent(ctx context.Context, storeID, id string, stats *mergeStats) (Chunk, bool) {
	if s.cache != nil {
		if chunk, ok, err := s.cache.Get(ctx, storeID, id); err == nil && ok {
			stats.cacheHits++
			return chunk, true
		}
	}
	stats.cacheMisses++
	chunk, err := s.parents.GetParent(ctx, storeID, id)
	if err != nil {
		return Chunk{}, false
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, storeID, chunk)
	}
	return chunk, true
}

func domainChunk(chunk Chunk) domain.KnowledgeChunk {
	return domain.KnowledgeChunk{ID: chunk.ID, DocID: chunk.DocID, ParentID: chunk.ParentID,
		ParentL1ID: chunk.ParentL1ID, ParentL2ID: chunk.ParentL2ID, StoreID: chunk.StoreID, Scope: chunk.Scope, DocumentVersion: chunk.DocumentVersion,
		Level: chunk.Level, ChunkIndex: chunk.ChunkIndex, Title: chunk.Title, Text: chunk.Content, Metadata: chunk.Metadata}
}

func enforceContextBudget(hits []domain.RetrievalHit, topK, maxChars int) []domain.RetrievalHit {
	if topK <= 0 {
		topK = 3
	}
	result := make([]domain.RetrievalHit, 0, min(topK, len(hits)))
	used := 0
	for _, hit := range hits {
		size := utf8.RuneCountInString(hit.Chunk.Text)
		if len(result) >= topK {
			break
		}
		if used+size > maxChars && len(result) > 0 {
			continue
		}
		result = append(result, hit)
		used += size
	}
	return result
}

func normalizeRRF(hits []domain.RetrievalHit) {
	maxScore := 0.0
	for _, hit := range hits {
		if hit.RRFScore > maxScore {
			maxScore = hit.RRFScore
		}
	}
	for index := range hits {
		if maxScore > 0 {
			hits[index].RerankScore = round(hits[index].RRFScore / maxScore)
		}
	}
}
func contextCharacters(hits []domain.RetrievalHit) int {
	total := 0
	for _, hit := range hits {
		total += utf8.RuneCountInString(hit.Chunk.Text)
	}
	return total
}
func hitIDs(hits []domain.RetrievalHit) []string {
	result := make([]string, len(hits))
	for i := range hits {
		result[i] = hits[i].Chunk.ID
	}
	return result
}
func bestRerank(hits []domain.RetrievalHit) float64 {
	if len(hits) == 0 {
		return 0
	}
	return hits[0].RerankScore
}
func joinReason(first, second string) string {
	if first == "" {
		return second
	}
	return first + "; " + second
}
func rewriteQuery(query, strategy string) string {
	if strategy == "hyde" {
		return query + " 可能原因 证据 检查项 广告投放处置"
	}
	return query + " 广告经营异常 归因分析 处置手册"
}
func normalizeRetrieval(cfg domain.RetrievalConfig) domain.RetrievalConfig {
	if cfg.TopK <= 0 {
		cfg.TopK = 3
	}
	if cfg.CandidateK < cfg.TopK {
		cfg.CandidateK = cfg.TopK * 4
	}
	if cfg.DenseWeight <= 0 && cfg.SparseWeight <= 0 {
		cfg.DenseWeight = .55
		cfg.SparseWeight = .45
	}
	total := cfg.DenseWeight + cfg.SparseWeight
	cfg.DenseWeight /= total
	cfg.SparseWeight /= total
	if cfg.RRFK <= 0 {
		cfg.RRFK = 60
	}
	if cfg.MergeThreshold <= 0 {
		cfg.MergeThreshold = .5
	}
	if cfg.RelevanceThreshold <= 0 {
		cfg.RelevanceThreshold = .45
	}
	if cfg.QueryRewriteStrategy == "" {
		cfg.QueryRewriteStrategy = "step_back"
	}
	return cfg
}

func (s *Service) Close(ctx context.Context) error {
	var errors []string
	if s.cache != nil {
		if err := s.cache.Close(); err != nil {
			errors = append(errors, err.Error())
		}
	}
	s.parents.Close()
	if err := s.leaves.Close(ctx); err != nil {
		errors = append(errors, err.Error())
	}
	if len(errors) > 0 {
		return fmt.Errorf("close RAG services: %s", strings.Join(errors, "; "))
	}
	return nil
}
