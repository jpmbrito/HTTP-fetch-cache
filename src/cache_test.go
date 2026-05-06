package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setupTest(t *testing.T, defaultTTL time.Duration, httpServerCount *atomic.Int64) (*Cache, *httptest.Server, string) {
	// 1Megabyte random payload
	randomBytes := make([]byte, 512*1024)
	rand.Read(randomBytes)
	expected_payload := hex.EncodeToString(randomBytes)

	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	server := &httptest.Server{
		Listener: listener,
		Config: &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(expected_payload))
				httpServerCount.Add(1)
			}),
		},
	}
	server.Start()

	c := NewCache(defaultTTL)
	return c, server, expected_payload
}

func assertFetch(t *testing.T, c *Cache, url string, expected string, ttlOverride ...time.Duration) {
	ttl := c.defaultTTL
	if len(ttlOverride) > 0 {
		ttl = ttlOverride[0]
	}
	data, err := c.Fetch(context.Background(), url, ttl)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != expected {
		t.Fatalf("expected %q, got %q", expected, string(data))
	}

	// lets give a small 1ns delay to make sure the next entry is created with different timestamp
	// if cpu is too fast sometimes timestamps are the same. My ryzen 7 sometimes is.
	time.Sleep(1 * time.Nanosecond)
}

func assertFetchConcurrent(t *testing.T, c *Cache, url string, expected string, concurrentRoutines int) {
	var wg sync.WaitGroup
	wg.Add(concurrentRoutines)
	for i := 0; i < concurrentRoutines; i++ {
		go func() {
			defer wg.Done()
			assertFetch(t, c, url, expected)
		}()
	}
	wg.Wait()
}

func assertFetchServerConcurrent(t *testing.T, c *Cache, urls []string, expected []string, concurrentRoutines int) {
	var wg sync.WaitGroup
	wg.Add(len(urls))
	for i, url := range urls {
		go func(u string, exp string) {
			defer wg.Done()
			assertFetchConcurrent(t, c, u, exp, concurrentRoutines)
		}(url, expected[i])
	}
	wg.Wait()
}

func assertServerCount(t *testing.T, httpServerCount atomic.Int64, expected int64) {
	if httpServerCount.Load() != expected {
		t.Fatalf("expected %d server calls, got %d", expected, httpServerCount.Load())
	}
}

func assertStats(t *testing.T, c *Cache, expectedHits int, expectedMisses int, expectedEntries int) {
	hits, misses, entries := c.Stats()
	if hits != expectedHits || misses != expectedMisses || entries != expectedEntries {
		t.Fatalf("expected assertStats(%d, %d, %d), got assertStats(%d, %d, %d)", expectedHits, expectedMisses, expectedEntries, hits, misses, entries)
	}
}

func TestCacheFetch(t *testing.T) {
	var httpServerCount atomic.Int64
	c, server, expected_payload := setupTest(t, 5*time.Minute, &httpServerCount)
	defer server.Close()

	assertFetch(t, c, server.URL, expected_payload, 0)
	assertServerCount(t, httpServerCount, 1)
	assertStats(t, c, 0, 1, 1) // MISS

	assertFetch(t, c, server.URL, expected_payload, 0)
	assertServerCount(t, httpServerCount, 2)
	assertStats(t, c, 0, 2, 1) // MISS

	assertFetch(t, c, server.URL, expected_payload)
	assertServerCount(t, httpServerCount, 3)
	assertStats(t, c, 0, 3, 1) // MISS

	assertFetch(t, c, server.URL, expected_payload)
	assertServerCount(t, httpServerCount, 3)
	assertStats(t, c, 1, 3, 1) // HIT

	assertFetch(t, c, server.URL, expected_payload)
	assertServerCount(t, httpServerCount, 3)
	assertStats(t, c, 2, 3, 1) // HIT

	var httpServer2Count atomic.Int64
	_, server2, expected_payload2 := setupTest(t, 5*time.Minute, &httpServer2Count)
	defer server2.Close()

	assertFetch(t, c, server2.URL, expected_payload2, 0)
	assertServerCount(t, httpServerCount, 3)
	assertServerCount(t, httpServer2Count, 1)
	assertStats(t, c, 2, 4, 2) // MISS

	assertFetch(t, c, server2.URL, expected_payload2)
	assertServerCount(t, httpServerCount, 3)
	assertServerCount(t, httpServer2Count, 2)
	assertStats(t, c, 2, 5, 2) // MISS

	assertFetch(t, c, server2.URL, expected_payload2, 0)
	assertServerCount(t, httpServerCount, 3)
	assertServerCount(t, httpServer2Count, 2)
	assertStats(t, c, 3, 5, 2) // HIT
}

func TestCacheConcurrency(t *testing.T) {
	var httpServerCount atomic.Int64
	c, server, expected_payload := setupTest(t, 5*time.Minute, &httpServerCount)
	defer server.Close()

	assertFetchConcurrent(t, c, server.URL, expected_payload, 1000)
	assertServerCount(t, httpServerCount, 1)
	assertStats(t, c, 999, 1, 1)

	var httpServerCount2 atomic.Int64
	c, server2, expected_payload2 := setupTest(t, 5*time.Minute, &httpServerCount2)
	defer server2.Close()

	var httpServerCount3 atomic.Int64
	_, server3, expected_payload3 := setupTest(t, 5*time.Minute, &httpServerCount3)
	defer server3.Close()

	var httpServerCount4 atomic.Int64
	_, server4, expected_payload4 := setupTest(t, 5*time.Minute, &httpServerCount4)
	defer server4.Close()

	assertFetchServerConcurrent(t, c, []string{server.URL, server2.URL, server3.URL, server4.URL}, []string{expected_payload, expected_payload2, expected_payload3, expected_payload4}, 10000)
	assertStats(t, c, 10000*4-4, 4, 4)

	var httpServerCount5 atomic.Int64
	c, server5, expected_payload5 := setupTest(t, 5*time.Minute, &httpServerCount5)
	defer server5.Close()

	assertFetchServerConcurrent(t, c,
		[]string{
			server.URL, server.URL,
			server2.URL, server2.URL,
			server3.URL, server3.URL,
			server4.URL, server4.URL,
			server5.URL, server5.URL,
		},
		[]string{
			expected_payload, expected_payload,
			expected_payload2, expected_payload2,
			expected_payload3, expected_payload3,
			expected_payload4, expected_payload4,
			expected_payload5, expected_payload5,
		}, 10000)
	assertStats(t, c, 10000*5*2-5, 5, 5)
}

func TestCacheTTL(t *testing.T) {
	var httpServerCount atomic.Int64
	c, server, expected_payload := setupTest(t, 1*time.Millisecond, &httpServerCount)
	defer server.Close()

	assertFetch(t, c, server.URL, expected_payload, 100*time.Millisecond)
	assertServerCount(t, httpServerCount, 1)
	assertStats(t, c, 0, 1, 1) // MISS

	assertFetch(t, c, server.URL, expected_payload)
	assertServerCount(t, httpServerCount, 1)
	assertStats(t, c, 1, 1, 1) // HIT

	time.Sleep(110 * time.Millisecond)

	assertFetch(t, c, server.URL, expected_payload, 100*time.Millisecond)
	assertServerCount(t, httpServerCount, 2)
	assertStats(t, c, 1, 2, 1) // MISS

	assertFetch(t, c, server.URL, expected_payload)
	assertServerCount(t, httpServerCount, 2)
	assertStats(t, c, 2, 2, 1) // HIT

	time.Sleep(110 * time.Millisecond)
	assertFetch(t, c, server.URL, expected_payload, 100*time.Millisecond)
	assertServerCount(t, httpServerCount, 3)
	assertStats(t, c, 2, 3, 1) // MISS

}
