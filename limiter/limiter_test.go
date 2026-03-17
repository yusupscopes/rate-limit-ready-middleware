package limiter

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
)

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

			limiter := New(db, 5, windowDuration)
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

			// 5. Assertions
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
		})
	}
}
