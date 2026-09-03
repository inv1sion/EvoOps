package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/inv1sion/evoops/internal/domain"
)

const maxProviderErrorBytes = 4096

type DashScopeEmbedder struct {
	apiKey     string
	endpoint   string
	model      string
	dimensions int
	client     *http.Client
}

func NewDashScopeEmbedder(apiKey, baseURL, model string, dimensions int) (*DashScopeEmbedder, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("embedding API key is required")
	}
	if model == "" {
		model = "text-embedding-v3"
	}
	if dimensions <= 0 {
		dimensions = 1024
	}
	endpoint, err := joinEndpoint(baseURL, "embeddings")
	if err != nil {
		return nil, err
	}
	return &DashScopeEmbedder{apiKey: apiKey, endpoint: endpoint, model: model, dimensions: dimensions, client: &http.Client{Timeout: 45 * time.Second}}, nil
}

func (e *DashScopeEmbedder) Model() string { return e.model }

func (e *DashScopeEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	vectors, err := e.embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (e *DashScopeEmbedder) EmbedDocuments(ctx context.Context, documents []string) ([][]float32, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	// text-embedding-v3 accepts at most ten text inputs per synchronous call.
	result := make([][]float32, 0, len(documents))
	for start := 0; start < len(documents); start += 10 {
		end := min(start+10, len(documents))
		batch, err := e.embed(ctx, documents[start:end])
		if err != nil {
			return nil, fmt.Errorf("embed document batch %d: %w", start/10+1, err)
		}
		result = append(result, batch...)
	}
	return result, nil
}

func (e *DashScopeEmbedder) embed(ctx context.Context, input []string) ([][]float32, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("embedding input is empty")
	}
	for _, value := range input {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("embedding input contains empty text")
		}
	}
	payload := map[string]any{"model": e.model, "input": input, "dimensions": e.dimensions, "encoding_format": "float"}
	var response struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := e.post(ctx, payload, &response); err != nil {
		return nil, err
	}
	if len(response.Data) != len(input) {
		return nil, fmt.Errorf("embedding response count %d does not match input count %d", len(response.Data), len(input))
	}
	sort.Slice(response.Data, func(i, j int) bool { return response.Data[i].Index < response.Data[j].Index })
	vectors := make([][]float32, len(response.Data))
	for index, item := range response.Data {
		if item.Index != index || len(item.Embedding) != e.dimensions {
			return nil, fmt.Errorf("embedding response index or dimension is invalid")
		}
		vectors[index] = item.Embedding
	}
	return vectors, nil
}

func (e *DashScopeEmbedder) post(ctx context.Context, payload any, target any) error {
	return postJSON(ctx, e.client, e.endpoint, e.apiKey, payload, target)
}

type DashScopeReranker struct {
	apiKey   string
	endpoint string
	model    string
	client   *http.Client
}

func NewDashScopeReranker(apiKey, endpoint, model string) (*DashScopeReranker, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("rerank API key is required")
	}
	if model == "" {
		model = "qwen3-rerank"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("rerank endpoint must be an absolute HTTP URL")
	}
	return &DashScopeReranker{apiKey: apiKey, endpoint: strings.TrimRight(endpoint, "/"), model: model, client: &http.Client{Timeout: 45 * time.Second}}, nil
}

func (r *DashScopeReranker) Model() string { return r.model }

func (r *DashScopeReranker) Rerank(ctx context.Context, query string, hits []domain.RetrievalHit) ([]domain.RetrievalHit, error) {
	if len(hits) == 0 {
		return nil, nil
	}
	documents := make([]string, len(hits))
	for index, hit := range hits {
		documents[index] = strings.TrimSpace(hit.Chunk.Title + "\n" + hit.Chunk.Text)
	}
	payload := map[string]any{
		"model": r.model, "query": query, "documents": documents, "top_n": len(documents),
		"instruct": "Given an advertising operations question, retrieve passages that provide evidence for the answer.",
	}
	type rank struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
	}
	var response struct {
		Results []rank `json:"results"`
		Output  struct {
			Results []rank `json:"results"`
		} `json:"output"`
	}
	if err := postJSON(ctx, r.client, r.endpoint, r.apiKey, payload, &response); err != nil {
		return nil, err
	}
	results := response.Results
	if len(results) == 0 {
		results = response.Output.Results
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("rerank response contains no results")
	}
	seen := make(map[int]bool, len(results))
	reranked := make([]domain.RetrievalHit, 0, len(results))
	for _, item := range results {
		if item.Index < 0 || item.Index >= len(hits) || seen[item.Index] {
			return nil, fmt.Errorf("rerank response contains an invalid index")
		}
		seen[item.Index] = true
		hit := hits[item.Index]
		hit.RerankScore = round(item.RelevanceScore)
		reranked = append(reranked, hit)
	}
	sort.SliceStable(reranked, func(i, j int) bool { return reranked[i].RerankScore > reranked[j].RerankScore })
	return reranked, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint, apiKey string, payload, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, maxProviderErrorBytes))
		return fmt.Errorf("provider returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode provider response: %w", err)
	}
	return nil
}

func joinEndpoint(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("provider base URL must be an absolute HTTP URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return parsed.String(), nil
}

func round(value float64) float64 {
	const factor = 10000
	if value >= 0 {
		return float64(int64(value*factor+0.5)) / factor
	}
	return float64(int64(value*factor-0.5)) / factor
}
