package cache

import (
	"context"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// LoadingCacheEntry represents an entry that is currently being loaded.
// This is necessary to serve the concurrent customers competing for the same resource and to avoid dedpulicated http requests.
type loadingCacheEntry struct {
	// wait group is required to make sure all concurrent requests are served before deletion of the loading cache entry
	wg sync.WaitGroup

	// atomic counter to keep track of all requests waiting for the same resource
	waiters atomic.Int64

	// Data needs to be stored in the loading cache entry because we don't control TTL.
	// In edge case, if TTL is zero, we would keep all concurrent requests waiting and the cache would expire immediately
	// Therefore we need to store the data and serve the concurrent requests.
	data []byte

	// Same for error, for deduplication reasons, we forward the error to all concurrent waiters
	err error
}

// CacheEntry represents a loaded cache entry
type cacheEntry struct {
	data      []byte
	timestamp time.Time
	ttl       time.Duration
}

// metrics holds statistics about cache performance.
// Intentionally the structure is not thread safe to force updating and accessing the values from within critical sessions.
// This ensures that all logs represents the correct state of the cache on any given instant
type metrics struct {
	hits   int
	misses int
}

// Cache is a thread-safe in-memory TTL cache
type Cache struct {
	// Default TTL value if not provided by the user during fetch
	defaultTTL time.Duration

	// The entries map holds cached data indexed by URL
	// The mutex is required to ensure exclusive thread safe access
	entries      map[string]*cacheEntry
	entriesMutex sync.RWMutex

	// The loadingCacheEntries map holds loading cache entries to be served to concurrent requests.
	// It's necessary to ensure that there are no deduplicated http requests
	loadingCacheEntries map[string]*loadingCacheEntry
	loadingCacheMutex   sync.Mutex

	stats metrics

	// Cache Maximum memory size
	maxMemorySize uint64
}

// NewCache creates and initialize a new Cache with a predefined memory limitation
func NewCache(defaultTTL time.Duration) *Cache {
	return &Cache{
		defaultTTL:          defaultTTL,
		entries:             make(map[string]*cacheEntry),
		loadingCacheEntries: make(map[string]*loadingCacheEntry),
		maxMemorySize:       1024 * 1024 * 100, // By default lets set max memory size to 100MB
	}
}

// timeout ctx shall be provided by the caller. Default context is used if not provided.
func (c *Cache) Fetch(ctx context.Context, url string, ttlOverride ...time.Duration) ([]byte, error) {
	ttl := c.defaultTTL

	// 1. Override default ttl if provided
	if len(ttlOverride) > 0 {
		ttl = ttlOverride[0]
	}

	// 2. Lets ensure we have the cache entry lock
	c.entriesMutex.Lock()

	// 3. Check if resource exists on cache
	entry, exists := c.entries[url]
	if exists && time.Since(entry.timestamp) < entry.ttl {

		//3.1. It exists, Cache HIT
		// Lock exists mainly to keep consistency when logging. Indeed I could have hits an atomic.Int64 
		// but since it always go together with log we can keep it as normal int since it's protected with a mutex
		c.stats.hits += 1
		log.Printf("CACHE HIT %d\t: url=%s, elapsed=%v, ttl=%v\n", c.stats.hits, url, time.Since(entry.timestamp), entry.ttl)

		//3.2. Release 2.
		c.entriesMutex.Unlock()
		
		return entry.data, nil
	}

	// 4. We know there is no cache HIT. If the entry exists, it means TTL elapsed. High performance clean-up at this stage
	if exists {
		delete(c.entries, url)
	}

	// 5. Before fetching, lets verify if another fetch request is already loading the same resource. If yes, we wait for it.
	c.loadingCacheMutex.Lock()

	// 5.1. Verify if the entry exists in the loading cache
	if loadingCacheEntry, loadingCacheExists := c.loadingCacheEntries[url]; loadingCacheExists {

		// 5.2. We get a loading cache it
		c.stats.hits += 1
		log.Printf("CACHE HIT(LOAD) %d\t: url=%s\n", c.stats.hits, url)

		// 5.3. We add this fetch request as a waiter of the loading entry
		// We need to control how many waiters there are because the last one needs to clear the entry
		loadingCacheEntry.waiters.Add(1)

		// 5.4. At this stage we can release both cache and loadingCache locks
		c.loadingCacheMutex.Unlock()
		c.entriesMutex.Unlock()

		// 5.5. Lets wait to be informed that the resource is available
		loadingCacheEntry.wg.Wait()

		// 5.6. resolve the loading entry
		return c.resolve(url, loadingCacheEntry)
	}

	// 6. In this step we know the resource doesn't exist in the cache neither loadingCache.
	// Therefore lets initiate the loadingCache for other consumers for this specific resource which will be fetched
	loadingCacheEntry := &loadingCacheEntry{}
	c.loadingCacheEntries[url] = loadingCacheEntry

	// 6.1. The loading cache producer is also a consumer. This is required in case the producer is the only interested
	loadingCacheEntry.waiters.Add(1)
	loadingCacheEntry.wg.Add(1)

	// 6.2. Now we can safely release the loadingCache
	c.loadingCacheMutex.Unlock()
	c.entriesMutex.Unlock()

	// 7. Fetch the resource. This might be time consuming. No resource is locked
	data, err := httpRequest(ctx, url)

	// 8. Response is fetched. We still don't know if it errored or not, doesn't matter now.
	// The next step is to share this result with all loadingCache consumers
	c.loadingCacheMutex.Lock()
	c.loadingCacheEntries[url].err = err
	c.loadingCacheEntries[url].data = data
	c.loadingCacheMutex.Unlock()

	// 9. Lets unblock all loadingCache consumers
	loadingCacheEntry.wg.Done()

	// 10. At this moment we only want to store the fetched result in the cache in case there is no error, or, as observed by code review,
	// in case there is not enough memory to safely store it.
	var memUsageEstimate uint64
	if err == nil {

		// 10.1. We have a valid result. Definitely it's a cache miss.
		c.stats.misses += 1
		log.Printf("CACHE MISS %d\t: url=%s, ttl=%v\n", c.stats.misses, url, ttl)

		// 10.2. Lets estimate the memory needed to pack this new resource
		// During housekeeping, we are also releasing all resources that elapsed ttl
		// It's expensive call because we will iterate over all cache entries.
		// For future, we could optimize the housekeeper. For the sake of time this is suffient for this first version
		memUsageEstimate = c.housekeep()
		memUsageEstimate += uint64(len(data))
	}

	// 11. For security reasons, we only store in cache if we have available memory
	if err == nil && memUsageEstimate <= c.maxMemorySize {

		// 11.1. Lets make sure we have the cache entry lock
		c.entriesMutex.Lock()

		// 11.2. Now we add the cache entry. Important: this is a reference copy (therefore this is not expensive).
		// GC will take care of memory releases later on.
		c.entries[url] = &cacheEntry{
			data:      data,
			timestamp: time.Now(),
			ttl:       ttl,
		}

		// 11.3. Lets safely release it
		c.entriesMutex.Unlock()
	}

	// 12. As mentioned in 6.1, producer is also a consumer to ensure we properly delete the loading cache entry
	return c.resolve(url, loadingCacheEntry)
}

// Stats return the current cache metrics, hits, misses and total cache entries.
func (c *Cache) Stats() (hits int, misses int, entries int) {
	c.entriesMutex.RLock()
	defer c.entriesMutex.RUnlock()
	return c.stats.hits, c.stats.misses, len(c.entries)
}

// houseKeep removes expired entries from cache and calculates the total memory used after ckean up
func (c *Cache) housekeep() uint64 {
	c.entriesMutex.Lock()
	defer c.entriesMutex.Unlock()

	var memoryUsed uint64

	now := time.Now()
	for url, entry := range c.entries {
		if now.Sub(entry.timestamp) > entry.ttl {
			delete(c.entries, url)
		} else {
			memoryUsed += uint64(len(entry.data))
		}
	}

	return memoryUsed
}

// resolve all loadingCache entries and ensures the final concurrent waiter cleans up the temporary entry
func (c *Cache) resolve(url string, loadingCacheEntry *loadingCacheEntry) ([]byte, error) {
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

// httpRequest is a helper function to execute a synchronous http request with context
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
