# ApiGateway

A production-grade API Gateway built in Go with Gin, featuring multi-algorithm rate limiting, two-layer caching, dynamic load balancing, Kubernetes service discovery, dual auth, and full observability.

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Tech Stack](#tech-stack)
4. [Project Structure](#project-structure)
5. [Components](#components)
   - [Rate Limiter](#rate-limiter)
   - [Cache Layer](#cache-layer)
   - [Load Balancer](#load-balancer)
   - [Service Discovery](#service-discovery)
   - [Auth Middleware](#auth-middleware)
   - [Observability](#observability)
6. [Configuration](#configuration)
7. [Request Lifecycle](#request-lifecycle)
8. [Admin API](#admin-api)
9. [Deployment (Kubernetes)](#deployment-kubernetes)
10. [Key Design Decisions](#key-design-decisions)

---

## Overview

ApiGateway sits at the edge of your infrastructure and acts as a single entry point for all upstream services. It handles:

- **Rate limiting** — four algorithms, configurable per route, scoped per IP / API key / user
- **Caching** — two-layer (L1 in-memory LRU + L2 Redis), TTL + pub/sub invalidation
- **Load balancing** — round-robin, least-connections, weighted round-robin, consistent hashing; configurable per upstream
- **Service discovery** — dynamic backend registration via Kubernetes Watch API
- **Auth** — API key validation + JWT verification, both usable simultaneously
- **Observability** — structured logging (Zap), distributed tracing (OpenTelemetry), metrics (Prometheus + Grafana)

---

## Architecture

```
                         ┌──────────────────────────────────────────────────┐
                         │              RateLimiterGateway (Gin)            │
                         │                                                  │
Internet ──► TLS ──►     │  Auth ──► Rate Limiter ──► Cache ──► LB ──► Proxy│
                         │            │                  │         │        │
                         │         Redis              L1+L2      Discovery  │
                         └──────────────────────────────────────────────────┘
                                                                    │
                               ┌────────────────────────────────────┤
                               │                │                   │
                          Service A         Service B          Service C
                         (3 pods)           (2 pods)           (1 pod)
```

**Data stores:**
- **Redis** — rate limit counters, L2 cache, API key store, pub/sub bus, distributed locks
- **In-process LRU** — L1 cache (per-pod, configurable max size + TTL)

**Control plane:**
- Kubernetes API Watch — streams endpoint changes, auto-registers/deregisters backends
- Admin HTTP API — runtime config updates, cache purge, backend management

---

## Tech Stack

| Concern              | Library / Tool                                      |
|----------------------|-----------------------------------------------------|
| HTTP framework       | `github.com/gin-gonic/gin`                          |
| Redis client         | `github.com/redis/go-redis/v9`                      |
| L1 LRU cache         | `github.com/hashicorp/golang-lru/v2`                |
| JWT                  | `github.com/golang-jwt/jwt/v5`                      |
| Kubernetes client    | `k8s.io/client-go`                                  |
| Structured logging   | `go.uber.org/zap`                                   |
| Distributed tracing  | `go.opentelemetry.io/otel` + OTLP exporter          |
| Metrics              | `github.com/prometheus/client_golang`               |
| Config               | `github.com/spf13/viper`                            |
| Testing              | `github.com/stretchr/testify` + `go test`           |
| Containerization     | Docker multi-stage build                            |
| Orchestration        | Kubernetes + Helm                                   |
| Dashboards           | Grafana (Prometheus datasource + Tempo for traces)  |

---

## Project Structure

```
RateLimiterGateway/
├── cmd/
│   └── gateway/
│       └── main.go                  # Entry point: wires all components, starts server
│
├── internal/
│   ├── config/
│   │   └── config.go                # Viper-based config loader, hot-reload support
│   │
│   ├── server/
│   │   └── server.go                # Gin engine setup, route registration, graceful shutdown
│   │
│   ├── proxy/
│   │   └── reverse_proxy.go         # httputil.ReverseProxy wrapper, header forwarding
│   │
│   ├── middleware/
│   │   │
│   │   ├── auth/
│   │   │   ├── apikey.go            # API key extraction + Redis lookup
│   │   │   ├── jwt.go               # JWT parse + claims validation (JWKS or shared secret)
│   │   │   └── auth.go              # Unified auth middleware: tries API key, falls back to JWT
│   │   │
│   │   ├── ratelimiter/
│   │   │   ├── limiter.go           # Entry point: reads route config, delegates to algorithm
│   │   │   ├── scope.go             # Extracts limit key (IP / API key / user ID)
│   │   │   ├── store/
│   │   │   │   └── redis_store.go   # Redis operations for all algorithms (atomic Lua scripts)
│   │   │   └── algorithms/
│   │   │       ├── token_bucket.go  # Refill-on-read token bucket via Redis hash
│   │   │       ├── sliding_window.go# Sorted-set based sliding window log
│   │   │       ├── fixed_window.go  # INCR + EXPIRE per window key
│   │   │       └── leaky_bucket.go  # Timestamp-based drip queue in Redis
│   │   │
│   │   ├── cache/
│   │   │   ├── cache.go             # Two-layer read/write logic, pub/sub invalidation listener
│   │   │   ├── l1/
│   │   │   │   └── lru.go           # golang-lru wrapper with TTL envelope
│   │   │   └── l2/
│   │   │       └── redis_cache.go   # Redis GET/SET/DEL with serialization
│   │   │
│   │   └── observability/
│   │       ├── logging.go           # Zap logger init, request-scoped logger middleware
│   │       ├── tracing.go           # OTel tracer init, span creation per request
│   │       └── metrics.go           # Prometheus counters/histograms, /metrics endpoint
│   │
│   ├── loadbalancer/
│   │   ├── balancer.go              # Upstream registry, algorithm dispatch, backend pool mgmt
│   │   ├── algorithms/
│   │   │   ├── round_robin.go       # Atomic counter mod len(backends)
│   │   │   ├── least_connections.go # sync/atomic active-request tracking per backend
│   │   │   ├── weighted_rr.go       # Smooth weighted round-robin (Nginx algorithm)
│   │   │   └── consistent_hash.go   # Jump consistent hash (for session affinity)
│   │   └── healthcheck/
│   │       ├── active.go            # Goroutine ticker: HTTP GET /health per backend
│   │       └── passive.go           # Circuit breaker: tracks 5xx rate, opens on threshold
│   │
│   └── discovery/
│       ├── registry.go              # In-memory backend registry, thread-safe
│       └── kubernetes.go            # client-go Watch on Endpoints, feeds registry
│
├── pkg/
│   └── redis/
│       └── client.go                # Redis client singleton with connection pool config
│
├── config/
│   ├── gateway.yaml                 # Server, Redis, auth global config
│   └── routes.yaml                  # Per-route: upstream, rate limit, cache, LB algorithm
│
├── deploy/
│   ├── Dockerfile                   # Multi-stage: builder + distroless runtime image
│   └── helm/
│       └── gateway/
│           ├── Chart.yaml
│           ├── values.yaml          # All tunable values
│           └── templates/
│               ├── deployment.yaml
│               ├── service.yaml
│               ├── configmap.yaml
│               ├── hpa.yaml         # HorizontalPodAutoscaler on CPU + custom Prometheus metric
│               ├── rbac.yaml        # ServiceAccount + ClusterRole for Watch Endpoints
│               └── servicemonitor.yaml  # Prometheus Operator scrape config
│
├── go.mod
├── go.sum
└── README.md
```

---

## Components

### Rate Limiter

The rate limiter is a Gin middleware applied before proxying. It supports four algorithms, each implemented as an atomic Redis operation via **Lua scripts** (guarantees read-modify-write atomicity without WATCH/MULTI).

#### Algorithms

| Algorithm       | Use case                                     | Redis structure              |
|-----------------|----------------------------------------------|------------------------------|
| Token Bucket    | Allow short bursts, smooth long-term rate    | Hash `{tokens, last_refill}` |
| Sliding Window  | Precise per-window count, no boundary spike  | Sorted Set `{timestamp}`     |
| Fixed Window    | Simplest, lowest memory, minor edge spike    | String with EXPIREAT         |
| Leaky Bucket    | Enforce strict constant output rate          | Hash `{last_drip, queue}`    |

#### Configuration per route (routes.yaml)

```yaml
routes:
  - path: /api/v1/search
    upstream: search-service
    rate_limit:
      algorithm: sliding_window     # token_bucket | sliding_window | fixed_window | leaky_bucket
      scope: [ip, api_key, user]    # all three applied; most restrictive wins
      limits:
        ip:      { requests: 100, window: 60s }
        api_key: { requests: 1000, window: 60s }
        user:    { requests: 500, window: 60s }
```

#### Scope resolution order

```
1. Extract user ID from JWT claims          → "user:<id>" key
2. Extract X-API-Key header                 → "apikey:<key>" key
3. Fall back to X-Forwarded-For / RemoteAddr → "ip:<addr>" key
```

All three checks run; if any scope exceeds its limit, the request is rejected with `429 Too Many Requests` and a `Retry-After` header.

---

### Cache Layer

The cache middleware intercepts **safe, idempotent** requests (GET, HEAD) before they reach the proxy. Cache is **never applied** to authenticated-user-specific responses unless explicitly opted in.

#### Two-Layer Strategy

```
Request
  │
  ▼
L1 (in-process LRU, ~10ms lookup)
  │ miss
  ▼
L2 (Redis, ~1-2ms network)
  │ miss
  ▼
Upstream (proxy)
  │
  ├──► store in L2 (SET with TTL)
  └──► store in L1 (promote hot entries)
```

- **L1** — `golang-lru/v2` with a configurable max item count (default: 10,000 entries). Each entry carries an expiry timestamp checked at read time.
- **L2** — Redis `SET key value EX <ttl>` with full response bytes serialized as MessagePack.

#### Cache Key

```
sha256( method + "|" + host + "|" + path + "|" + sorted_query_params )
```

Vary headers (e.g. `Accept`, `Accept-Language`) are optionally included per route config.

#### Invalidation

Three mechanisms, all co-existing:

1. **TTL** — Every cached entry has a hard TTL (configurable per route, default 60s).
2. **Manual purge** — Admin API `DELETE /admin/cache?key=<pattern>` performs Redis `SCAN + DEL` and broadcasts an invalidation event.
3. **Event-driven (pub/sub)** — Upstream services publish to a Redis channel `cache:invalidate`. The gateway subscribes on startup; on message receipt it deletes matching keys from both L1 and L2. Wildcard patterns supported via `SCAN` + glob match.

---

### Load Balancer

Each upstream (group of backends for one service) has its own load balancer instance and algorithm, configured independently.

#### Algorithms

**Round Robin**
```
backend = backends[ atomic_counter++ % len(backends) ]
```

**Least Connections**
```
backend = min(backends, key=active_requests)
active_requests tracked with sync/atomic, incremented on forward, decremented on response
```

**Weighted Round Robin** (Nginx smooth algorithm)
```
Each backend holds: weight (static), current_weight (dynamic)
Pick: backend with highest current_weight
After pick: current_weight -= total_weight
Each cycle: all current_weights += their static weight
Result: smooth distribution without long consecutive runs to one backend
```

**Consistent Hashing**
```
Hash ring built from backend addresses using Jump Consistent Hash
Key = client IP or session token (configurable)
Useful for: cache locality, stateful upstream services
```

#### Configuration per upstream

```yaml
upstreams:
  search-service:
    algorithm: least_connections     # round_robin | least_connections | weighted_rr | consistent_hash
    hash_key: ip                     # only for consistent_hash: ip | header:<name>
    backends: []                     # populated dynamically by service discovery
```

---

### Service Discovery

The gateway watches the **Kubernetes Endpoints API** using `client-go`'s `ListWatch` + `Informer`. No polling — changes are pushed via long-lived HTTP/2 stream.

#### Flow

```
K8s Endpoints change (pod added/removed/updated)
        │
        ▼
Informer event handler in discovery/kubernetes.go
        │
        ▼
registry.Register(upstreamName, backend) / registry.Deregister(...)
        │
        ▼
Load balancer pool updated atomically (copy-on-write slice swap)
```

#### RBAC requirements (Helm template)

```yaml
rules:
  - apiGroups: [""]
    resources: ["endpoints", "services"]
    verbs: ["get", "list", "watch"]
```

The gateway is namespace-scoped by default; cluster-wide discovery is opt-in via `values.yaml`.

#### Upstream annotation convention

Services are opted into discovery via annotation:

```yaml
# On the Kubernetes Service object
annotations:
  gateway.io/upstream: "search-service"
  gateway.io/port: "8080"
  gateway.io/weight: "10"           # for weighted_rr
```

---

### Auth Middleware

Auth runs before rate limiting. It populates the Gin context with identity claims so the rate limiter and downstream services can use them.

#### API Key Auth (`middleware/auth/apikey.go`)

1. Read `X-API-Key` header (or `?api_key=` query param as fallback).
2. `GET apikey:<key>` from Redis → returns JSON `{owner, scopes, rate_limit_tier}`.
3. If not found → `401 Unauthorized`.
4. Set `gin.Context` keys: `auth.type=apikey`, `auth.owner=<owner>`, `auth.scopes=[...]`.

Keys are stored in Redis by the admin API and can be revoked instantly by deleting the Redis key.

#### JWT Auth (`middleware/auth/jwt.go`)

1. Read `Authorization: Bearer <token>` header.
2. Fetch JWKS from configured URL (cached in-process with TTL, refreshed on `kid` miss).
3. Verify signature, expiry, issuer, audience.
4. Set `gin.Context` keys: `auth.type=jwt`, `auth.user=<sub>`, `auth.scopes=[...]`.

#### Combined strategy (`middleware/auth/auth.go`)

```
Try API Key
  ├── found + valid → proceed (API key identity)
  └── not found
       └── Try JWT
             ├── valid → proceed (JWT identity)
             └── invalid/missing → 401
```

Routes can be marked `auth: none` in config to skip entirely (public endpoints).

---

### Observability

All three pillars are wired as Gin middleware and run concurrently with zero coupling between them.

#### Structured Logging (Zap)

- Production mode: JSON output
- Development mode: human-readable colored output
- Per-request fields logged: `trace_id`, `method`, `path`, `status`, `latency_ms`, `upstream`, `cache_hit`, `rate_limit_remaining`, `user`
- Log level configurable at runtime via `PUT /admin/log-level`

#### Distributed Tracing (OpenTelemetry)

- Each request gets a root span in the Gin middleware
- Child spans created for: auth lookup, rate limit check (Redis), cache lookup (L1, L2), upstream proxy call
- Trace context propagated to upstream via `traceparent` header (W3C format)
- Exporter: OTLP gRPC → Tempo (or Jaeger)

#### Metrics (Prometheus)

All metrics exposed at `GET /metrics` (scraped by Prometheus Operator via `ServiceMonitor`).

| Metric                                    | Type      | Labels                                  |
|-------------------------------------------|-----------|-----------------------------------------|
| `gateway_requests_total`                  | Counter   | `method`, `path`, `status`, `upstream`  |
| `gateway_request_duration_seconds`        | Histogram | `method`, `path`, `upstream`            |
| `gateway_rate_limit_hits_total`           | Counter   | `scope`, `algorithm`, `route`           |
| `gateway_cache_hits_total`                | Counter   | `layer` (l1\|l2), `route`              |
| `gateway_cache_misses_total`              | Counter   | `route`                                 |
| `gateway_upstream_active_connections`     | Gauge     | `upstream`, `backend`                   |
| `gateway_upstream_health`                 | Gauge     | `upstream`, `backend` (0=down, 1=up)    |

Grafana dashboards are pre-built as JSON in `deploy/grafana/`.

---

## Configuration

### `config/gateway.yaml`

```yaml
server:
  port: 8080
  admin_port: 9090
  read_timeout: 30s
  write_timeout: 30s
  graceful_shutdown_timeout: 15s

redis:
  addr: redis:6379
  password: ""
  db: 0
  pool_size: 50
  min_idle_conns: 10

auth:
  jwt:
    jwks_url: "https://auth.example.com/.well-known/jwks.json"
    issuer: "https://auth.example.com"
    audience: "api.example.com"
    jwks_cache_ttl: 5m
  api_keys:
    header: "X-API-Key"

cache:
  l1:
    max_items: 10000
    default_ttl: 30s
  l2:
    default_ttl: 60s
  invalidation_channel: "cache:invalidate"

discovery:
  type: kubernetes
  namespace: "default"          # or "" for all namespaces
  annotation_prefix: "gateway.io"

observability:
  log_level: info               # debug | info | warn | error
  tracing:
    enabled: true
    otlp_endpoint: "otel-collector:4317"
    sampling_rate: 1.0
  metrics:
    enabled: true
    path: /metrics
```

### `config/routes.yaml`

```yaml
routes:
  - path: /api/v1/search
    methods: [GET]
    upstream: search-service
    auth: required              # required | optional | none
    cache:
      enabled: true
      ttl: 30s
      vary: [Accept-Language]
    rate_limit:
      algorithm: sliding_window
      scope: [ip, api_key, user]
      limits:
        ip:      { requests: 100, window: 60s }
        api_key: { requests: 1000, window: 60s }
        user:    { requests: 500, window: 60s }

  - path: /api/v1/orders
    methods: [POST, PUT, DELETE]
    upstream: order-service
    auth: required
    cache:
      enabled: false
    rate_limit:
      algorithm: token_bucket
      scope: [user]
      limits:
        user: { capacity: 50, refill_rate: 10, refill_interval: 1s }

upstreams:
  search-service:
    algorithm: least_connections
  order-service:
    algorithm: weighted_rr
```

---

## Request Lifecycle

```
1. TLS termination (handled by Kubernetes Ingress / Gateway API)
2. Gin router matches path → runs middleware chain:

   a. Observability Middleware
      - Assign/extract trace ID (W3C traceparent)
      - Start root OTel span
      - Attach logger with trace_id to context

   b. Auth Middleware
      - Try X-API-Key → Redis lookup
      - Else try JWT → JWKS verify
      - Inject identity into context
      - On failure → 401, end chain

   c. Rate Limiter Middleware
      - Determine scopes (IP, API key, user) from context
      - For each scope: run configured algorithm via Lua script in Redis
      - If any scope exhausted → 429 with Retry-After, end chain

   d. Cache Middleware (GET/HEAD only)
      - Check L1 (in-process LRU)
        └── hit → return cached response, record metric, end chain
      - Check L2 (Redis)
        └── hit → promote to L1, return response, end chain
      - miss → continue to proxy

   e. Load Balancer
      - Look up upstream from route config
      - Select backend via configured algorithm
      - Increment active_connections counter

   f. Reverse Proxy
      - Inject headers: X-Forwarded-For, X-Request-ID, traceparent
      - Forward request to selected backend
      - On 5xx: passive health check records failure, may open circuit breaker

   g. Cache Store (on successful response)
      - Serialize response
      - Write to L2 (Redis SET with TTL)
      - Write to L1 (LRU promote)

   h. Observability finalization
      - Decrement active_connections counter
      - Record request_duration_seconds histogram
      - Increment requests_total counter
      - Finish OTel span
      - Write structured log line
```

---

## Admin API

Exposed on a separate port (default `:9090`) — not routed through the main proxy pipeline, no auth required inside the cluster (firewall at network policy level).

| Method | Path                            | Description                                      |
|--------|---------------------------------|--------------------------------------------------|
| GET    | /admin/health                   | Liveness probe                                   |
| GET    | /admin/ready                    | Readiness probe (checks Redis + backends)        |
| GET    | /admin/backends                 | List all registered backends and health status   |
| POST   | /admin/backends/:upstream       | Manually register a backend                      |
| DELETE | /admin/backends/:upstream/:addr | Manually deregister a backend                    |
| GET    | /admin/cache/stats              | L1/L2 hit rates, size, key count                 |
| DELETE | /admin/cache                    | Purge cache by key pattern (triggers pub/sub)    |
| GET    | /admin/rate-limits/:key         | Inspect current counter state for a limit key    |
| DELETE | /admin/rate-limits/:key         | Reset rate limit counters for a key              |
| POST   | /admin/api-keys                 | Create API key (stored in Redis)                 |
| DELETE | /admin/api-keys/:key            | Revoke API key                                   |
| PUT    | /admin/log-level                | Change log level at runtime                      |
| GET    | /metrics                        | Prometheus metrics (can be moved to main port)   |

---

## Deployment (Kubernetes)

### Architecture in-cluster

```
Ingress / Gateway API
       │
       ▼
gateway Service (ClusterIP)
       │
  ┌────┴────┐
  Pod 1    Pod 2   (HPA: 2–10 replicas, scales on RPS via custom Prometheus metric)
  │         │
  └────┬────┘
       │
    Redis (StatefulSet or managed Redis)
       │
    Backends (watched via Endpoints API)
```

### Helm values (abridged)

```yaml
replicaCount: 2

image:
  repository: your-registry/rate-limiter-gateway
  tag: latest
  pullPolicy: IfNotPresent

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi

hpa:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
  customMetric:
    name: gateway_requests_per_second
    targetValue: 1000

redis:
  external: true
  addr: redis-master.redis.svc.cluster.local:6379

config:
  logLevel: info
  tracingEnabled: true
  otlpEndpoint: otel-collector.monitoring:4317
```

### Dockerfile (multi-stage)

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o gateway ./cmd/gateway

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /app/gateway /gateway
COPY --from=builder /app/config /config
ENTRYPOINT ["/gateway"]
```

---

## Implementation Phases

### Phase 5 — Service Discovery (Week 5)
- [ ] Kubernetes client-go setup with in-cluster config
- [ ] Informer/Watch on Endpoints API
- [ ] Annotation-based upstream mapping
- [ ] Registry thread-safe register/deregister
- [ ] RBAC Helm templates
- [ ] Fallback: static backend list if discovery disabled

### Phase 6 — Auth (Week 6)
- [ ] API key Redis schema + CRUD admin endpoints
- [ ] API key middleware (header extraction + Redis lookup)
- [ ] JWKS fetcher with in-process cache + refresh on unknown `kid`
- [ ] JWT validation middleware (signature, expiry, iss, aud)
- [ ] Combined auth middleware with fallback chain
- [ ] Auth identity propagation to rate limiter + upstream headers

### Phase 7 — Observability (Week 7)
- [ ] OTel tracer init with OTLP gRPC exporter
- [ ] Span creation in each middleware (auth, rate limit, cache, proxy)
- [ ] traceparent injection into upstream requests
- [ ] Prometheus metrics registration + /metrics endpoint
- [ ] All metrics from the metrics table above
- [ ] Grafana dashboard JSON for requests, latency, cache, rate limits, backends
- [ ] Runtime log level change Admin API endpoint

### Phase 8 — Kubernetes & Hardening (Week 8)
- [ ] Helm chart with all templates
- [ ] HPA with custom Prometheus metric
- [ ] ServiceMonitor for Prometheus Operator
- [ ] Network policy: admin port blocked from outside cluster
- [ ] Integration tests: full request lifecycle with real Redis (testcontainers-go)
- [ ] Load test with k6: validate rate limits, cache behavior, LB distribution
- [ ] README final review

---

## Key Design Decisions

**Why Lua scripts for rate limiting?**
Redis Lua scripts execute atomically server-side. This eliminates the TOCTOU race condition that would exist with separate GET + SET commands across concurrent gateway pods.

**Why copy-on-write for the backend pool?**
The backend list is read on every request (hot path) but written rarely (only when discovery fires). A `sync/atomic` pointer swap on a new slice avoids any lock contention on reads while keeping writes safe.

**Why two cache layers?**
L1 eliminates Redis round-trips for the hottest keys (~sub-millisecond). L2 ensures consistency across gateway pods and survives pod restarts. L1 is intentionally small and ephemeral — it is a read-throughput optimization, not the source of truth.

**Why pub/sub invalidation?**
TTL alone means stale data lives until expiry. Upstream services need a way to say "this resource changed right now." The Redis pub/sub channel is a lightweight, already-available primitive that lets any service trigger immediate invalidation across all gateway pods simultaneously.

**Why separate admin port?**
The admin API must not be rate-limited, cached, or routed through the same middleware chain as user traffic. A dedicated port lets Kubernetes NetworkPolicy restrict access to in-cluster services only, without complicating the main routing config.
