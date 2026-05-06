package cache

import (
	"context"
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

	// Unfortunatelly we need to store data because when evaluating a loadingCache cache hit we only have a lock towards the loadingCache structure
	data []byte
	err  error
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

func (c *Cache) cacheUpdateTTL(url URL, ttl time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if entry, exists := c.entries[url]; exists {
		entry.ttl = ttl
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

func (c *Cache) poploadingCacheHit(url URL, loadingCacheEntry *loadingCacheEntry) ([]byte, error) {
	c.loadingCacheMutex.Lock()
	defer c.loadingCacheMutex.Unlock()

	var data []byte
	var err error

	if err = loadingCacheEntry.err; err == nil {
		data = loadingCacheEntry.data
	}

	// Last consumer cleans up the loading cache entry
	if loadingCacheEntry.waiters.Load() == 0 {
		delete(c.loadingCacheEntries, url)
	}

	return data, err
}

func (c *Cache) Fetch(ctx context.Context, url string, ttlOverride ...time.Duration) ([]byte, error) {
	ttl := c.defaultTTL

	// override TTL if provided to already existing cache entries
	// Since it's a Map in theory there is not much performance impact
	if len(ttlOverride) > 0 {
		c.cacheUpdateTTL(URL(url), ttlOverride[0])
		ttl = ttlOverride[0]
	}

	// It's possible to force a fetch if ttlOverride to 0
	// todo: really time consuming per request. Create a housekeeping routine to free-up memory
	c.cleanUpExpiredEntries()

	// From this moment onwards the data either exists on valid cache or we need to fetch it
	// Regardless of what we do, we need to be exclusive
	c.mutex.Lock()

	// Check if cache entry exists and is valid
	entry, exists := c.entries[URL(url)]
	if exists && time.Since(entry.timestamp) <= entry.ttl {
		c.mutex.Unlock()
		return entry.data, nil
	}

	// lets free up the cache in case we identify an expired entry
	if exists {
		delete(c.entries, URL(url))
	}

	c.loadingCacheMutex.Lock()

	// verify if the cache is loaded by another fetch. If so, we wait and use the loading cache
	if loadingCacheEntry, loadingCacheExists := c.loadingCacheEntries[URL(url)]; loadingCacheExists {
		loadingCacheEntry.waiters.Add(1)
		c.loadingCacheMutex.Unlock()
		c.mutex.Unlock()

		loadingCacheEntry.wg.Wait()
		return c.poploadingCacheHit(URL(url), loadingCacheEntry)
	}

	// Lets create the loading cache entry and release the locks
	loadingCacheEntry := &loadingCacheEntry{}
	loadingCacheEntry.wg.Add(1)
	c.loadingCacheEntries[URL(url)] = loadingCacheEntry
	c.loadingCacheMutex.Unlock()

	c.mutex.Unlock()

	// We now execute the http request without holding any lock
	data, err := httpRequest(ctx, url)

	// Store in loadingCache
	c.loadingCacheMutex.Lock()
	c.loadingCacheEntries[URL(url)].err = err
	c.loadingCacheEntries[URL(url)].data = data
	c.loadingCacheMutex.Unlock()

	// Store in cache entry if no error
	if err == nil {
		c.mutex.Lock()
		c.entries[URL(url)] = &cacheEntry{
			data:      data,
			timestamp: time.Now(),
			ttl:       ttl,
		}
		c.mutex.Unlock()
	}

	// Lets make sure the producer also pops the loading Cache
	// to ensure he also frees up the memory. This is a edge case in case there are any consumers waiting for the loading cache hit, but the producer also needs to pop the loading cache hit to ensure he also frees up the memory
	return c.poploadingCacheHit(URL(url), loadingCacheEntry)
}

func (c *Cache) Stats() (hits int, misses int, entries int) {
	return 0, 0, 0
}
