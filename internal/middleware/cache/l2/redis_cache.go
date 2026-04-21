package l2

import (
	"bytes"
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/ugorji/go/codec"
)

var mh codec.MsgpackHandle

type CachedResponse struct {
	Status  int               `codec:"s"`
	Headers map[string]string `codec:"h"`
	Body    []byte            `codec:"b"`
}

type Cache struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Cache {
	return &Cache{rdb: rdb}
}

func (c *Cache) Get(ctx context.Context, key string) (*CachedResponse, error) {
	data, err := c.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var resp CachedResponse
	dec := codec.NewDecoderBytes(data, &mh)
	if err := dec.Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Cache) Set(ctx context.Context, key string, resp *CachedResponse, ttl time.Duration) error {
	var buf bytes.Buffer
	enc := codec.NewEncoder(&buf, &mh)
	if err := enc.Encode(resp); err != nil {
		return nil
	}
	return c.rdb.Set(ctx, key, buf.Bytes(), ttl).Err()
}

func (c *Cache) Delete(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

func (c *Cache) Scan(ctx context.Context, pattern string) ([]string, error) {
	var keys []string
	iter := c.rdb.Scan(ctx, 0, pattern, 100).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	return keys, iter.Err()
}
