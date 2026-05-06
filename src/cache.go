package cache

import (
	"context"
	"time"
)

type Cache struct {
}

func NewCache(defaultTTL time.Duration) *Cache {
	return &Cache{}
}

func (c *Cache) Fetch(ctx context.Context, url string, ttlOverride ...time.Duration) ([]byte, error) {
	return nil, nil
}

func (c *Cache) Stats() (hits int, misses int, entries int) {
	return 0, 0, 0
}
