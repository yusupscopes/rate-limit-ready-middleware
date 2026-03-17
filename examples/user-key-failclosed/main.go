package main

import (
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yusupscopes/rate-limit-ready-middleware/limiter"
)

// user-key-failclosed example: limit per X-User-ID and fail closed on Redis errors.
func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	userKeyFunc := func(r *http.Request) string {
		if id := r.Header.Get("X-User-ID"); id != "" {
			return id
		}
		return "anonymous"
	}

	rateLimiter := limiter.New(
		rdb,
		50,
		time.Minute,
		limiter.WithKeyFunc(userKeyFunc),
		limiter.WithFailureMode(limiter.FailClosed),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, user\n"))
	})

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", rateLimiter.Middleware(mux)))
}
