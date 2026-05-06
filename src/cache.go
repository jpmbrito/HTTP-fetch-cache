package cache

import (
	"context"
	"fmt"
	"io"
	"net/http"
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

	// Unfortunatelly we need to store data because when evaluating a loading cache hit we only have a lock towards the loadingCache structure
	// also pretty easily ttl can expire. the waiters get always the instant cache
	data []byte
	err  error
}

type statistics struct {
	hits   int
	misses int
}

type Cache struct {
	defaultTTL time.Duration

	// To be used for clean-up of expired entries
	mutex sync.RWMutex

	entries map[URL]*cacheEntry

	// Last one waiting cleans up the loading cache entry
	loadingCacheEntries map[URL]*loadingCacheEntry
	loadingCacheMutex   sync.RWMutex

	stats statistics
}

func (c *Cache) cleanUpExpiredEntries() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := time.Now()
	for url, entry := range c.entries {
		if now.Sub(entry.timestamp) > entry.ttl {
			delete(c.entries, url)
		}
	}
}

func httpRequest(ctx context.Context, url string, method ...string) ([]byte, error) {
	httpMethod := http.MethodGet
	if len(method) > 0 {
		httpMethod = method[0]
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *Cache) popLoadingCacheHit(url URL, loadingCacheEntry *loadingCacheEntry) ([]byte, error) {
	c.loadingCacheMutex.Lock()
	defer c.loadingCacheMutex.Unlock()

	var data []byte
	var err error

	if err = loadingCacheEntry.err; err == nil {
		data = loadingCacheEntry.data
	}

	// Last one cleans up the loading cache entry
	if loadingCacheEntry.waiters.Add(-1) == 0 {
		delete(c.loadingCacheEntries, url)
	}

	return data, err
}

func NewCache(defaultTTL time.Duration) *Cache {
	return &Cache{
		defaultTTL:          defaultTTL,
		entries:             make(map[URL]*cacheEntry),
		loadingCacheEntries: make(map[URL]*loadingCacheEntry),
	}
}

func (c *Cache) Fetch(ctx context.Context, url string, ttlOverride ...time.Duration) ([]byte, error) {
	ttl := c.defaultTTL

	// todo: really time consuming per request. Create a housekeeping routine to free-up memory
	// This will only run every something requests. when we implement stats. No housekeeping!
	// The reason is because we could have some kind of dos attack and we want to make sure we free up memory as soon as possible. We could also have a max cache size and free up the least recently used entries.
	c.cleanUpExpiredEntries()

	// override default ttl if provided
	if len(ttlOverride) > 0 {
		ttl = ttlOverride[0]
	}

	// From this moment onwards the data either exists on valid cache or we need to fetch it
	// Regardless of what we do, we need to be exclusive
	c.mutex.Lock()

	// Check if cache entry exists and is valid
	entry, exists := c.entries[URL(url)]
	if exists && time.Since(entry.timestamp) < entry.ttl {
		c.stats.hits += 1
		fmt.Printf("CACHE HIT %d\t: url=%s, elapsed=%v, ttl=%v\n", c.stats.hits, url, time.Since(entry.timestamp), entry.ttl)

		c.mutex.Unlock()
		return entry.data, nil
	}

	// lets free up the cache in case we identify an expired entry
	if exists {
		delete(c.entries, URL(url))
	}

	c.loadingCacheMutex.Lock()

	// Check if data exists in loading cache
	if loadingCacheEntry, loadingCacheExists := c.loadingCacheEntries[URL(url)]; loadingCacheExists {
		c.stats.hits += 1
		fmt.Printf("CACHE HIT(LOAD) %d\t: url=%s\n", c.stats.hits, url)

		loadingCacheEntry.waiters.Add(1)
		c.loadingCacheMutex.Unlock()
		c.mutex.Unlock()

		loadingCacheEntry.wg.Wait()
		return c.popLoadingCacheHit(URL(url), loadingCacheEntry)
	}

	// Lets create the loading cache entry and release the locks
	loadingCacheEntry := &loadingCacheEntry{}
	c.loadingCacheEntries[URL(url)] = loadingCacheEntry

	// Producer is also a waiter, to ensure we properly delete the loading cache entry
	loadingCacheEntry.waiters.Add(1)
	loadingCacheEntry.wg.Add(1)
	c.loadingCacheMutex.Unlock()

	c.mutex.Unlock()

	// Need to fetch the data as in loading cache
	data, err := httpRequest(ctx, url)

	// Store in loading cache
	c.loadingCacheMutex.Lock()
	c.loadingCacheEntries[URL(url)].err = err
	c.loadingCacheEntries[URL(url)].data = data
	c.loadingCacheMutex.Unlock()

	// Store in cache if no error
	if err == nil {
		c.mutex.Lock()
		c.entries[URL(url)] = &cacheEntry{
			data:      data, // reference copy. GC manages it
			timestamp: time.Now(),
			ttl:       ttl,
		}

		c.stats.misses += 1
		fmt.Printf("CACHE MISS %d\t: url=%s, elapsed=%v, ttl=%v\n", c.stats.misses, url, time.Since(c.entries[URL(url)].timestamp), c.entries[URL(url)].ttl)
		c.mutex.Unlock()
	}

	// Lets release all the waiters:
	loadingCacheEntry.wg.Done()

	// Lets make sure the producer also pops the loading cache hit to ensure he also frees up the memory
	return c.popLoadingCacheHit(URL(url), loadingCacheEntry)
}

func (c *Cache) Stats() (hits int, misses int, entries int) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.stats.hits, c.stats.misses, len(c.entries)
}
