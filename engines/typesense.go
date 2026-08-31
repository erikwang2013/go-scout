package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/erikwang2013/go-scout"
)

// TypesenseEngine speaks the Typesense HTTP API: POST /collections/<c>/documents
// to import, DELETE /collections/<c>/documents/<id> to remove, and
// POST /collections/<c>/documents/search to query. It mirrors TypesenseEngine
// plus the AdvancedTypesenseEngine filter/sort/facet/vector extensions.
type TypesenseEngine struct {
	cfg    *scout.Config
	client *http.Client
	base   string
	key    string
	action string
}

// MaxTotalResults is the configured cap on fetched results.
func (e *TypesenseEngine) MaxTotalResults() int {
	return e.cfg.Int("typesense.max_total_results", 1000)
}

// NewTypesense builds an engine from the "typesense" config section. The base
// URL comes from the first entry of typesense.client-settings.nodes.
func NewTypesense(cfg *scout.Config) *TypesenseEngine {
	if cfg == nil {
		cfg = scout.DefaultConfig()
	}
	return &TypesenseEngine{
		cfg:    cfg,
		client: HTTPClient(30*time.Second, "", "", false),
		base:   strings.TrimRight(tsNodeBase(cfg), "/"),
		key:    cfg.String("typesense.client-settings.api_key", "xyz"),
		action: cfg.String("typesense.import_action", "upsert"),
	}
}

// Name returns the driver name.
func (e *TypesenseEngine) Name() string { return "typesense" }

func (e *TypesenseEngine) headers() map[string]string {
	h := map[string]string{}
	if e.key != "" {
		h["X-TYPESENSE-API-KEY"] = e.key
	}
	return h
}

// Update imports models into their collection with the configured action.
func (e *TypesenseEngine) Update(ctx context.Context, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	path := e.base + "/collections/" + url.PathEscape(IndexName(models[0], e.cfg)) +
		"/documents?action=" + url.QueryEscape(e.action)
	body, err := json.Marshal(tsDocuments(models))
	if err != nil {
		return fmt.Errorf("scout: typesense update: encode documents: %w", err)
	}
	// The import endpoint replies with a JSON array of per-document statuses,
	// not an object, so it needs DoBytes rather than DoJSON.
	raw, err := DoBytes(ctx, e.client, http.MethodPost, path, e.headers(), body)
	if err != nil {
		return fmt.Errorf("scout: typesense update: %w", err)
	}
	return tsImportErr(raw)
}

// tsImportErr fails the import if any document was rejected, matching the PHP
// engine's per-document success check.
func tsImportErr(raw []byte) error {
	items, err := jsonUnmarshalList(raw)
	if err != nil {
		return fmt.Errorf("scout: typesense update: decode import result: %w", err)
	}
	for _, item := range items {
		m, _ := item.(map[string]any)
		if ok, has := m["success"].(bool); has && !ok {
			errText := ""
			if s, ok := m["error"].(string); ok {
				errText = s
			}
			return fmt.Errorf("scout: typesense update: import error: %s", errText)
		}
	}
	return nil
}

func jsonUnmarshalList(raw []byte) ([]any, error) {
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes each model from its collection. Missing documents are ignored,
// matching the PHP engine's catch-all.
func (e *TypesenseEngine) Delete(ctx context.Context, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	name := IndexName(models[0], e.cfg)
	for _, m := range models {
		id := url.PathEscape(scout.KeyString(m.ScoutKey()))
		_, err := DoJSON(ctx, e.client, http.MethodDelete, e.base+"/collections/"+url.PathEscape(name)+"/documents/"+id, e.headers(), nil)
		if err != nil {
			continue
		}
	}
	return nil
}

// Search runs the query described by b.
func (e *TypesenseEngine) Search(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.run(ctx, b, b.GetLimit(), 1)
}

// AdvancedSearch runs the full advanced query (filters, sorts, facets, vector).
func (e *TypesenseEngine) AdvancedSearch(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.Search(ctx, b)
}

// Paginate runs the query for one page using Typesense's page/per_page.
func (e *TypesenseEngine) Paginate(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	if perPage <= 0 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	return e.run(ctx, b, perPage, page)
}

// MapIDs extracts the primary keys from a result.
func (e *TypesenseEngine) MapIDs(results *scout.Result) []any {
	out := make([]any, 0, len(results.Hits))
	for _, h := range results.Hits {
		out = append(out, h.ID)
	}
	return out
}

// Map hydrates models for a result, preserving engine order.
func (e *TypesenseEngine) Map(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	ids := e.MapIDs(results)
	models, err := b.ModelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return orderByIDPos(ids, models), nil
}

// GetTotalCount returns the total match count from a result.
func (e *TypesenseEngine) GetTotalCount(results *scout.Result) int { return results.Total }

// Flush deletes the whole collection for the model.
func (e *TypesenseEngine) Flush(ctx context.Context, model scout.ScoutModel) error {
	path := e.base + "/collections/" + url.PathEscape(IndexName(model, e.cfg))
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, e.headers(), nil)
	return tsErr("flush", err)
}

// CreateIndex creates a collection. Fields come from options["fields"] or
// options["collection-schema"]["fields"]; otherwise a wildcard auto field is used.
func (e *TypesenseEngine) CreateIndex(ctx context.Context, name string, options map[string]any) (any, error) {
	fields := tsAnyList(options["fields"])
	if len(fields) == 0 {
		if schema, ok := options["collection-schema"].(map[string]any); ok {
			fields = tsAnyList(schema["fields"])
		}
	}
	if len(fields) == 0 {
		// ponytail: no schema given; one wildcard auto field covers any payload.
		// Pass options["fields"] when explicit typing or vector dims are needed.
		fields = []any{map[string]any{"name": "*", "type": "auto"}}
	}
	body := map[string]any{"name": name, "fields": fields}
	if dsf, ok := options["default_sorting_field"].(string); ok && dsf != "" {
		body["default_sorting_field"] = dsf
	}
	return DoJSON(ctx, e.client, http.MethodPost, e.base+"/collections", e.headers(), body)
}

// DeleteIndex drops a collection.
func (e *TypesenseEngine) DeleteIndex(ctx context.Context, name string) (any, error) {
	path := e.base + "/collections/" + url.PathEscape(name)
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, e.headers(), nil)
	if err != nil {
		return nil, fmt.Errorf("scout: typesense delete index: %w", err)
	}
	return nil, nil
}

// --- request building ---

// run issues one /documents/search request and parses the response.
func (e *TypesenseEngine) run(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	body := tsParams(b, perPage, page)
	if out := b.InvokeCallback(ctx, body); out != nil {
		if r, ok := out.(*scout.Result); ok {
			return r, nil
		}
		if raw, ok := out.(map[string]any); ok {
			return e.parse(raw, b), nil
		}
	}
	path := e.base + "/collections/" + url.PathEscape(b.GetIndex()) + "/documents/search"
	raw, err := DoJSON(ctx, e.client, http.MethodPost, path, e.headers(), body)
	if err != nil && strings.Contains(err.Error(), "returned 404") {
		// The collection may not exist yet; create it, as the PHP engine does.
		if _, cerr := e.CreateIndex(ctx, b.GetIndex(), nil); cerr != nil {
			return nil, fmt.Errorf("scout: typesense search: %w", err)
		}
		raw, err = DoJSON(ctx, e.client, http.MethodPost, path, e.headers(), body)
	}
	if err != nil {
		return nil, fmt.Errorf("scout: typesense search: %w", err)
	}
	return e.parse(raw, b), nil
}

// tsParams assembles the /documents/search body. Builder options are merged
// first so the computed keys win.
func tsParams(b *scout.Builder, perPage, page int) map[string]any {
	if perPage <= 0 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	body := map[string]any{}
	for k, v := range b.GetOptions() {
		body[k] = v
	}
	body["q"] = b.Query
	if fb := b.SearchFields(); len(fb) > 0 {
		body["query_by"] = strings.Join(fb, ",")
	}
	body["per_page"] = perPage
	body["page"] = page
	if f := tsFilters(b); f != "" {
		body["filter_by"] = f
	}
	if s := tsSorts(b); s != "" {
		body["sort_by"] = s
	}
	if fb := tsFacetBy(b); fb != "" {
		body["facet_by"] = fb
		if _, ok := body["max_facet_values"]; !ok {
			body["max_facet_values"] = 10
		}
	}
	if a := tsAggregates(b); a != "" {
		body["aggregate"] = a
	}
	// Advanced parity: highlight and field projection options, mirroring the
	// PHP engine's getHighlightFields/getIncludeFields/getExcludeFields.
	if hf := tsOptionString(b, "highlight_fields", "highlight_full_fields"); hf != "" {
		body["highlight_full_fields"] = hf
	}
	if inc := tsOptionString(b, "include_fields", "include"); inc != "" {
		body["include_fields"] = inc
	}
	if exc := tsOptionString(b, "exclude_fields", "exclude"); exc != "" {
		body["exclude_fields"] = exc
	}
	// Advanced parity: geo conditions become location_* params as well as
	// filters, mirroring extractTypesenseGeoSearch.
	for _, c := range b.GetAdvancedWheres() {
		for k, v := range tsGeoParams(c) {
			body[k] = v
		}
	}
	tsVectorSearch(b, body)
	return body
}

// tsFilters renders wheres, whereIns, whereNotIns and advancedWheres into a
// Typesense filter_by expression joined by &&.
func tsFilters(b *scout.Builder) string {
	var parts []string
	for _, field := range tsKeys(b.Wheres) {
		value := b.Wheres[field]
		if v, ok := value.([]any); ok && len(v) > 0 {
			parts = append(parts, fmt.Sprintf("%s:[%s]", field, tsList(v)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:=%s", field, tsLit(value)))
	}
	for _, field := range tsKeys(b.GetWhereIns()) {
		if v := b.GetWhereIns()[field]; len(v) > 0 {
			parts = append(parts, fmt.Sprintf("%s:[%s]", field, tsList(v)))
		}
	}
	for _, field := range tsKeys(b.GetWhereNotIns()) {
		if v := b.GetWhereNotIns()[field]; len(v) > 0 {
			parts = append(parts, fmt.Sprintf("%s:!=[%s]", field, tsList(v)))
		}
	}
	for _, c := range b.GetAdvancedWheres() {
		f := tsAdvanced(c)
		if f == "" {
			continue
		}
		if c.Boolean == "not" {
			f = "!" + f
		}
		parts = append(parts, f)
	}
	return strings.Join(parts, " && ")
}

// --- helpers ---

func tsDocuments(models []scout.ScoutModel) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		doc := m.ToSearchableArray()
		if doc == nil {
			doc = map[string]any{}
		}
		doc["id"] = m.ScoutKey()
		out = append(out, doc)
	}
	return out
}

// tsLit renders a value as a Typesense filter literal: strings double-quoted
// with " and \ escaped, booleans lowercase, nil as null, numbers bare.
func tsLit(v any) string {
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
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return tsLit(fmt.Sprint(t))
	}
}

func tsList(vals []any) string {
	lits := make([]string, 0, len(vals))
	for _, v := range vals {
		lits = append(lits, tsLit(v))
	}
	return strings.Join(lits, ", ")
}

func tsDir(column, dir string) string {
	if dir == "desc" {
		return column + ":desc"
	}
	return column + ":asc"
}

func tsStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return strings.Split(t, ",")
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

func tsAnyList(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func tsInt(v any) int {
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

func tsFloat(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func tsFloatOrNil(v any) any {
	if f, ok := v.(float64); ok {
		return f
	}
	return nil
}

func tsFloatOr(v any, def float64) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	if i, ok := v.(int); ok {
		return float64(i)
	}
	return def
}

func tsNum(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// tsNodeBase builds protocol://host:port from the first configured node.
func tsNodeBase(cfg *scout.Config) string {
	a, ok := cfg.Get("typesense.client-settings.nodes", nil).([]any)
	if !ok || len(a) == 0 {
		return "http://127.0.0.1:8108"
	}
	n, ok := a[0].(map[string]any)
	if !ok {
		return "http://127.0.0.1:8108"
	}
	host := tsNodeStr(n["host"], "127.0.0.1")
	port := tsNodeStr(n["port"], "8108")
	proto := tsNodeStr(n["protocol"], "http")
	if host == "" {
		return "http://127.0.0.1:8108"
	}
	return proto + "://" + host + ":" + port
}

func tsNodeStr(v any, def string) string {
	if v == nil {
		return def
	}
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fmt.Sprint(v)
}

func tsKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func tsErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("scout: typesense %s: %w", op, err)
}
