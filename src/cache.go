package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type cacheEntry struct {
	data      []byte
	timestamp time.Time
	ttl       time.Duration
}
type URL string

type loadingCacheEntry struct {
	wg      sync.WaitGroup
	waiters atomic.Int64

	err error
}

type Cache struct {
	defaultTTL time.Duration

	// To be used for clean-up of expired entries
	mutex sync.RWMutex

	entries map[URL]*cacheEntry

	// Last one waiting cleans up the loadingCache entry
	loadingCacheEntries map[URL]*loadingCacheEntry
	loadingCacheMutex   sync.RWMutex
}

func NewCache(defaultTTL time.Duration) *Cache {
	return &Cache{
		defaultTTL:          defaultTTL,
		entries:             make(map[URL]*cacheEntry),
		loadingCacheEntries: make(map[URL]*loadingCacheEntry),
	}
}

func (c *Cache) Fetch(ctx context.Context, url string, ttlOverride ...time.Duration) ([]byte, error) {
	return nil, nil
}

func (c *Cache) Stats() (hits int, misses int, entries int) {
	return 0, 0, 0
}
