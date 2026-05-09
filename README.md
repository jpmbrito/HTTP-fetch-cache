# HTTP Fetch cache

This repository contains a implementation of HTTP Fetch with TTL and Deduplication in golang (`src/cache.go`)

## Table of contents
1. [Planning](#planning)
2. [Software Development Life Cycle](#software-development-life-cycle)
    * [Requirements](#requirements)
    * [Design](#design)
    * [Implementation](#implementation)
3. [Getting started](#getting-started)
    * [Prerequisites](#prerequisites)
    * [Build and execution](#build-and-execution)
4. [Review findings](#review-findings)
5. [Current limitations](#current-limitations)

## Requirements
It's necessary to develop a web service proxy capable of:
- Fetch and cache on the fly HTTP requests within Time To Live period
- Prevent deduplication during caching. This means that only one HTTP request for a given resource shall be executed at a time. Means other concurrent requests shall wait for this unique request.
- Provide cache statics (cache miss, cache hits and total cache entries)
- The deliverable has just a form of a golang unit test file

## Design
This implementation will be mainly located in `cache.go` exposing three main interfaces:
- NewCache
    - Cache initializer
- Fetch
    - This is where the cache fetching algorithm should be implemented. It is initiated by a user HTTP request for a specific resource, as follows:
        1. It shall check if the requested URL exists (cached) in the internal cache structure
        1.1. If it exists, it shall check if it's within the configured Time To Leave period. If not, it shall be cleaned
        2. It checks if the requested URL is already being loaded by another fetch request. If it is, the system waits for the request to finish and uses the cached version
        3. If the URL is neither cached nor being loaded, an HTTP request shall be initiated accordingly with a mechanism that prevents deduplication, by informing other Fetch requests for the same URL from step 2.
- Stats
    - The following metrics shall be made available:
        1. hits: For cache hits. The counter gets incremented when a cache entry exists within the registered TT
        2. misses: For cache misses. The counter gets incremented each time a given URL doesn't exist in the cache
        3. entries: The number of cache entries existing at the instant

## Implementation
All implementation details will be stored in the code. The executable will be mainly a unit test file called cache_test.go

## Planning

An initial planning session has been made to estimate the amount of time necessary to perform this task. Following table contains the estimation and real time measurement:

| Task Description | Estimated Time | 
| :--- | :--- | 
| Reading spec & planning | 10 min | 
| Designing cache structure (storage, TTLs, deduplication, locks) | 10 min | 
| Implementing basic cache storage & Fetch skeleton | 15 min |  
| Adding TTL expiration logic | 10 min |
| Implementing deduplication for concurrent fetches | 15 min | 
| Adding HTTP fetch logic & error handling | 15 min | 
| Adding logging of cache hits/misses | 10 min |
| Implementing Stats() | 5 min |
| Writing unit tests (httptest.Server, concurrency, TTL) | 20 min | 
| Code review, documentation, and polish | 15 min |
| **Total  Time** | 2h |

## Getting started

### Prerequisites
- go1.22.2

### Build and execution
```
go mod tidy
go test -v -count=1 ./src/
```

#### With code coverage profile:
```
go test -coverprofile=code_coverage.out -v -count=1 ./src/
```
##### Export coverage to html:
```
go tool cover -html=code_coverage.out
```

Current cache.go coverage is 96.1%. It isn't hard to reach 100%, but I simply ran out of time.


## Review findings
During review activities following issues have been identified and resolved:
- Cache was able to indefinitely grow and could lead to memory overflow. Initialy it was not clear where to perform the housekeeping (because performance would be degraded).
  - After the review, I realized that the best place to do this is before adding an entry to the cache. At this stage, we perform the housekeeping and evaluate the used cache memory. If used cache memory is above a certain threshold, caching doesn't happen, however, deduplication still works for the concurrent requests that are not cached due to memory reasons. From user perspective, the cache will get slightly slower because it either needs to fetch or wait for the loading cache from another fetch request.
    - This was fixed and coverage was added.
- Many coding style issues have been identified and corrected. It's very tempting to use lock defer within functions called by fetch. Indeed, by design, this is the best practice ; however, I do think it's much easier to inspect concurrency with explicit locks and unlocks as it is.

## Current limitations
The biggest limitation of the current solution is the housekeeper. It basically locks the cache entry completely, and will basically underperform. Unfortunately, for the sake of time, I have no time to continue investigating this, but by far this is a scalability limiting factor.
