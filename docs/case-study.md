# 📌 Case Study: Distributed Rate Limiter (Go + Redis)

## 🧩 Context

Modern APIs and microservices often run across multiple instances behind load balancers. In this environment, enforcing consistent rate limits becomes challenging because each server instance operates independently.

This project was built to create a **production-ready, distributed rate-limiting middleware** that works reliably across horizontally scaled systems.

---

## 🚨 Problem

Traditional rate-limiting approaches (e.g., in-memory or fixed-window counters):

- Break in distributed environments due to lack of shared state
- Suffer from **boundary spike issues** (traffic bursts at window edges)
- Are vulnerable to **race conditions** under high concurrency
- Can reduce system reliability if dependencies (like Redis) fail

**Core Challenge:**  
How can we enforce **accurate, fair, and fault-tolerant rate limits** across multiple stateless services?

---

## 🏗️ Architecture

### Key Design Decisions

- **Go HTTP Middleware** → lightweight, composable integration
- **Redis as centralized state store** → shared across instances
- **Sliding Window Log algorithm** → precise request tracking
- **Redis Sorted Sets (ZSET)** → time-ordered request logs
- **Atomic pipeline transactions** → consistency under concurrency

### Request Flow

1. Incoming request hits middleware
2. Middleware executes Redis transaction:
   - Remove expired entries (`ZREMRANGEBYSCORE`)
   - Add current request timestamp (`ZADD`)
   - Count active requests (`ZCARD`)
   - Refresh TTL (`EXPIRE`)
3. Decision returned instantly → allow or reject request

**Result:**  
Stateless application layer with centralized, consistent rate control.

---

## ⚙️ Implementation

### Core Techniques

#### 1. Sliding Window via Redis ZSET

- Stores request timestamps (nanoseconds) per client
- Eliminates fixed-window burst loopholes

#### 2. Atomic Operations with `TxPipeline`

- Combines:
  - `ZREMRANGEBYSCORE`
  - `ZADD`
  - `ZCARD`
  - `EXPIRE`
- Executed in a **single network round-trip**
- Prevents race conditions

#### 3. Flexible Keying Strategy

- Default: IP-based limiting
- Customizable:
  - User ID
  - API key
  - Request headers

#### 4. Resilience Design

- **Fail-open (default):** system remains available if Redis fails
- **Fail-closed (optional):** stricter enforcement (reject requests)

#### 5. Observability Hooks

- Hooks for:
  - Allowed requests
  - Limited requests
  - Errors
- Enables integration with logging and monitoring systems

#### 6. Production Readiness

- Dockerized setup (Go + Redis)
- Environment-based configuration
- Load-tested under concurrency

---

## 📈 Outcome

### Technical Results

- ✅ Accurate distributed rate limiting across multiple instances
- ✅ Eliminated boundary spike issues with sliding window approach
- ✅ Ensured race-condition-free execution using atomic pipelines
- ✅ Maintained high availability with fail-open fallback

### Performance Validation

Load test results (200 requests, concurrency = 10):

- **200 OK:** 50 requests
- **429 Too Many Requests:** 150 requests

### Business / Engineering Impact

- Improves API reliability and abuse protection
- Prevents traffic spikes from overwhelming services
- Supports scalable microservices architecture
- Ready for production deployment with minimal integration

---

## 🎯 Key Takeaway

> I don’t just build middleware — I design scalable, fault-tolerant systems that work reliably in distributed environments.
