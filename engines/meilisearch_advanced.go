package engines

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/erikwang2013/go-scout"
)

// This file holds the advanced surface the PHP plugin's AdvancedMeilisearchEngine
// ships: vector search (createVectorIndex, updateVectors), engine info, facets
// and aggregations, plus the highlight/hybrid/sort/advanced-condition request
// translation and the advanced result processing.

// CreateVectorIndex creates an index plus the embedder settings that make it
// vector searchable, mirroring the PHP engine: the primaryKey comes from
// settings, and a "default" userProvided embedder is written when the server
// supports vectors (v1.3+).
func (e *MeilisearchEngine) CreateVectorIndex(ctx context.Context, index string, dimensions int, settings map[string]any) error {
	if dimensions <= 0 {
		dimensions = 1536
	}
	if settings == nil {
		settings = map[string]any{}
	}
	primary := "id"
	if pk, ok := settings["primaryKey"].(string); ok && pk != "" {
		primary = pk
	}
	if _, err := DoJSON(ctx, e.client, http.MethodPost, e.base+"/indexes", e.headers(),
		map[string]any{"uid": index, "primaryKey": primary}); err != nil {
		return meiliErr("create vector index", err)
	}
	supports, err := e.meiliSupportsVectors(ctx)
	if err != nil {
		return meiliErr("create vector index", err)
	}
	if !supports {
		return nil
	}
	// ponytail: PHP writes the full settings block, but every default it sends
	// matches Meili's own server defaults; only the embedder config is needed.
	embedder := map[string]any{"source": "userProvided", "dimensions": dimensions}
	if dt, ok := settings["documentTemplate"]; ok {
		embedder["documentTemplate"] = dt
	}
	if _, err := DoJSON(ctx, e.client, http.MethodPut, e.base+"/indexes/"+url.PathEscape(index)+"/settings", e.headers(),
		map[string]any{"embedders": map[string]any{"default": embedder}}); err != nil {
		return meiliErr("create vector index", err)
	}
	return nil
}

// UpdateVectors attaches vectors to models, mirroring the PHP engine's
// updateVectors: each document carries both _vectors and the "embedding"
// compatibility alias, then the batch is upserted and the task awaited.
func (e *MeilisearchEngine) UpdateVectors(ctx context.Context, models []scout.ScoutModel, vectors [][]float64) error {
	if len(models) == 0 {
		return nil
	}
	docs := meiliDocuments(models)
	for i := range docs {
		// The i < len(vectors) guard keeps the caller contract (models and
		// vectors are paired 1:1), but Meili's PUT /documents is a full
		// replace: a document that arrives without _vectors silently loses
		// its vector. Call UpdateVectors with a complete vector per model.
		if i < len(vectors) {
			docs[i]["_vectors"] = vectors[i]
			docs[i]["embedding"] = vectors[i] // PHP compatibility alias
		}
	}
	path := e.base + "/indexes/" + url.PathEscape(IndexName(models[0], e.cfg)) + "/documents"
	raw, err := DoJSON(ctx, e.client, http.MethodPut, path, e.headers(), docs)
	if err != nil {
		return meiliErr("update vectors", err)
	}
	return e.meiliWaitTask(ctx, raw["taskUid"])
}

// GetEngineInfo reports engine health and version, mirroring the PHP engine:
// version/stats/health plus a supportsVectors flag (v1.3+). Failures fold into
// the payload as the PHP engine does, with isHealthy false.
func (e *MeilisearchEngine) GetEngineInfo(ctx context.Context) map[string]any {
	info := map[string]any{"type": "meilisearch"}
	version, err := DoJSON(ctx, e.client, http.MethodGet, e.base+"/version", e.headers(), nil)
	if err != nil {
		info["error"] = err.Error()
		info["isHealthy"] = false
		return info
	}
	stats, err := DoJSON(ctx, e.client, http.MethodGet, e.base+"/stats", e.headers(), nil)
	if err != nil {
		info["error"] = err.Error()
		info["isHealthy"] = false
		return info
	}
	info["version"] = anyStr(version["pkgVersion"], "unknown")
	info["databaseSize"] = anyFloat(stats["databaseSize"], 0)
	info["lastUpdate"] = stats["lastUpdate"]
	if indexes, ok := stats["indexes"].(map[string]any); ok {
		info["indexes"] = indexes
	} else {
		info["indexes"] = map[string]any{}
	}
	health, err := DoJSON(ctx, e.client, http.MethodGet, e.base+"/health", e.headers(), nil)
	info["isHealthy"] = err == nil && anyStr(health["status"], "") == "available"
	info["supportsVectors"] = meiliVersionSupports(anyStr(version["pkgVersion"], ""))
	return info
}

// meiliSupportsVectors reports whether the server understands embedders
// (Meilisearch v1.3+), mirroring the PHP engine's version_compare check.
func (e *MeilisearchEngine) meiliSupportsVectors(ctx context.Context) (bool, error) {
	raw, err := DoJSON(ctx, e.client, http.MethodGet, e.base+"/version", e.headers(), nil)
	if err != nil {
		return false, err
	}
	return meiliVersionSupports(anyStr(raw["pkgVersion"], "")), nil
}

// meiliVersionSupports is the v1.3+ embedder check over a pkgVersion string.
func meiliVersionSupports(v string) bool {
	var major, minor int
	if _, err := fmt.Sscanf(v, "%d.%d", &major, &minor); err != nil {
		return false
	}
	return major > 1 || (major == 1 && minor >= 3)
}

// meiliWaitTask polls an update task to completion, mirroring the PHP engine's
// waitForTaskCompletion (30 attempts, 1s apart).
func (e *MeilisearchEngine) meiliWaitTask(ctx context.Context, taskUid any) error {
	for i := 0; i < 30; i++ {
		raw, err := DoJSON(ctx, e.client, http.MethodGet, e.base+"/tasks/"+fmt.Sprint(meiliInt(taskUid)), e.headers(), nil)
		if err != nil {
			return meiliErr("update vectors", err)
		}
		switch anyStr(raw["status"], "") {
		case "succeeded":
			return nil
		case "failed":
			// The task failure payload carries error as {"message", "code",
			// "type"}; a bare anyStr would always yield "unknown", so read the
			// message out of the map first.
			msg := "unknown"
			if em, ok := raw["error"].(map[string]any); ok {
				if s, ok := em["message"].(string); ok {
					msg = s
				}
			} else if s, ok := raw["error"].(string); ok {
				msg = s
			}
			return fmt.Errorf("scout: meilisearch update vectors: task failed: %s", msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	// ponytail: bounded wait like the PHP engine; give up after 30s.
	return fmt.Errorf("scout: meilisearch update vectors: task timeout")
}

// GetAggregations returns the facet distributions for a query.
func (e *MeilisearchEngine) GetAggregations(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	res, err := e.Search(ctx, b)
	if err != nil {
		return nil, err
	}
	return res.Aggregations, nil
}

// GetFacets returns the facet distributions for a query.
func (e *MeilisearchEngine) GetFacets(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	return e.GetAggregations(ctx, b)
}

// --- request building ---

// meiliHighlightFields prefers the highlight_fields option, mirroring the PHP
// engine's getHighlightFields; the search fields are the default.
func meiliHighlightFields(b *scout.Builder) []any {
	if hf := b.GetOptions()["highlight_fields"]; hf != nil {
		return esFieldList(hf)
	}
	fields := b.SearchFields()
	out := make([]any, len(fields))
	for i, f := range fields {
		out[i] = f
	}
	return out
}

// meiliHybrid renders the hybrid payload, mirroring the PHP engine's
// addMeilisearchVectorSearch: an options hybrid object wins, otherwise the
// embedder (default "default") plus the semantic/similarity/score overrides.
func meiliHybrid(b *scout.Builder) map[string]any {
	opts := asMap(b.GetVectorSearch()["options"])
	if h, ok := opts["hybrid"].(map[string]any); ok {
		return h
	}
	hybrid := map[string]any{"embedder": "default"}
	if e, ok := opts["embedder"].(string); ok && e != "" {
		hybrid["embedder"] = e
	}
	if v, ok := opts["semantic_ratio"]; ok {
		hybrid["semanticRatio"] = v
	}
	if v, ok := opts["similarity_threshold"]; ok {
		hybrid["similarityThreshold"] = v
	}
	if v, ok := opts["show_ranking_score"]; ok {
		hybrid["showRankingScore"] = v
	}
	return hybrid
}

// meiliAdvanced translates one structured condition. Untranslatable operators
// return "" and are dropped, matching the PHP fallback.
func meiliAdvanced(c scout.AdvancedWhere) string {
	field := c.Field
	if field == "" {
		return ""
	}
	val := c.Value
	switch c.Operator {
	case "range", "date_range":
		return meiliRange(field, val)
	case "geo_radius", "geo_distance":
		g, _ := val.(map[string]any)
		if g == nil {
			return ""
		}
		return fmt.Sprintf("_geoRadius(%s, %s, %s)", meiliLit(g["lat"]), meiliLit(g["lng"]), meiliLit(g["radius"]))
	case "geo_bounding_box":
		g, _ := val.(map[string]any)
		if g == nil {
			return ""
		}
		tl, _ := g["top_left"].(map[string]any)
		br, _ := g["bottom_right"].(map[string]any)
		return fmt.Sprintf("_geoBoundingBox([%s, %s, %s, %s])",
			meiliLit(tl["lat"]), meiliLit(tl["lng"]), meiliLit(br["lat"]), meiliLit(br["lng"]))
	case ">":
		return fmt.Sprintf("%s > %s", field, meiliLit(val))
	case ">=":
		return fmt.Sprintf("%s >= %s", field, meiliLit(val))
	case "<":
		return fmt.Sprintf("%s < %s", field, meiliLit(val))
	case "<=":
		return fmt.Sprintf("%s <= %s", field, meiliLit(val))
	case "!=":
		return fmt.Sprintf("%s != %s", field, meiliLit(val))
	case "exists":
		return field + " EXISTS"
	case "missing":
		return field + " NOT EXISTS"
	case "null":
		return field + " IS NULL"
	case "not_null":
		return field + " IS NOT NULL"
	case "empty":
		return field + " IS EMPTY"
	case "not_empty":
		return field + " IS NOT EMPTY"
	case "contains":
		return fmt.Sprintf("%s CONTAINS %s", field, meiliLit(val))
	case "starts_with":
		return fmt.Sprintf("%s STARTS WITH %s", field, meiliLit(val))
	case "ends_with":
		return fmt.Sprintf("%s ENDS WITH %s", field, meiliLit(val))
	case "regex":
		return fmt.Sprintf("%s MATCHES %s", field, meiliLit(val))
	case "in":
		return fmt.Sprintf("%s IN [%s]", field, meiliList(meiliAnyList(val)))
	case "not_in":
		return fmt.Sprintf("%s NOT IN [%s]", field, meiliList(meiliAnyList(val)))
	case "fulltext":
		// ponytail: Meili filters can't express a full-text clause; the terms
		// already ride along in q. Emit an explicit filter when that is needed.
		return ""
	default: // "=", "==", "eq", "match", and anything unrecognised
		return fmt.Sprintf("%s = %s", field, meiliLit(val))
	}
}

// meiliRange renders range/date_range bounds; only gte/gt/lte/lt are honoured.
func meiliRange(field string, val any) string {
	m, _ := val.(map[string]any)
	if m == nil {
		return ""
	}
	bounds, ok := m["range"].(map[string]any)
	if !ok {
		bounds = m
	}
	op := map[string]string{"gte": ">=", "gt": ">", "lte": "<=", "lt": "<"}
	var parts []string
	for _, k := range []string{"gte", "gt", "lte", "lt"} {
		if v, ok := bounds[k]; ok {
			parts = append(parts, fmt.Sprintf("%s %s %s", field, op[k], meiliLit(v)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " AND ")
}

// meiliSorts renders orders plus advanced sorts as Meilisearch sort entries.
func meiliSorts(b *scout.Builder) []string {
	var out []string
	for _, o := range b.GetOrders() {
		out = append(out, meiliDir(o.Column, o.Direction))
	}
	for _, s := range b.GetSorts() {
		switch s.Type {
		case "", "field":
			continue // already covered by GetOrders
		case "vector_similarity":
			// ponytail: Meili has no _vector_distance sort; the vector already
			// drives ranking via hybrid. Skip unless per-field distance sorts are needed.
		case "geo_distance":
			lat, lng := 0.0, 0.0
			if s.Location != nil {
				lat, lng = s.Location["lat"], s.Location["lng"]
			}
			out = append(out, fmt.Sprintf("_geoPoint(%s, %s):%s", meiliLit(lat), meiliLit(lng), meiliD(s.Direction)))
		case "random":
			out = append(out, "_random:asc")
		default:
			if s.Field != "" {
				out = append(out, meiliDir(s.Field, s.Direction))
			}
		}
	}
	return out
}

// meiliFacetFields unions aggregation and facet names for facetsBy.
func meiliFacetFields(b *scout.Builder) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for name := range b.GetAggregationConfig() {
		add(name)
	}
	for name := range b.GetFacetConfig() {
		add(name)
	}
	sort.Strings(out)
	return out
}

// meiliVector returns the query vector, or nil when none is configured.
func meiliVector(b *scout.Builder) []float64 {
	vs := b.GetVectorSearch()
	if vs == nil {
		return nil
	}
	if v, ok := vs["vector"].([]float64); ok && len(v) > 0 {
		return v
	}
	return nil
}

// --- response parsing ---

// parse converts a raw Meilisearch search response into a scout.Result. The
// model's key column is read from the document body (default "id").
func (e *MeilisearchEngine) parse(raw map[string]any, b *scout.Builder) *scout.Result {
	key := scout.KeyNameOf(b.Model)
	var hits []map[string]any
	for _, h := range meiliAnyList(raw["hits"]) {
		doc, ok := h.(map[string]any)
		if !ok {
			continue
		}
		// Advanced parity: PHP's processMeilisearchResults folds _formatted,
		// _vectorDistance and _matchesPosition back into the document.
		if f, ok := doc["_formatted"]; ok {
			doc["_highlight"] = f
		}
		if vd, ok := doc["_vectorDistance"]; ok {
			doc["_vector_distance"] = vd
		}
		if mp, ok := doc["_matchesPosition"]; ok {
			doc["_matches_position"] = mp
		}
		hits = append(hits, map[string]any{
			"_id":           doc[key],
			"_score":        meiliNum(doc, "_rankingScore"),
			"_source":       doc,
			"highlight":     doc["_formatted"],
			"_vector_score": meiliNum(doc, "_vectorDistance"),
		})
	}
	res := Result(hits, meiliTotal(raw))
	if m, ok := raw["facetDistribution"].(map[string]any); ok {
		res.Aggregations = m
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
