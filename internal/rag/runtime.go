package rag

import (
	"context"
	"fmt"
	"time"
)

type RuntimeConfig struct {
	PostgresURL        string
	RedisAddress       string
	RedisPassword      string
	RedisDB            int
	RedisTTL           time.Duration
	MilvusAddress      string
	MilvusToken        string
	MilvusDatabase     string
	MilvusCollection   string
	EmbeddingAPIKey    string
	EmbeddingBaseURL   string
	EmbeddingModel     string
	EmbeddingDimension int
	RerankAPIKey       string
	RerankURL          string
	RerankModel        string
	MaxContextChars    int
}

func Open(ctx context.Context, cfg RuntimeConfig) (*Service, error) {
	parents, err := NewPostgresParentStore(ctx, cfg.PostgresURL)
	if err != nil {
		return nil, err
	}
	leaves, err := NewMilvusLeafStore(ctx, cfg.MilvusAddress, cfg.MilvusToken, cfg.MilvusDatabase, cfg.MilvusCollection, cfg.EmbeddingDimension)
	if err != nil {
		parents.Close()
		return nil, err
	}
	cache, err := NewRedisParentCache(ctx, cfg.RedisAddress, cfg.RedisPassword, cfg.RedisDB, cfg.RedisTTL)
	if err != nil {
		parents.Close()
		_ = leaves.Close(context.Background())
		return nil, err
	}
	embedder, err := NewDashScopeEmbedder(cfg.EmbeddingAPIKey, cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimension)
	if err != nil {
		_ = cache.Close()
		parents.Close()
		_ = leaves.Close(context.Background())
		return nil, fmt.Errorf("initialize embedding client: %w", err)
	}
	reranker, err := NewDashScopeReranker(cfg.RerankAPIKey, cfg.RerankURL, cfg.RerankModel)
	if err != nil {
		_ = cache.Close()
		parents.Close()
		_ = leaves.Close(context.Background())
		return nil, fmt.Errorf("initialize reranker client: %w", err)
	}
	service := NewService(parents, leaves, cache, embedder, reranker, cfg.MaxContextChars)
	if err := service.EnsureSchema(ctx); err != nil {
		_ = service.Close(context.Background())
		return nil, err
	}
	return service, nil
}
