package engines

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/erikwang2013/go-scout"
)

// AlgoliaEngine speaks the Algolia REST API: POST /1/indexes/<i>/objects to
// upsert, POST /1/indexes/<i>/batch to delete by id, POST /1/indexes/<i>/query
// to search. It mirrors AlgoliaEngine; the PHP plugin ships no
// AdvancedAlgoliaEngine, so AdvancedSearch delegates to Search.
type AlgoliaEngine struct {
	cfg      *scout.Config
	client   *http.Client
	appID    string
	key      string
	header   string
	identify bool
}

// NewAlgolia builds an engine from the "algolia" config section (v4 API).
func NewAlgolia(cfg *scout.Config) *AlgoliaEngine { return NewAlgolia4(cfg) }

// NewAlgolia3 builds an engine against the legacy v3 API.
func NewAlgolia3(cfg *scout.Config) *AlgoliaEngine { return newAlgolia(cfg) }

// NewAlgolia4 builds an engine against the v4 API.
func NewAlgolia4(cfg *scout.Config) *AlgoliaEngine { return newAlgolia(cfg) }

// ponytail: NewAlgolia3 and NewAlgolia4 share one engine; the v3/v4 search
// endpoints accept the same query body, so the drivers are aliases.
func newAlgolia(cfg *scout.Config) *AlgoliaEngine {
	if cfg == nil {
		cfg = scout.DefaultConfig()
	}
	appID := strings.TrimSpace(cfg.String("algolia.app_id", cfg.String("algolia.id", "")))
	key := strings.TrimSpace(cfg.String("algolia.admin_key", cfg.String("algolia.secret", "")))
	if appID == "" {
		panic("scout: algolia.app_id is required (set ALGOLIA_APP_ID)")
	}
	if key == "" {
		panic("scout: algolia.admin_key is required (set ALGOLIA_SECRET)")
	}
	return &AlgoliaEngine{
		cfg:      cfg,
		client:   HTTPClient(30*time.Second, "", "", cfg.Bool("algolia.skip_tls_verify", false)),
		appID:    appID,
		key:      key,
		header:   cfg.String("algolia.api_key_header", "X-Algolia-API-Key"),
		identify: cfg.Identify(),
	}
}

// Name returns the driver name.
func (e *AlgoliaEngine) Name() string { return "algolia" }

// headers builds the request headers. With SCOUT_IDENTIFY on it also forwards
// who is searching, exactly as laravel-scout's defaultAlgoliaHeaders does: the
// user key as X-Algolia-UserToken and the client IP as X-Forwarded-For (public
// addresses only). Both come off the context — see scout.WithUser and
// scout.WithClientIP — and are simply absent when the caller set neither.
func (e *AlgoliaEngine) headers(ctx context.Context) map[string]string {
	h := map[string]string{
		e.header:                   e.key,
		"X-Algolia-Application-Id": e.appID,
	}
	if !e.identify {
		return h
	}
	if user, ok := scout.UserFrom(ctx); ok {
		h["X-Algolia-UserToken"] = user
	}
	if ip, ok := scout.ClientIPFrom(ctx); ok {
		h["X-Forwarded-For"] = ip
	}
	return h
}

func (e *AlgoliaEngine) index(model scout.ScoutModel) string {
	return url.PathEscape(IndexName(model, e.cfg))
}

// Update upserts models; empty documents are skipped, matching the PHP filter.
func (e *AlgoliaEngine) Update(ctx context.Context, models []scout.ScoutModel) error {
	docs := algoliaDocuments(models)
	if len(docs) == 0 {
		return nil
	}
	path := e.base() + "/1/indexes/" + e.index(models[0]) + "/objects?batch=1111"
	_, err := DoJSON(ctx, e.client, http.MethodPost, path, e.headers(ctx), map[string]any{"objects": docs})
	return algoliaErr("update", err)
}

// Delete removes models by their object ids.
func (e *AlgoliaEngine) Delete(ctx context.Context, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	ids := make([]any, 0, len(models))
	for _, m := range models {
		ids = append(ids, scout.KeyString(m.ScoutKey()))
	}
	path := e.base() + "/1/indexes/" + e.index(models[0]) + "/batch?requestBodyType=objects"
	_, err := DoJSON(ctx, e.client, http.MethodPost, path, e.headers(ctx), map[string]any{"objectIDs": ids})
	return algoliaErr("delete", err)
}

// Search runs the query described by b.
func (e *AlgoliaEngine) Search(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	body := algoliaBody(b, b.GetLimit(), b.GetOffset())
	if out := b.InvokeCallback(ctx, body); out != nil {
		if r, ok := out.(*scout.Result); ok {
			return r, nil
		}
		if raw, ok := out.(map[string]any); ok {
			return e.parse(raw, b), nil
		}
	}
	name := b.GetIndex()
	if name == "" && b.Model != nil {
		name = IndexName(b.Model, e.cfg)
	}
	path := e.base() + "/1/indexes/" + url.PathEscape(name) + "/query"
	raw, err := DoJSON(ctx, e.client, http.MethodPost, path, e.headers(ctx), body)
	if err != nil {
		return nil, algoliaErr("search", err)
	}
	return e.parse(raw, b), nil
}

// AdvancedSearch runs the full advanced query. Algolia shares one API surface
// with its advanced sibling, so this is the base search.
func (e *AlgoliaEngine) AdvancedSearch(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.Search(ctx, b)
}

// Paginate runs the query for one page. Algolia pages are 0-based.
func (e *AlgoliaEngine) Paginate(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	if perPage <= 0 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	body := algoliaBody(b, perPage, (page-1)*perPage)
	name := b.GetIndex()
	if name == "" && b.Model != nil {
		name = IndexName(b.Model, e.cfg)
	}
	raw, err := DoJSON(ctx, e.client, http.MethodPost, e.base()+"/1/indexes/"+url.PathEscape(name)+"/query", e.headers(ctx), body)
	if err != nil {
		return nil, algoliaErr("search", err)
	}
	return e.parse(raw, b), nil
}

// MapIDs extracts the primary keys from a result.
func (e *AlgoliaEngine) MapIDs(results *scout.Result) []any {
	out := make([]any, 0, len(results.Hits))
	for _, h := range results.Hits {
		out = append(out, h.ID)
	}
	return out
}

// Map hydrates models for a result, preserving engine order.
func (e *AlgoliaEngine) Map(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	models, err := b.ModelsByIDs(ctx, e.MapIDs(results))
	if err != nil {
		return nil, err
	}
	return orderByIDPos(e.MapIDs(results), models), nil
}

// GetTotalCount returns the total match count from a result.
func (e *AlgoliaEngine) GetTotalCount(results *scout.Result) int { return results.Total }

// Flush deletes every object in the model's index.
func (e *AlgoliaEngine) Flush(ctx context.Context, model scout.ScoutModel) error {
	path := e.base() + "/1/indexes/" + e.index(model) + "/objects"
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, e.headers(ctx), nil)
	return algoliaErr("flush", err)
}

// CreateIndex is unavailable: Algolia indexes are provisioned by its API's
// separate settings flow, not by scout.
func (e *AlgoliaEngine) CreateIndex(ctx context.Context, name string, options map[string]any) (any, error) {
	return nil, scout.ErrNotSupported
}

// DeleteIndex drops an index.
func (e *AlgoliaEngine) DeleteIndex(ctx context.Context, name string) (any, error) {
	path := e.base() + "/1/indexes/" + url.PathEscape(name)
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, e.headers(ctx), nil)
	if err != nil {
		return nil, algoliaErr("delete index", err)
	}
	return nil, nil
}

// GetAggregations returns the facet distributions for a query.
func (e *AlgoliaEngine) GetAggregations(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	res, err := e.Search(ctx, b)
	if err != nil {
		return nil, err
	}
	return res.Aggregations, nil
}

// GetFacets returns the facet distributions for a query.
func (e *AlgoliaEngine) GetFacets(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	return e.GetAggregations(ctx, b)
}

// --- request building ---

// base is the API endpoint: algolia.host (ALGOLIA_HOST) when set — a proxy or an
// Algolia-compatible endpoint — otherwise the app's own cluster. An empty value
// falls back too, since Config.String returns a stored "" rather than the
// default once the key exists.
func (e *AlgoliaEngine) base() string {
	host := strings.TrimRight(e.cfg.String("algolia.host", ""), "/")
	if host == "" {
		host = "https://" + e.appID + ".algolia.net"
	}
	return host
}

// algoliaDocuments turns models into Algolia objects, each carrying its
// objectID, and drops empty documents.
func algoliaDocuments(models []scout.ScoutModel) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		doc := m.ToSearchableArray()
		if len(doc) == 0 {
			continue
		}
		doc["objectID"] = scout.KeyString(m.ScoutKey())
		out = append(out, doc)
	}
	return out
}

// algoliaBody assembles the /query body; options merge first, query applied
// last so it wins.
func algoliaBody(b *scout.Builder, limit, offset int) map[string]any {
	if limit <= 0 {
		limit = 20
	}
	body := map[string]any{}
	for k, v := range b.GetOptions() {
		body[k] = v
	}
	body["hitsPerPage"] = limit
	body["page"] = offset / limit
	if f := algoliaFilters(b); f != "" {
		body["filters"] = f
	}
	if s := algoliaSort(b); s != "" {
		body["sortBy"] = s
	}
	// ponytail: filters/sortBy only. Facets, geo and distinct live in
	// attributesForFaceting, which must be configured once at index creation.
	if fs := b.SearchFields(); len(fs) > 0 {
		body["attributesToSearch"] = fs
	}
	if hl := b.GetOptions()["attributesToHighlight"]; hl != nil {
		body["attributesToHighlight"] = hl
	}
	if e := b.GetOptions()["engine"]; e != nil {
		body["engine"] = e
	}
	body["query"] = b.Query
	return body
}

// algoliaFilters renders wheres, whereIns and whereNotIns as Algolia filter
// clauses joined by AND, in the PHP engine's order.
func algoliaFilters(b *scout.Builder) string {
	var parts []string
	for _, field := range meiliKeys(b.Wheres) {
		v := b.Wheres[field]
		if list, ok := v.([]any); ok && len(list) > 0 {
			parts = append(parts, fmt.Sprintf("%s IN [%s]", field, algoliaLitList(list)))
			continue
		}
		if v == nil {
			parts = append(parts, field+" IS NULL")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", field, algoliaLit(v)))
	}
	for _, field := range meiliKeys(b.GetWhereIns()) {
		if list := b.GetWhereIns()[field]; len(list) > 0 {
			parts = append(parts, fmt.Sprintf("%s IN [%s]", field, algoliaLitList(list)))
		}
	}
	// ponytail: whereNotIns becomes Algolia's 0=1 no-op exactly like the PHP
	// engine, which never emits a NOT IN clause. Real exclusion needs >=/<= or
	// filtersExcludes.
	for _, field := range meiliKeys(b.GetWhereNotIns()) {
		if list := b.GetWhereNotIns()[field]; len(list) > 0 {
			parts = append(parts, "0=1")
			_ = field
		}
	}
	return strings.Join(parts, " AND ")
}

// algoliaSort renders the first order clause as "field:asc|desc".
func algoliaSort(b *scout.Builder) string {
	for _, o := range b.GetOrders() {
		if o.Column != "" {
			return o.Column + ":" + meiliD(o.Direction)
		}
	}
	return ""
}

// algoliaLitList joins values for an IN clause.
func algoliaLitList(vals []any) string {
	lits := make([]string, 0, len(vals))
	for _, v := range vals {
		lits = append(lits, algoliaLit(v))
	}
	return strings.Join(lits, ", ")
}

// algoliaLit renders a value as an Algolia filter literal, mirroring meiliLit:
// strings double-quoted with " and \ escaped, numbers and booleans bare,
// timestamps as unix seconds (Algolia compares dates as integer timestamps).
func algoliaLit(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case string:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(t) + `"`
	case time.Time:
		return strconv.FormatInt(t.Unix(), 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return algoliaLit(fmt.Sprint(t))
	}
}

// --- response parsing ---

// parse converts a raw Algolia /query response into a scout.Result.
func (e *AlgoliaEngine) parse(raw map[string]any, b *scout.Builder) *scout.Result {
	var hits []map[string]any
	for _, h := range meiliAnyList(raw["hits"]) {
		doc, ok := h.(map[string]any)
		if !ok {
			continue
		}
		hits = append(hits, map[string]any{
			"_id":       doc["objectID"],
			"_score":    meiliNum(doc, "_score"),
			"_source":   doc,
			"highlight": doc["_highlightResult"],
		})
	}
	res := Result(hits, meiliInt(raw["nbHits"]))
	if m, ok := raw["facets"].(map[string]any); ok {
		res.Aggregations = m
	}
	if t := meiliInt(raw["processingTimeMS"]); t > 0 {
		res.Took = t
	}
	if t := meiliInt(raw["processingTimeMs"]); t > 0 {
		res.Took = t
	}
	res.Raw = raw
	for _, p := range b.GetResultProcessors() {
		res = p(res)
	}
	return res
}

// --- helpers ---

func algoliaErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("scout: algolia %s: %w", op, err)
}
