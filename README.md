# Distributed Rate Limiter in Go & Redis

A production-ready, highly concurrent HTTP rate-limiting middleware written in Go. It utilizes Redis to share state across multiple server instances, ensuring accurate limits across distributed architectures.

## 🚀 Features

- **Sliding Window Log Algorithm**: Eliminates the "boundary spike" flaw found in traditional fixed-window limiters by using Redis Sorted Sets (`ZSET`).
- **Distributed & Stateless**: Application instances do not hold state. Multiple Go servers can safely share the same Redis backend.
- **Atomic Operations**: Leverages Redis Pipelines (`TxPipeline`) to execute validation, insertion, and cleanup in a single network round-trip, preventing race conditions.
- **Fail-Open Design**: If the Redis connection fails, the middleware gracefully fails open, ensuring your application remains available even if rate limiting goes offline.
- **Informational Headers**: Automatically injects `X-RateLimit-Limit` and `X-RateLimit-Remaining` headers into responses.

## 🛠️ Architecture

When an incoming request hits the middleware, the following Redis transaction occurs atomically:

1. `ZREMRANGEBYSCORE`: Removes all timestamps older than the rolling time window.
2. `ZADD`: Adds the current request's nanosecond timestamp to the sorted set.
3. `ZCARD`: Counts the remaining valid requests in the window.
4. `EXPIRE`: Refreshes the TTL of the key to prevent memory leaks in Redis.

## 📁 Project Structure

```text
go-redis-rate-limiter/
├── cmd/
│   └── api/
│       └── main.go           # The entry point for our HTTP server
├── limiter/
│   ├── limiter.go            # The Redis sliding window middleware logic
│   └── limiter_test.go       # The table-driven unit tests
├── docker-compose.yml        # Orchestrates the Go API and Redis containers
├── Dockerfile                # Multi-stage build for the Go API
├── go.mod
├── go.sum
└── README.md
```

## 🐳 Running with Docker (Recommended)

The easiest way to run this project is using Docker Compose. This will automatically build the Go application and spin up a linked Redis instance.

### 1. `Dockerfile`

Create a `Dockerfile` in the root of your project:

```dockerfile
# Build stage
FROM golang:1.26.1-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o main ./cmd/api

# Run stage
FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/main .
EXPOSE 8080
CMD ["./main"]
```

### 2. `docker-compose.yml`

Create a `docker-compose.yml` in the root of your project:

```yaml
version: "3.8"

services:
  api:
    build: .
    ports:
      - "8080:8080"
    environment:
      - REDIS_ADDR=redis:6379 # Points to the Redis container
    depends_on:
      - redis

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
```

### 3. Update `cmd/api/main.go` for Docker

Ensure your main function dynamically reads the Redis address from the environment:

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yusupscopes/rate-limit-ready-middleware/limiter"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	rateLimiter := limiter.New(rdb, 50, time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Success! You bypassed the distributed rate limiter.\n"))
	})

	log.Println("Server starting on :8080...")
	log.Fatal(http.ListenAndServe(":8080", rateLimiter.Middleware(mux)))
}
```

### 4. Start the Application

Simply run the following command in your terminal:

```bash
docker-compose up --build
```

The server will start on `http://localhost:8080` with a default limit of 50 requests per minute.

## Usage

### Basic usage (defaults: IP-based key, headers on, fail-open)

The simplest way to use this library is to construct a `Limiter` with a Redis client, a maximum number of requests, and a window duration. By default, it:

- Derives keys from the client IP (with conservative `X-Forwarded-For` handling).
- Emits `X-RateLimit-Limit` and `X-RateLimit-Remaining` headers.
- Fails open if Redis is unavailable (your app continues serving traffic).

```go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yusupscopes/rate-limit-ready-middleware/limiter"
)

func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 100 requests per minute per client
	rateLimiter := limiter.New(rdb, 100, time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK\n"))
	})

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", rateLimiter.Middleware(mux)))
}
```

### Advanced usage: custom key + fail-closed

For production systems you often want to:

- Rate-limit by user ID or API key instead of IP.
- Decide what happens if Redis is down (fail-open vs fail-closed).
- Optionally customize header names.

You can do this via functional options:

```go
userKeyFunc := func(r *http.Request) string {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		return "anonymous"
	}
	return userID
}

rateLimiter := limiter.New(
	rdb,
	50,
	time.Minute,
	limiter.WithKeyFunc(userKeyFunc),
	limiter.WithFailureMode(limiter.FailClosed),
	limiter.WithHeaders(true, "X-RateLimit-Limit", "X-RateLimit-Remaining"),
)
```

You can then wrap any `http.Handler` (or router) with `rateLimiter.Middleware(...)` as shown above.

## 🚦 Load Testing

To see the rate limiter in action and verify the boundary spike protection, you can use a load-testing tool like [hey](https://github.com/rakyll/hey).

1. Install `hey`:
   ```bash
   go install [github.com/rakyll/hey@latest](https://github.com/rakyll/hey@latest)
   ```
2. Blast the server with 200 requests from 10 concurrent workers:
   ```bash
   hey -n 200 -c 10 http://localhost:8080/
   ```

**Expected Output:**
You should see exactly 50 requests succeed with an `HTTP 200`, and exactly 150 requests blocked with an `HTTP 429 Too Many Requests`.

```text
Status code distribution:
  [200] 50 responses
  [429] 150 responses
```
