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

	// 2. Initialize Rate Limiter: 50 requests per 1 minute
	rateLimiter := limiter.New(rdb, 50, time.Minute)

	// 3. Set up routes
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Success! You bypassed the distributed rate limiter.\n"))
	})

	// 4. Wrap the router with the middleware
	log.Println("Server starting on :8080...")
	log.Fatal(http.ListenAndServe(":8080", rateLimiter.Middleware(mux)))
}