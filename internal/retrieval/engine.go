package retrieval

import (
	"context"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/inv1sion/evoops/internal/domain"
)

const vectorDimensions = 192

type Engine struct{}

func New() *Engine { return &Engine{} }

func (e *Engine) Search(_ context.Context, articles []domain.KnowledgeArticle, query string, cfg domain.RetrievalConfig) domain.RetrievalResult {
	started := time.Now()
	cfg = normalizeConfig(cfg)
	corpus, parents, childCount := buildCorpus(articles)
	first := searchPass(corpus, parents, childCount, query, cfg)
	first.Trace.OriginalQuery = query
	first.Trace.EffectiveQuery = query

	if shouldRewrite(first, cfg) && cfg.QueryRewriteStrategy != "none" {
		rewritten := rewrite(query, cfg.QueryRewriteStrategy)
		second := searchPass(corpus, parents, childCount, rewritten, cfg)
		if bestScore(second.Hits) >= bestScore(first.Hits) {
			second.Trace.OriginalQuery = query
			second.Trace.EffectiveQuery = rewritten
			second.Trace.RewriteUsed = true
			second.Trace.RewriteReason = "top relevance score was below the policy threshold"
			first = second
		}
	}
	first.Trace.DurationMS = time.Since(started).Milliseconds()
	first.Cost = round(0.2 + float64(cfg.CandidateK)*0.01 + float64(len(tokenize(first.Trace.EffectiveQuery)))*0.005)
	if cfg.RerankEnabled {
		first.Cost = round(first.Cost + 0.1)
	}
	if first.Trace.RewriteUsed {
		first.Cost = round(first.Cost + 0.15)
	}
	return first
}

func searchPass(corpus []domain.KnowledgeChunk, parents map[string]domain.KnowledgeChunk, childCount map[string]int, query string, cfg domain.RetrievalConfig) domain.RetrievalResult {
	queryTerms := expandTokens(tokenize(query))
	docTerms := make(map[string][]string, len(corpus))
	for _, chunk := range corpus {
		docTerms[chunk.ID] = expandTokens(tokenize(chunk.Title + " " + chunk.Text + " " + strings.Join(chunk.Tags, " ")))
	}
	sparseScores := bm25(queryTerms, corpus, docTerms)
	denseScores := make(map[string]float64, len(corpus))
	queryVector := vectorize(queryTerms)
	for _, chunk := range corpus {
		denseScores[chunk.ID] = cosine(queryVector, vectorize(docTerms[chunk.ID]))
	}
	denseRank := rank(corpus, denseScores)
	sparseRank := rank(corpus, sparseScores)
	fused := fuse(corpus, denseRank, sparseRank, denseScores, sparseScores, cfg)
	if len(fused) > cfg.CandidateK {
		fused = fused[:cfg.CandidateK]
	}
	merged, mergedIDs := autoMerge(fused, parents, childCount, cfg.MergeThreshold)
	rerankHits(merged, query, queryTerms, cfg.RerankEnabled)
	if len(merged) > cfg.TopK {
		merged = merged[:cfg.TopK]
	}
	trace := domain.RetrievalTrace{
		DenseRanking:  ids(denseRank, cfg.CandidateK),
		SparseRanking: ids(sparseRank, cfg.CandidateK),
		FusedRanking:  hitIDs(fused),
		MergedIDs:     mergedIDs,
		FinalRanking:  hitIDs(merged),
	}
	return domain.RetrievalResult{Hits: merged, Trace: trace}
}

func buildCorpus(articles []domain.KnowledgeArticle) ([]domain.KnowledgeChunk, map[string]domain.KnowledgeChunk, map[string]int) {
	var leaves []domain.KnowledgeChunk
	parents := make(map[string]domain.KnowledgeChunk, len(articles))
	childCount := make(map[string]int, len(articles))
	for _, article := range articles {
		parent := domain.KnowledgeChunk{ID: article.ID, DocID: article.ID, Level: 1, Title: article.Title, Text: article.Content, Tags: article.Tags}
		parents[parent.ID] = parent
		parts := splitContent(article.Content)
		for i, part := range parts {
			leaf := domain.KnowledgeChunk{
				ID: article.ID + "#s" + itoa(i+1), DocID: article.ID, ParentID: article.ID,
				Level: 3, Title: article.Title, Text: part, Tags: article.Tags,
			}
			leaves = append(leaves, leaf)
			childCount[article.ID]++
		}
	}
	return leaves, parents, childCount
}

func splitContent(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '.' || r == '。' || r == ';' || r == '；' || r == '\n' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	if len(result) == 0 && strings.TrimSpace(value) != "" {
		return []string{strings.TrimSpace(value)}
	}
	return result
}

func bm25(query []string, corpus []domain.KnowledgeChunk, terms map[string][]string) map[string]float64 {
	df := map[string]int{}
	totalLength := 0
	for _, chunk := range corpus {
		seen := map[string]bool{}
		for _, term := range terms[chunk.ID] {
			if !seen[term] {
				df[term]++
				seen[term] = true
			}
		}
		totalLength += len(terms[chunk.ID])
	}
	avgLength := float64(totalLength) / math.Max(1, float64(len(corpus)))
	scores := make(map[string]float64, len(corpus))
	const k1, b = 1.2, 0.75
	for _, chunk := range corpus {
		frequencies := map[string]int{}
		for _, term := range terms[chunk.ID] {
			frequencies[term]++
		}
		length := float64(len(terms[chunk.ID]))
		for _, term := range query {
			tf := float64(frequencies[term])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(corpus))-float64(df[term])+0.5)/(float64(df[term])+0.5))
			scores[chunk.ID] += idf * (tf * (k1 + 1)) / (tf + k1*(1-b+b*length/avgLength))
		}
	}
	return scores
}

func fuse(corpus []domain.KnowledgeChunk, denseRank, sparseRank []domain.KnowledgeChunk, dense, sparse map[string]float64, cfg domain.RetrievalConfig) []domain.RetrievalHit {
	densePosition := positions(denseRank)
	sparsePosition := positions(sparseRank)
	hits := make([]domain.RetrievalHit, 0, len(corpus))
	for _, chunk := range corpus {
		dp, dok := densePosition[chunk.ID]
		sp, sok := sparsePosition[chunk.ID]
		score := 0.0
		if dok {
			score += cfg.DenseWeight / float64(cfg.RRFK+dp)
		}
		if sok {
			score += cfg.SparseWeight / float64(cfg.RRFK+sp)
		}
		if dense[chunk.ID] == 0 && sparse[chunk.ID] == 0 {
			continue
		}
		hits = append(hits, domain.RetrievalHit{Chunk: chunk, DenseScore: round(dense[chunk.ID]), SparseScore: round(sparse[chunk.ID]), RRFScore: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].RRFScore > hits[j].RRFScore })
	for i := range hits {
		hits[i].RRFScore = round(hits[i].RRFScore)
	}
	return hits
}

func autoMerge(hits []domain.RetrievalHit, parents map[string]domain.KnowledgeChunk, childCount map[string]int, threshold float64) ([]domain.RetrievalHit, []string) {
	selected := map[string][]domain.RetrievalHit{}
	for _, hit := range hits {
		selected[hit.Chunk.ParentID] = append(selected[hit.Chunk.ParentID], hit)
	}
	mergedParents := map[string]domain.RetrievalHit{}
	var mergedIDs []string
	for parentID, children := range selected {
		if parentID == "" || float64(len(children))/float64(max(1, childCount[parentID])) < threshold {
			continue
		}
		parent, ok := parents[parentID]
		if !ok {
			continue
		}
		best := children[0]
		from := make([]string, 0, len(children))
		for _, child := range children {
			from = append(from, child.Chunk.ID)
			if child.RRFScore > best.RRFScore {
				best = child
			}
		}
		best.Chunk = parent
		best.MergedFrom = from
		mergedParents[parentID] = best
		mergedIDs = append(mergedIDs, parentID)
	}
	seenParent := map[string]bool{}
	result := make([]domain.RetrievalHit, 0, len(hits))
	for _, hit := range hits {
		if replacement, ok := mergedParents[hit.Chunk.ParentID]; ok {
			if !seenParent[hit.Chunk.ParentID] {
				result = append(result, replacement)
				seenParent[hit.Chunk.ParentID] = true
			}
			continue
		}
		result = append(result, hit)
	}
	return result, mergedIDs
}

func rerankHits(hits []domain.RetrievalHit, query string, queryTerms []string, enabled bool) {
	maxRRF := 0.0
	for _, hit := range hits {
		if hit.RRFScore > maxRRF {
			maxRRF = hit.RRFScore
		}
	}
	for i := range hits {
		if !enabled {
			hits[i].RerankScore = hits[i].RRFScore
			continue
		}
		docTerms := expandTokens(tokenize(hits[i].Chunk.Title + " " + hits[i].Chunk.Text + " " + strings.Join(hits[i].Chunk.Tags, " ")))
		coverage := overlap(queryTerms, docTerms)
		normalizedRRF := 0.0
		if maxRRF > 0 {
			normalizedRRF = hits[i].RRFScore / maxRRF
		}
		phraseMatch := businessPhraseMatch(query, hits[i].Chunk.Tags)
		hits[i].RerankScore = round(0.35*normalizedRRF + 0.25*coverage + 0.15*cosine(vectorize(queryTerms), vectorize(docTerms)) + 0.25*phraseMatch)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].RerankScore > hits[j].RerankScore })
}

func businessPhraseMatch(query string, tags []string) float64 {
	normalizedQuery := strings.Join(tokenize(query), " ")
	for _, tag := range tags {
		normalizedTag := strings.Join(tokenize(tag), " ")
		if strings.Contains(normalizedTag, " ") && strings.Contains(normalizedQuery, normalizedTag) {
			return 1
		}
	}
	return 0
}

func shouldRewrite(result domain.RetrievalResult, cfg domain.RetrievalConfig) bool {
	return len(result.Hits) == 0 || bestScore(result.Hits) < cfg.RelevanceThreshold
}

func rewrite(query, strategy string) string {
	switch strategy {
	case "hyde":
		return query + " likely root causes evidence diagnostic checks recommended action playbook"
	case "step_back":
		return query + " business operations anomaly root cause playbook"
	default:
		return query
	}
}

func normalizeConfig(cfg domain.RetrievalConfig) domain.RetrievalConfig {
	if cfg.TopK <= 0 {
		cfg.TopK = 3
	}
	if cfg.CandidateK < cfg.TopK {
		cfg.CandidateK = cfg.TopK * 4
	}
	if cfg.DenseWeight <= 0 && cfg.SparseWeight <= 0 {
		cfg.DenseWeight, cfg.SparseWeight = 0.55, 0.45
	}
	total := cfg.DenseWeight + cfg.SparseWeight
	cfg.DenseWeight, cfg.SparseWeight = cfg.DenseWeight/total, cfg.SparseWeight/total
	if cfg.RRFK <= 0 {
		cfg.RRFK = 60
	}
	if cfg.MergeThreshold <= 0 {
		cfg.MergeThreshold = 0.5
	}
	if cfg.RelevanceThreshold <= 0 {
		cfg.RelevanceThreshold = 0.45
	}
	if cfg.QueryRewriteStrategy == "" {
		cfg.QueryRewriteStrategy = "step_back"
	}
	return cfg
}

func rank(corpus []domain.KnowledgeChunk, scores map[string]float64) []domain.KnowledgeChunk {
	result := append([]domain.KnowledgeChunk(nil), corpus...)
	sort.SliceStable(result, func(i, j int) bool {
		if scores[result[i].ID] == scores[result[j].ID] {
			return result[i].ID < result[j].ID
		}
		return scores[result[i].ID] > scores[result[j].ID]
	})
	return result
}

func positions(ranking []domain.KnowledgeChunk) map[string]int {
	result := make(map[string]int, len(ranking))
	for i, chunk := range ranking {
		result[chunk.ID] = i + 1
	}
	return result
}

func ids(chunks []domain.KnowledgeChunk, limit int) []string {
	if limit > 0 && len(chunks) > limit {
		chunks = chunks[:limit]
	}
	result := make([]string, len(chunks))
	for i := range chunks {
		result[i] = chunks[i].ID
	}
	return result
}

func hitIDs(hits []domain.RetrievalHit) []string {
	result := make([]string, len(hits))
	for i := range hits {
		result[i] = hits[i].Chunk.ID
	}
	return result
}

func bestScore(hits []domain.RetrievalHit) float64 {
	if len(hits) == 0 {
		return 0
	}
	return hits[0].RerankScore
}

func tokenize(value string) []string {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, value)
	seen := map[string]bool{}
	var result []string
	for _, field := range strings.Fields(normalized) {
		if len([]rune(field)) < 2 || seen[field] {
			continue
		}
		seen[field] = true
		result = append(result, field)
	}
	return result
}

var synonyms = map[string][]string{
	"campaign":   {"advertising", "ads", "spend", "marketing"},
	"roi":        {"return", "spend", "efficiency"},
	"conversion": {"funnel", "checkout", "purchase"},
	"traffic":    {"visitors", "organic", "paid", "attribution"},
	"stockout":   {"inventory", "replenishment", "supply"},
	"refund":     {"quality", "fulfillment", "returns"},
	"risk":       {"anomaly", "guardrail", "control"},
}

func expandTokens(tokens []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(tokens)*2)
	for _, token := range tokens {
		if !seen[token] {
			seen[token] = true
			result = append(result, token)
		}
		for _, synonym := range synonyms[token] {
			if !seen[synonym] {
				seen[synonym] = true
				result = append(result, synonym)
			}
		}
	}
	return result
}

func vectorize(tokens []string) []float64 {
	vector := make([]float64, vectorDimensions)
	for _, token := range tokens {
		h := fnv.New32a()
		_, _ = h.Write([]byte(token))
		index := int(h.Sum32() % vectorDimensions)
		vector[index]++
	}
	return vector
}

func cosine(a, b []float64) float64 {
	var dot, aa, bb float64
	for i := range a {
		dot += a[i] * b[i]
		aa += a[i] * a[i]
		bb += b[i] * b[i]
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb))
}

func overlap(a, b []string) float64 {
	if len(a) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, value := range b {
		set[value] = true
	}
	matched := 0
	for _, value := range a {
		if set[value] {
			matched++
		}
	}
	return float64(matched) / float64(len(a))
}

func round(value float64) float64 { return math.Round(value*10000) / 10000 }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
