package rag

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisParentCache struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedisParentCache(ctx context.Context, address, password string, database int, ttl time.Duration) (*RedisParentCache, error) {
	client := redis.NewClient(&redis.Options{Addr: address, Password: password, DB: database})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping Redis: %w", err)
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &RedisParentCache{client: client, ttl: ttl}, nil
}

func (c *RedisParentCache) Get(ctx context.Context, storeID, parentID string) (Chunk, bool, error) {
	data, err := c.client.Get(ctx, parentCacheKey(storeID, parentID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Chunk{}, false, nil
	}
	if err != nil {
		return Chunk{}, false, err
	}
	var chunk Chunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return Chunk{}, false, err
	}
	return chunk, true, nil
}

func (c *RedisParentCache) Set(ctx context.Context, storeID string, chunk Chunk) error {
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, parentCacheKey(storeID, chunk.ID), data, c.ttl).Err()
}

func (c *RedisParentCache) Close() error { return c.client.Close() }

func parentCacheKey(storeID, parentID string) string {
	digest := sha256.Sum256([]byte(storeID + "\x00" + parentID))
	return fmt.Sprintf("evoops:rag:parent:%x", digest[:16])
}
