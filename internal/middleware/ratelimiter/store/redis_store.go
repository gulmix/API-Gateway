package store

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Result struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
}

type Store struct {
	rdb   *redis.Client
	clock func() float64

	fixedWindow   *redis.Script
	tokenBucket   *redis.Script
	slidindWindow *redis.Script
	leakyBucket   *redis.Script
}

func New(rdb *redis.Client) *Store {
	return NewWithClock(rdb, func() float64 {
		return float64(time.Now().UnixNano()) / 1e9
	})
}

func NewWithClock(rdb *redis.Client, clock func() float64) *Store {
	return &Store{
		rdb:           rdb,
		clock:         clock,
		fixedWindow:   redis.NewScript(luaFixedWindow),
		tokenBucket:   redis.NewScript(luaTokenBucket),
		slidindWindow: redis.NewScript(luaSlidingWindow),
		leakyBucket:   redis.NewScript(luaLeakyBucket),
	}
}

const luaFixedWindow = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])

local current = redis.call("INCR", key)
if current == 1 then
	redis.call("EXPIRE", key, window)
end

if current > limit then
	return {current, 0}
end
return {current, 1}
`

func (s *Store) AllowFixedWindow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	windowSec := int64(window.Seconds())

	vals, err := s.fixedWindow.Run(ctx, s.rdb, []string{key}, limit, windowSec).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	current := vals[0]
	allowed := vals[1] == 1
	remaining := int64(limit) - current
	if remaining < 0 {
		remaining = 0
	}

	var retryAfter time.Duration
	if !allowed {
		if ttl, err := s.rdb.TTL(ctx, key).Result(); err == nil && ttl > 0 {
			retryAfter = ttl
		}
	}

	return Result{Allowed: allowed, Remaining: remaining, RetryAfter: retryAfter}, nil
}

const luaTokenBucket = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

local data = redis.call("HMGET", key, "tokens", "last_refill")
local tokens = tonumber(data[1])
local last_refill = tonumber(data[2])

if tokens == nil then
	tokens = capacity
	last_refill = now
end

local elapsed = now - last_refill
local refilled = elapsed * refill_rate
tokens = math.min(capacity, tokens + refilled)

local allowed = 0
if tokens >= 1 then
	tokens = tokens - 1
	allowed = 1
end

redis.call("HMSET", key, "tokens", tokens, "last_refill", now)
redis.call("EXPIRE", key, math.ceil(capacity / refill_rate) + 10)

return {math.floor(tokens), allowed}
`

func (s *Store) AllowTokenBucket(ctx context.Context, key string, capacity int, refillRate float64) (Result, error) {
	now := s.clock()
	vals, err := s.tokenBucket.Run(ctx, s.rdb, []string{key}, capacity, refillRate, now).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	remaining := vals[0]
	allowed := vals[1] == 1

	var retryAfter time.Duration
	if !allowed && refillRate > 0 {
		retryAfter = time.Duration(float64(time.Second) / refillRate)
	}

	return Result{Allowed: allowed, Remaining: remaining, RetryAfter: retryAfter}, nil
}

const luaSlidingWindow = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2]) * 1000
local now_ms = tonumber(ARGV[3])
local oldest = now_ms - window_ms

redis.call("ZREMRANGEBYSCORE", key, "-inf", oldest)
local current = redis.call("ZCARD", key)

if current >= limit then
	return {current, 0}
end

local member = now_ms .. ":" .. math.random(1, 1000000)
redis.call("ZADD", key, now_ms, member)
redis.call("PEXPIRE", key, window_ms + 1000)

return {current + 1, 1}
`

func (s *Store) AllowSlidingWindow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	nowMs := int64(s.clock() * 1000)
	windowSec := int64(window.Seconds())

	vals, err := s.slidindWindow.Run(ctx, s.rdb, []string{key}, limit, windowSec, nowMs).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	current := vals[0]
	allowed := vals[1] == 1
	remainig := int64(limit) - current
	if remainig < 0 {
		remainig = 0
	}

	var retryAfter time.Duration
	if !allowed {
		retryAfter = time.Second
	}

	return Result{Allowed: allowed, Remaining: remainig, RetryAfter: retryAfter}, nil
}

const luaLeakyBucket = `
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

local data = redis.call("HMGET", key, "queue", "last_drip")
local queue = tonumber(data[1])
local last_drip = tonumber(data[2])

if queue == nil then 
	queue = 0
	last_drip = now
end

local elapsed = now - last_drip
local leaked = math.floor(elapsed * rate)
queue = math.max(0, queue - leaked)
last_drip = now

local allowed = 0
if queue < capacity then
	queue = queue + 1
	allowed = 1
end

redis.call("HMSET", key, "queue", queue, "last_drip", last_drip)
redis.call("EXPIRE", key, math.ceil(capacity / rate) + 10)

return {queue, allowed}
`

func (s *Store) AllowLeakyBucket(ctx context.Context, key string, rate float64, capacity int) (Result, error) {
	now := s.clock()
	vals, err := s.leakyBucket.Run(ctx, s.rdb, []string{key}, rate, capacity, now).Int64Slice()
	if err != nil {
		return Result{}, err
	}

	queueSize := vals[0]
	allowed := vals[1] == 1
	remaining := int64(capacity) - queueSize
	if remaining < 0 {
		remaining = 0
	}

	var retryAfter time.Duration
	if !allowed && rate > 0 {
		retryAfter = time.Duration(float64(time.Second) / rate)
	}

	return Result{Allowed: allowed, Remaining: remaining, RetryAfter: retryAfter}, nil
}
