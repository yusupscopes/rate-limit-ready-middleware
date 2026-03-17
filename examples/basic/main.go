package main

import (
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yusupscopes/rate-limit-ready-middleware/limiter"
)

// basic example: per-IP limiting with default options.
func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	rateLimiter := limiter.New(rdb, 100, time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK\n"))
	})

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", rateLimiter.Middleware(mux)))
}
