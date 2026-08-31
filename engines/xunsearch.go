package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/erikwang2013/go-scout"
)

// XunSearchEngine speaks XunSearch's HTTP daemon protocol: the index daemon
// accepts form commands (cmd=add|del|clean with the document as a JSON string),
// and the search daemon answers JSON for q/project queries. It mirrors the PHP
// plugin's XunsearchEngine; NewAdvancedXunSearch resolves to the same engine,
// which also implements the advanced surface. Projects are defined by config
// files on the server, so index management is not supported.
type XunSearchEngine struct {
	cfg        *scout.Config
	client     *http.Client
	indexBase  string
	searchBase string
	charset    string
}

// NewXunSearch builds an engine from the "xunsearch" config section.
func NewXunSearch(cfg *scout.Config) *XunSearchEngine {
	if cfg == nil {
		cfg = scout.DefaultConfig()
	}
	return &XunSearchEngine{
		cfg:        cfg,
		client:     HTTPClient(30*time.Second, "", "", false),
		indexBase:  strings.TrimRight(cfg.String("xunsearch.index_host", "http://127.0.0.1"), "/") + ":" + cfg.String("xunsearch.index_port", "8383"),
		searchBase: strings.TrimRight(cfg.String("xunsearch.search_host", "http://127.0.0.1"), "/") + ":" + cfg.String("xunsearch.search_port", "8384"),
		charset:    cfg.String("xunsearch.charset", "utf-8"),
	}
}

// NewAdvancedXunSearch builds the advanced variant; XunSearch shares one
// engine surface, so this is the base engine.
func NewAdvancedXunSearch(cfg *scout.Config) *XunSearchEngine { return NewXunSearch(cfg) }

// Name returns the driver name.
func (e *XunSearchEngine) Name() string { return "xunsearch" }

// Update upserts models one by one via the index daemon's cmd=add.
func (e *XunSearchEngine) Update(ctx context.Context, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	for _, m := range models {
		doc := m.ToSearchableArray()
		if doc == nil {
			doc = map[string]any{}
		}
		doc[scout.KeyNameOf(m)] = m.ScoutKey()
		raw, err := json.Marshal(doc)
		if err != nil {
			return xsErr("update", err)
		}
		if _, err := e.index(ctx, "add", IndexName(m, e.cfg), string(raw)); err != nil {
			return xsErr("update", err)
		}
	}
	return nil
}

// Delete removes models via the index daemon's cmd=del. PHP parity: the
// payload is the bare primary key, not a JSON document.
func (e *XunSearchEngine) Delete(ctx context.Context, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	for _, m := range models {
		if _, err := e.index(ctx, "del", IndexName(m, e.cfg), scout.KeyString(m.ScoutKey())); err != nil {
			return xsErr("delete", err)
		}
	}
	return nil
}

// Search runs the query described by b against the search daemon.
func (e *XunSearchEngine) Search(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.run(ctx, b, b.GetLimit(), b.GetOffset())
}

// AdvancedSearch runs the full advanced query; XunSearch shares one search
// surface, so this is the base search.
func (e *XunSearchEngine) AdvancedSearch(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.Search(ctx, b)
}

// Paginate runs the query for one page.
func (e *XunSearchEngine) Paginate(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	if perPage <= 0 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	return e.run(ctx, b, perPage, (page-1)*perPage)
}

// MapIDs extracts the primary keys from a result.
func (e *XunSearchEngine) MapIDs(results *scout.Result) []any {
	out := make([]any, 0, len(results.Hits))
	for _, h := range results.Hits {
		out = append(out, h.ID)
	}
	return out
}

// Map hydrates models for a result, preserving engine order.
func (e *XunSearchEngine) Map(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	ids := e.MapIDs(results)
	models, err := b.ModelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return orderByIDPos(ids, models), nil
}

// GetTotalCount returns the total match count from a result.
func (e *XunSearchEngine) GetTotalCount(results *scout.Result) int { return results.Total }

// Flush clears the model's project via the index daemon's cmd=clean.
func (e *XunSearchEngine) Flush(ctx context.Context, model scout.ScoutModel) error {
	if _, err := e.index(ctx, "clean", IndexName(model, e.cfg), ""); err != nil {
		return xsErr("flush", err)
	}
	return nil
}

// CreateIndex is unsupported: XunSearch projects are defined by config files
// on the server, not created over the wire.
func (e *XunSearchEngine) CreateIndex(ctx context.Context, name string, options map[string]any) (any, error) {
	return nil, scout.ErrNotSupported
}

// DeleteIndex is unsupported: the daemon has no drop-project command.
// Flush clears a project's data instead.
func (e *XunSearchEngine) DeleteIndex(ctx context.Context, name string) (any, error) {
	return nil, scout.ErrNotSupported
}

// --- request building ---

// index posts one command to the index daemon and returns the plain-text reply.
// The daemon signals failures in 200 responses ("ERR:..."/"FAIL:..." etc), so
// a non-empty reply that is not "OK" is surfaced as an error.
func (e *XunSearchEngine) index(ctx context.Context, cmd, project, data string) ([]byte, error) {
	form := url.Values{"cmd": {cmd}, "project": {project}}
	if data != "" {
		form.Set("data", data)
	}
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	reply, err := DoBytes(ctx, e.client, http.MethodPost, e.indexBase, headers, []byte(form.Encode()))
	if err != nil {
		return nil, err
	}
	if msg := strings.TrimSpace(string(reply)); msg != "" && !strings.HasPrefix(msg, "OK") {
		return nil, fmt.Errorf("scout: xunsearch index daemon: %s", msg)
	}
	return reply, nil
}

// run issues one search-daemon query and parses the JSON response.
func (e *XunSearchEngine) run(ctx context.Context, b *scout.Builder, limit, offset int) (*scout.Result, error) {
	q := url.Values{
		"q":        {xsQuery(b)},
		"project":  {b.GetIndex()},
		"charset":  {e.charset},
		"per_page": {fmt.Sprint(limit)},
		"start":    {fmt.Sprint(offset)},
	}
	for k, v := range b.GetOptions() {
		if s, ok := v.(string); ok && k != "fields" {
			q.Set(k, s)
		}
	}
	// Advanced parity: the facet config rides along as facet/facet_size params,
	// as the PHP engine's buildFacets reads the same config after searching.
	if f, s := xsFacetParams(b); f != "" {
		if q.Get("facet") == "" {
			q.Set("facet", f)
		}
		if q.Get("facet_size") == "" {
			q.Set("facet_size", s)
		}
	}
	if out := b.InvokeCallback(ctx, q); out != nil {
		if r, ok := out.(*scout.Result); ok {
			return r, nil
		}
		if raw, ok := out.(map[string]any); ok {
			return e.parse(raw, b), nil
		}
	}
	path := e.searchBase + "/search?" + q.Encode()
	raw, err := DoJSON(ctx, e.client, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("scout: xunsearch search: %w", err)
	}
	return e.parse(raw, b), nil
}

// parse converts a raw search-daemon response into a scout.Result.
func (e *XunSearchEngine) parse(raw map[string]any, b *scout.Builder) *scout.Result {
	key := scout.KeyNameOf(b.Model)
	var hits []map[string]any
	for _, d := range xsAnyList(raw["docs"]) {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		fields, _ := dm["fields"].(map[string]any)
		if fields == nil {
			fields = map[string]any{}
		}
		id := fields[key]
		if id == nil {
			id = dm["docid"]
		}
		hits = append(hits, map[string]any{
			"_id":     id,
			"_score":  xsNum(dm["percent"]),
			"_source": fields,
		})
	}
	// The daemon reports total matches in "total" (what the PHP engine reads via
	// getLastCount); "count" is the older field name, kept as fallback.
	total := xsInt(raw["total"])
	if total == 0 {
		total = xsInt(raw["count"])
	}
	res := Result(hits, total)
	if t := xsInt(raw["cost"]); t > 0 {
		res.Took = t
	}
	// Advanced parity: the daemon reports facet counts next to the docs when a
	// facet param was sent; surface them under Aggregations["facets"].
	if fc, ok := raw["facets"].(map[string]any); ok {
		res.Aggregations = map[string]any{"facets": fc}
	}
	res.Raw = raw
	for _, p := range b.GetResultProcessors() {
		res = p(res)
	}
	return res
}

// --- helpers ---

func xsAnyList(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func xsInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	}
	return 0
}

func xsNum(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return 0
}

func xsErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("scout: xunsearch %s: %w", op, err)
}
