package limiter

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
)

func benchmarkLimiter(b *testing.B, zcardCount int64) {
	db, mock := redismock.NewClientMock()

	windowDuration := time.Minute
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	nowNano := now.UnixNano()
	windowStartStr := strconv.FormatInt(now.Add(-windowDuration).UnixNano(), 10)
	memberStr := strconv.FormatInt(nowNano, 10)

	key := "ratelimit:127.0.0.1"

	mock.ExpectTxPipeline()
	mock.ExpectZRemRangeByScore(key, "-inf", windowStartStr).SetVal(0)
	mock.ExpectZAdd(key, redis.Z{Score: float64(nowNano), Member: memberStr}).SetVal(1)
	mock.ExpectZCard(key).SetVal(zcardCount)
	mock.ExpectExpire(key, windowDuration).SetVal(true)
	mock.ExpectTxPipelineExec()

	lim := New(db, 5, windowDuration)
	lim.Now = func() time.Time { return now }

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler := lim.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(rr, req)
	}
}

func BenchmarkLimiter_Allow(b *testing.B) {
	benchmarkLimiter(b, 3)
}

func BenchmarkLimiter_Denied(b *testing.B) {
	benchmarkLimiter(b, 6)
}
