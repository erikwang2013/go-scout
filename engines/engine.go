// Package engines implements the search backends for go-scout: the built-in
// database/collection/null engines and REST clients for OpenSearch,
// Elasticsearch, Meilisearch, Typesense, Algolia and XunSearch.
//
// No third-party search SDKs are used: every remote engine speaks its real
// HTTP API directly, keeping the module dependency-free apart from the
// standard library.
package engines

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/erikwang2013/go-scout"
)

// HTTPClient builds a *http.Client with sensible timeouts, optional basic auth
// and optional TLS verification skipping. Shared by the remote engines.
func HTTPClient(timeout time.Duration, username, password string, skipTLSVerify bool) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: skipTLSVerify}, // #nosec G402 - caller opts in via config
		MaxIdleConns:        32,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	var rt http.RoundTripper = transport
	if username != "" || password != "" {
		rt = &authRoundTripper{base: transport, user: username, pass: password}
	}
	return &http.Client{Timeout: timeout, Transport: rt}
}

// authRoundTripper injects HTTP basic auth into every request.
type authRoundTripper struct {
	base       http.RoundTripper
	user, pass string
}

func (a *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.SetBasicAuth(a.user, a.pass)
	return a.base.RoundTrip(req)
}

// DoJSON issues an HTTP request with an optional JSON body and decodes a JSON
// object response. A non-2xx status returns an error carrying the body.
func DoJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("scout: encode request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("scout: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scout: http %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("scout: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("scout: %s %s returned %d: %s", method, url, resp.StatusCode, trimBody(raw))
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("scout: decode response: %w (body %s)", err, trimBody(raw))
	}
	return out, nil
}

// DoBytes issues a request and returns the raw body, for non-JSON endpoints
// (e.g. XunSearch's plain-text query protocol).
func DoBytes(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("scout: build request: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scout: http %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("scout: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("scout: %s %s returned %d: %s", method, url, resp.StatusCode, trimBody(raw))
	}
	return raw, nil
}

// Documents turns models into index documents, each carrying its id.
func Documents(models []scout.ScoutModel) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		doc := m.ToSearchableArray()
		if doc == nil {
			doc = map[string]any{}
		}
		doc["_id"] = m.ScoutKey()
		out = append(out, doc)
	}
	return out
}

// Keys extracts the primary keys from a model set.
func Keys(models []scout.ScoutModel) []any {
	out := make([]any, 0, len(models))
	for _, m := range models {
		out = append(out, m.ScoutKey())
	}
	return out
}

// IndexName returns the index name for a model under the given prefix.
func IndexName(m scout.ScoutModel, cfg *scout.Config) string {
	prefix := ""
	if cfg != nil {
		prefix = cfg.Prefix()
	}
	return scout.SearchableAsOf(m, prefix)
}

// Result converts a raw engine payload into scout.Result, copying hit ids,
// scores and sources. Shared by the remote engines.
func Result(hits []map[string]any, total int, extra ...map[string]any) *scout.Result {
	res := &scout.Result{Total: total, Hits: make([]scout.Hit, 0, len(hits))}
	for _, h := range hits {
		res.Hits = append(res.Hits, Hit(h))
	}
	for _, m := range extra {
		for k, v := range m {
			switch k {
			case "aggregations":
				res.Aggregations = v.(map[string]any)
			case "suggestions":
				res.Suggestions = v.(map[string]any)
			case "raw":
				res.Raw = v
			case "max_score":
				if f, ok := v.(float64); ok {
					res.MaxScore = &f
				}
			}
		}
	}
	return res
}

// Hit converts a raw hit map into a scout.Hit.
func Hit(h map[string]any) scout.Hit {
	out := scout.Hit{ID: h["_id"], Source: map[string]any{}}
	if s, ok := h["_score"].(float64); ok {
		out.Score = s
	}
	if src, ok := h["_source"].(map[string]any); ok {
		out.Source = src
	} else if src, ok := h["source"].(map[string]any); ok {
		out.Source = src
	}
	if idx, ok := h["_index"].(string); ok {
		out.Index = idx
	}
	if hl, ok := h["highlight"].(map[string]any); ok {
		out.Highlight = hl
	}
	if vs, ok := h["_vector_score"].(float64); ok {
		out.VectorScore = &vs
	}
	return out
}

// BoolPtr returns a pointer to b, for optional result fields.
func BoolPtr(b bool) *bool { return &b }

func trimBody(raw []byte) string {
	s := string(raw)
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
