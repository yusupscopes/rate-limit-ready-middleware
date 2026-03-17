package main

import (
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yusupscopes/rate-limit-ready-middleware/limiter"
)

// custom-logger-hooks example: demonstrate logger and hooks.
func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	logger := log.New(log.Writer(), "[rate-limiter] ", log.LstdFlags)

	rateLimiter := limiter.New(
		rdb,
		20,
		time.Minute,
		limiter.WithLogger(logger),
		limiter.WithOnAllowed(func(key string, r *http.Request) {
			logger.Printf("allowed %s %s key=%s", r.Method, r.URL.Path, key)
		}),
		limiter.WithOnLimited(func(key string, r *http.Request) {
			logger.Printf("limited %s %s key=%s", r.Method, r.URL.Path, key)
		}),
		limiter.WithOnError(func(err error) {
			logger.Printf("redis error: %v", err)
		}),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK\n"))
	})

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", rateLimiter.Middleware(mux)))
}
