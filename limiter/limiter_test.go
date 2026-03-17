package limiter

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
)

func ExampleLimiter_basic() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	rateLimiter := New(rdb, 100, time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("OK\n")); err != nil {
			log.Printf("failed to write response: %v", err)
		}
	})

	log.Fatal(http.ListenAndServe(":8080", rateLimiter.Middleware(mux)))
}

func ExampleLimiter_withOptions() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	logger := log.New(log.Writer(), "[rate-limiter] ", log.LstdFlags)

	userKeyFunc := func(r *http.Request) string {
		if id := r.Header.Get("X-User-ID"); id != "" {
			return id
		}
		return "anonymous"
	}

	rateLimiter := New(
		rdb,
		50,
		time.Minute,
		WithKeyFunc(userKeyFunc),
		WithFailureMode(FailClosed),
		WithLogger(logger),
	)

	_ = rateLimiter
}

func TestLimiter_Middleware(t *testing.T) {
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	windowDuration := time.Minute

	mockTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mockTimeNano := mockTime.UnixNano()

	// Calculate the exact strings our mock will expect
	windowStartStr := strconv.FormatInt(mockTime.Add(-windowDuration).UnixNano(), 10)
	memberStr := strconv.FormatInt(mockTimeNano, 10)

	tests := []struct {
		name              string
		ip                string
		mockSetup         func(mock redismock.ClientMock, key string)
		expectedStatus    int
		expectedLimit     string
		expectedRemaining string
	}{
		{
			name: "Allowed - Under Limit",
			ip:   "192.168.1.1",
			mockSetup: func(mock redismock.ClientMock, key string) {
				mock.ExpectTxPipeline()
				mock.ExpectZRemRangeByScore(key, "-inf", windowStartStr).SetVal(0)
				mock.ExpectZAdd(key, redis.Z{Score: float64(mockTimeNano), Member: memberStr}).SetVal(1)
				mock.ExpectZCard(key).SetVal(3) // Simulate 3 requests so far (under limit of 5)
				mock.ExpectExpire(key, windowDuration).SetVal(true)
				mock.ExpectTxPipelineExec()
			},
			expectedStatus:    http.StatusOK,
			expectedLimit:     "5",
			expectedRemaining: "2",
		},
		{
			name: "Denied - Over Limit",
			ip:   "192.168.1.2",
			mockSetup: func(mock redismock.ClientMock, key string) {
				mock.ExpectTxPipeline()
				mock.ExpectZRemRangeByScore(key, "-inf", windowStartStr).SetVal(0)
				mock.ExpectZAdd(key, redis.Z{Score: float64(mockTimeNano), Member: memberStr}).SetVal(1)
				mock.ExpectZCard(key).SetVal(6) // Simulate 6 requests (over limit of 5)
				mock.ExpectExpire(key, windowDuration).SetVal(true)
				mock.ExpectTxPipelineExec()
			},
			expectedStatus:    http.StatusTooManyRequests,
			expectedLimit:     "5",
			expectedRemaining: "0",
		},
		{
			name: "Redis Down - Fail Open (default)",
			ip:   "192.168.1.3",
			mockSetup: func(mock redismock.ClientMock, key string) {
				mock.ExpectTxPipeline()
				mock.ExpectZRemRangeByScore(key, "-inf", windowStartStr).SetErr(errors.New("redis connection refused"))
			},
			expectedStatus:    http.StatusOK, // App should survive
			expectedLimit:     "",            // Headers shouldn't exist if Redis failed
			expectedRemaining: "",
		},
		{
			name: "Redis Down - Fail Closed",
			ip:   "192.168.1.4",
			mockSetup: func(mock redismock.ClientMock, key string) {
				mock.ExpectTxPipeline()
				mock.ExpectZRemRangeByScore(key, "-inf", windowStartStr).SetErr(errors.New("redis connection refused"))
			},
			expectedStatus:    http.StatusServiceUnavailable,
			expectedLimit:     "", // we don't set headers when Redis is down
			expectedRemaining: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := redismock.NewClientMock()

			var (
				allowedCalled bool
				limitedCalled bool
				errorCalled   bool
			)

			limiter := New(
				db,
				5,
				windowDuration,
				WithOnAllowed(func(key string, r *http.Request) { allowedCalled = true }),
				WithOnLimited(func(key string, r *http.Request) { limitedCalled = true }),
				WithOnError(func(err error) { errorCalled = true }),
			)
			limiter.Now = func() time.Time { return mockTime }

			if tt.name == "Redis Down - Fail Closed" {
				WithFailureMode(FailClosed)(limiter)
			}

			key := fmt.Sprintf("ratelimit:%s", tt.ip)
			tt.mockSetup(mock, key)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.ip
			rr := httptest.NewRecorder()

			handler := limiter.Middleware(nextHandler)
			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("wrong status code: got %v want %v", status, tt.expectedStatus)
			}

			if tt.expectedLimit != "" {
				if limit := rr.Header().Get("X-RateLimit-Limit"); limit != tt.expectedLimit {
					t.Errorf("wrong limit header: got %v want %v", limit, tt.expectedLimit)
				}
				if remaining := rr.Header().Get("X-RateLimit-Remaining"); remaining != tt.expectedRemaining {
					t.Errorf("wrong remaining header: got %v want %v", remaining, tt.expectedRemaining)
				}
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unfulfilled redis mock expectations: %s", err)
			}

			switch tt.name {
			case "Allowed - Under Limit":
				if !allowedCalled {
					t.Errorf("expected onAllowed to be called")
				}
				if limitedCalled {
					t.Errorf("did not expect onLimited to be called")
				}
				if errorCalled {
					t.Errorf("did not expect onError to be called")
				}
			case "Denied - Over Limit":
				if !limitedCalled {
					t.Errorf("expected onLimited to be called")
				}
				if allowedCalled {
					t.Errorf("did not expect onAllowed to be called")
				}
				if errorCalled {
					t.Errorf("did not expect onError to be called")
				}
			case "Redis Down - Fail Open (default)", "Redis Down - Fail Closed":
				if !errorCalled {
					t.Errorf("expected onError to be called")
				}
			}
		})
	}
}
