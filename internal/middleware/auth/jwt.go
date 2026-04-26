package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gulmix/apigateway/internal/config"
)

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwksCache struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	ttl       time.Duration
	url       string
}

func newJWKSCache(url string, ttl time.Duration) *jwksCache {
	return &jwksCache{url: url, ttl: ttl, keys: map[string]*rsa.PublicKey{}}
}

func (j *jwksCache) get(kid string) (*rsa.PublicKey, error) {
	j.mu.RLock()
	key, ok := j.keys[kid]
	stale := time.Since(j.fetchedAt) > j.ttl
	j.mu.RUnlock()

	if ok && !stale {
		return key, nil
	}

	if err := j.refresh(); err != nil {
		return nil, err
	}

	j.mu.RLock()
	key, ok = j.keys[kid]
	j.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown kid: %s", kid)
	}
	return key, nil
}

func (j *jwksCache) refresh() error {
	resp, err := http.Get(j.url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var parsed jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return err
	}

	fresh := make(map[string]*rsa.PublicKey, len(parsed.Keys))
	for _, k := range parsed.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := rsaPublicKey(k)
		if err != nil {
			continue
		}
		fresh[k.Kid] = pub
	}

	j.mu.Lock()
	j.keys = fresh
	j.fetchedAt = time.Now()
	j.mu.Unlock()
	return nil
}

func rsaPublicKey(k jwkKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	e := int(new(big.Int).SetBytes(eBytes).Int64())
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

type jwtClaims struct {
	jwt.RegisteredClaims
	Scopes []string `json:"scopes"`
}

type jwtMiddleware struct {
	cache    *jwksCache
	issuer   string
	audience string
}

func newJWTMiddleware(cfg config.JWTConfig) (*jwtMiddleware, error) {
	ttl := cfg.JWKSCacheTTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}

	m := &jwtMiddleware{
		cache:    newJWKSCache(cfg.JWKSUrl, ttl),
		issuer:   cfg.Issuer,
		audience: cfg.Audience,
	}

	_ = m.cache.refresh()
	return m, nil
}

func (m *jwtMiddleware) tryAuth(c *gin.Context) bool {
	header := c.GetHeader("Authorization")
	if len(header) < 8 || header[:7] != "Bearer " {
		return false
	}
	raw := header[7:]

	unverified, _, err := jwt.NewParser().ParseUnverified(raw, &jwtClaims{})
	if err != nil {
		return false
	}
	kid, _ := unverified.Header["kid"].(string)

	pubKey, err := m.cache.get(kid)
	if err != nil {
		return false
	}

	token, err := jwt.ParseWithClaims(raw, &jwtClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return pubKey, nil
	},
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return false
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok {
		return false
	}

	c.Set(CtxType, "jwt")
	c.Set(CtxUser, claims.Subject)
	c.Set(CtxScopes, claims.Scopes)
	return true
}
