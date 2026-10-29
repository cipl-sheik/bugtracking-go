package bugtrack

import (
	"net/http"
	"sort"
	"strings"
)

// HTTPMiddleware reports a panic from next and then re-panics.
// The request map omits the query string and credential headers.
func (c *Client) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer c.Recover(r.Context(),
			WithRequest(RequestFields(r)),
			WithTransaction(TransactionName(r)),
		)
		next.ServeHTTP(w, r)
	})
}

// TransactionName returns "METHOD /path" for an incoming request.
func TransactionName(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return strings.TrimSpace(r.Method + " " + r.URL.Path)
}

// RequestFields returns method, path, host, and headers safe to store.
// Query strings are omitted. Authorization, Cookie, and API key headers are omitted.
func RequestFields(r *http.Request) map[string]any {
	if r == nil || r.URL == nil {
		return nil
	}
	out := map[string]any{
		"method": r.Method,
		"url":    r.URL.Path,
	}
	if r.Host != "" {
		out["host"] = r.Host
	}
	if headers := scrubbedHeaders(r.Header); len(headers) > 0 {
		out["headers"] = headers
	}
	return out
}

func scrubbedHeaders(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		if sensitiveHeader(k) {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	if len(keys) > maxHeaders {
		keys = keys[:maxHeaders]
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = truncateBytes(strings.Join(h.Values(k), ","), maxHeaderValueLen)
	}
	return out
}

func sensitiveHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-bugtrack-key", "x-api-key":
		return true
	default:
		return false
	}
}
