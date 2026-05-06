# Enclaive Coding Challenge 1

This repository aims in solving Coding challenge 1 for enclaive interview process developed by Joao Brito:
https://docs.google.com/document/d/1xCU4lTI28lcYm7_n_H5Z7mR4l322-H-Z3Qpw3zlN0bQ/edit?tab=t.0

## Table of contents

## Planning

An initial planning session has been made to estimate the amount of time necessary to perform this task. Following table contains the estimation:

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
| Code review, documentation, and polish | 10 min |
| **Total Estimated Time** | 2h |

## SDL (Software Development Life Cycle)

One of the requirements for the project is to forbid the usage of LLM. Therefore I will use the formal SDL process to model the problem.

### Requirements
The problem in cause is a web server proxy, capable of:
- On the fly fetching of HTTP content per TTL (Time to Leave)
- Caching of duplicated requests by using internal memory
- Prevent deduplication during caching. This means that only a HTTP request shall be executed at a time, with a life of TTL, even if many requests are executed
- cache statics metrics shall be made available
- The deliverable has just a form of a golang test file

### Design
The problem description already defines three interfaces, within cache.go. This are:
- NewCache
    - Cache contructor. Idealy it should have a cache clean-up housekeeper.
- Fetch
    - This is where the cache fetching algorithm will be stored. It shall have the following high-level steps:
        1. It shall check if the requested URL exists in the internal cache structure
        1.1. If it exists, it shall check if it's within the configured TTL. If not, it shall be cleaned
        2. It shall check if the requested URL is being loaded by another Fetch request. If yes, it shall wait for it and use the loading cached version
        3. If the URL is neither cached nor being loaded, an HTTP request shall be initiated accordingly with a mechanism that prevents deduplication, by informing other Fetch requests for the same URL from step 2.
- Stats
    - Three stats are necessary:
        1. hits: For cache hits. The counter gets incremented when a cache entry exists within the registered TT
        2. misses: For cache misses. The counter gets incremented each time a given URL doesn't exist in the cache
        3. entries: The number of cache entries existing at the instant

### Implementation
All implementation details will be stored in the code. The executable will be mainly a unit test file called cache_test.go

## Getting started

### Prerequisites
- go1.22.2

### Build and execution
```
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

Current cache.go coverage is 94.4%.

## Licence
