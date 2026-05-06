package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
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

	// lets give a small 1ms delay to make sure the next entry is created with different timestamp
	// if cpu is too fast sometimes timestamps are the same
	time.Sleep(1 * time.Microsecond)
}

func assertServerCount(t *testing.T, httpServerCount atomic.Int64, expected int64) {
	if httpServerCount.Load() != expected {
		t.Fatalf("expected %d server calls, got %d", expected, httpServerCount.Load())
	}
}

func assertStats(t *testing.T, c *Cache, expectedHits int, expectedMisses int, expectedEntries int) {
	hits, misses, entries := c.Stats()
	if hits != expectedHits {
		t.Fatalf("expected %d hits, got %d , assertStats(%d, %d, %d)", expectedHits, hits, expectedHits, expectedMisses, expectedEntries)
	}
	if misses != expectedMisses {
		t.Fatalf("expected %d misses, got %d , assertStats(%d, %d, %d)", expectedMisses, misses, expectedHits, expectedMisses, expectedEntries)
	}
	if entries != expectedEntries {
		t.Fatalf("expected %d entries, got %d , assertStats(%d, %d, %d)", expectedEntries, entries, expectedHits, expectedMisses, expectedEntries)
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
	// TODO: simulate concurrent fetches for the same URL
}

func TestCacheTTL(t *testing.T) {
	// TODO: test TTL expiration and re-fetch
}
