package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func Key(r *http.Request, vary []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s", r.Method, r.Host, r.URL.Path, sortedQuery(r.URL.Query()))
	for _, v := range vary {
		fmt.Fprintf(h, "|%s=%s", v, r.Header.Get(v))
	}
	return "cache:" + hex.EncodeToString(h.Sum(nil))
}

func sortedQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vals := q[k]
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, "&")
}
