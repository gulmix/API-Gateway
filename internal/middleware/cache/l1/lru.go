package l1

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

type entry struct {
	body    []byte
	headers map[string]string
	status  int
	exp     time.Time
}

type Cache struct {
	inner *lru.Cache[string, entry]
}

func New(maxItems int) (*Cache, error) {
	inner, err := lru.New[string, entry](maxItems)
	if err != nil {
		return nil, err
	}
	return &Cache{inner: inner}, nil
}

func (c *Cache) Get(key string) (body []byte, headers map[string]string, status int, ok bool) {
	e, found := c.inner.Get(key)
	if !found {
		return nil, nil, 0, false
	}
	if time.Now().After(e.exp) {
		c.inner.Remove(key)
		return nil, nil, 0, false
	}
	return e.body, e.headers, e.status, true
}

func (c *Cache) Set(key string, body []byte, headers map[string]string, status int, ttl time.Duration) {
	c.inner.Add(key, entry{
		body:    body,
		headers: headers,
		status:  status,
		exp:     time.Now().Add(ttl),
	})
}

func (c *Cache) Delete(key string) {
	c.inner.Remove(key)
}

func (c *Cache) Len() int {
	return c.inner.Len()
}

func (c *Cache) Purge() {
	c.inner.Purge()
}
