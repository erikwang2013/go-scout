package engines

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/erikwang2013/go-scout"
)

// This file holds the advanced surface the PHP plugin's AdvancedTypesenseEngine
// ships: the search-param translation (highlight, projection, vector, geo,
// facets, aggregates), the aggregation/facet queries and the advanced result
// processing.

// GetAggregations runs the query and returns facet_counts plus groups.
func (e *TypesenseEngine) GetAggregations(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	res, err := e.Search(ctx, b)
	if err != nil {
		return nil, err
	}
	return res.Aggregations, nil
}

// GetFacets runs the query and returns facet_counts plus groups.
func (e *TypesenseEngine) GetFacets(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	return e.GetAggregations(ctx, b)
}

// tsOptionString returns the first present option as a comma-joined string.
func tsOptionString(b *scout.Builder, keys ...string) string {
	for _, k := range keys {
		if v, ok := b.GetOptions()[k]; ok && v != nil {
			if a, ok := v.([]any); ok {
				parts := make([]string, len(a))
				for i, p := range a {
					parts[i] = fmt.Sprint(p)
				}
				return strings.Join(parts, ",")
			}
			return fmt.Sprint(v)
		}
	}
	return ""
}

// tsGeoParams extracts one geo condition into the location_* params the PHP
// engine's extractTypesenseGeoSearch produces. Go's geo_distance operator maps
// to PHP's geo_radius; the filter clause is still emitted by tsAdvanced.
func tsGeoParams(c scout.AdvancedWhere) map[string]any {
	if c.Operator != "geo_radius" && c.Operator != "geo_distance" &&
		c.Operator != "geo_bounding_box" && c.Operator != "geo_polygon" {
		return nil
	}
	g, _ := c.Value.(map[string]any)
	if g == nil {
		return nil
	}
	switch c.Operator {
	case "geo_radius", "geo_distance":
		return map[string]any{
			"location_field": c.Field,
			"location_value": fmt.Sprintf("%s,%s,%skm", tsLit(g["lat"]), tsLit(g["lng"]), tsLit(g["radius"])),
		}
	case "geo_bounding_box":
		tl, _ := g["top_left"].(map[string]any)
		br, _ := g["bottom_right"].(map[string]any)
		return map[string]any{
			"location_field":        c.Field,
			"location_bounding_box": fmt.Sprintf("[%s,%s],[%s,%s]", tsLit(tl["lat"]), tsLit(tl["lng"]), tsLit(br["lat"]), tsLit(br["lng"])),
		}
	case "geo_polygon":
		var pts []string
		for _, p := range tsAnyList(g["points"]) {
			pm, _ := p.(map[string]any)
			pts = append(pts, fmt.Sprintf("[%s,%s]", tsLit(pm["lat"]), tsLit(pm["lng"])))
		}
		if len(pts) == 0 {
			return nil
		}
		return map[string]any{
			"location_field":   c.Field,
			"location_polygon": strings.Join(pts, ","),
		}
	}
	return nil
}

// tsVectorSearch writes the vector_query params, mirroring the PHP engine's
// addTypesenseVectorSearch: "field:[v1,v2]" plus k and the optional extras.
func tsVectorSearch(b *scout.Builder, body map[string]any) {
	vs := b.GetVectorSearch()
	if vs == nil {
		return
	}
	vector, ok := vs["vector"].([]float64)
	if !ok || len(vector) == 0 {
		return
	}
	field, _ := vs["field"].(string)
	if field == "" {
		field = "embedding"
	}
	opts := asMap(vs["options"])
	nums := make([]string, len(vector))
	for i, v := range vector {
		nums[i] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	body["vector_query"] = field + ":[" + strings.Join(nums, ",") + "]"
	body["k"] = anyFloat(opts["top_k"], 10)
	if v, ok := opts["distance_threshold"]; ok {
		body["distance_threshold"] = v
	}
	if v, ok := opts["metric"]; ok {
		body["vector_distance_metric"] = v
	}
	if v, ok := opts["include_vector_distance"]; ok {
		body["include_vector_distance"] = v
	}
	if v, ok := opts["include_vector"]; ok {
		body["include_vector"] = v
	}
}

// tsAdvanced translates one structured condition. Untranslatable operators are
// dropped, matching the PHP fallback.
func tsAdvanced(c scout.AdvancedWhere) string {
	field := c.Field
	if field == "" {
		return ""
	}
	val := c.Value
	switch c.Operator {
	case "range", "date_range":
		return tsRange(field, val)
	case "geo_radius", "geo_distance":
		g, _ := val.(map[string]any)
		if g == nil {
			return ""
		}
		return fmt.Sprintf("%s:(%s, %s, %s km)", field, tsLit(g["lat"]), tsLit(g["lng"]), tsLit(g["radius"]))
	case "geo_bounding_box":
		g, _ := val.(map[string]any)
		if g == nil {
			return ""
		}
		tl, _ := g["top_left"].(map[string]any)
		br, _ := g["bottom_right"].(map[string]any)
		return fmt.Sprintf("%s:([%s, %s], [%s, %s])", field,
			tsLit(tl["lat"]), tsLit(tl["lng"]), tsLit(br["lat"]), tsLit(br["lng"]))
	case "geo_polygon":
		g, _ := val.(map[string]any)
		if g == nil {
			return ""
		}
		var pts []string
		for _, p := range tsAnyList(g["points"]) {
			pm, _ := p.(map[string]any)
			pts = append(pts, fmt.Sprintf("[%s,%s]", tsLit(pm["lat"]), tsLit(pm["lng"])))
		}
		if len(pts) == 0 {
			return ""
		}
		return fmt.Sprintf("%s:[%s]", field, strings.Join(pts, ", "))
	case ">":
		return fmt.Sprintf("%s:>%s", field, tsLit(val))
	case ">=":
		return fmt.Sprintf("%s:>=%s", field, tsLit(val))
	case "<":
		return fmt.Sprintf("%s:<%s", field, tsLit(val))
	case "<=":
		return fmt.Sprintf("%s:<=%s", field, tsLit(val))
	case "!=":
		return fmt.Sprintf("%s:!=%s", field, tsLit(val))
	case "exists":
		return field + ":*"
	case "missing":
		return "!" + field + ":*"
	case "contains":
		// PHP parity: prefix/suffix matches use the raw value, unquoted.
		return fmt.Sprintf("%s:*%s*", field, fmt.Sprint(val))
	case "starts_with":
		return fmt.Sprintf("%s:%s*", field, fmt.Sprint(val))
	case "ends_with":
		return fmt.Sprintf("%s:*%s", field, fmt.Sprint(val))
	case "regex":
		pat := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `/`, `\/`).Replace(fmt.Sprint(val))
		return fmt.Sprintf("%s:/%s/", field, pat)
	case "in":
		return fmt.Sprintf("%s:[%s]", field, tsList(tsAnyList(val)))
	case "not_in":
		return fmt.Sprintf("%s:!=[%s]", field, tsList(tsAnyList(val)))
	case "null":
		return field + ":= null"
	case "not_null":
		return field + ":!= null"
	case "empty":
		return field + `:= ""`
	case "not_empty":
		return field + `:!= ""`
	case "fulltext":
		// ponytail: Typesense filter_by can't express a full-text clause; the
		// terms already ride along in q. Emit a filter when that is needed.
		return ""
	default: // "=", "==", "eq", "match", and anything unrecognised
		return fmt.Sprintf("%s:=%s", field, tsLit(val))
	}
}

// tsRange renders range/date_range bounds; only gte/gt/lte/lt are honoured.
func tsRange(field string, val any) string {
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
			parts = append(parts, fmt.Sprintf("%s:%s%s", field, op[k], tsLit(v)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " && ")
}

// tsSorts renders orders plus advanced sorts as a Typesense sort_by string.
func tsSorts(b *scout.Builder) string {
	var out []string
	for _, o := range b.GetOrders() {
		out = append(out, tsDir(o.Column, o.Direction))
	}
	for _, s := range b.GetSorts() {
		switch s.Type {
		case "", "field":
			continue // already covered by GetOrders
		case "vector_similarity":
			out = append(out, "vector_distance:desc")
		case "geo_distance":
			lat, lng := 0.0, 0.0
			if s.Location != nil {
				lat, lng = s.Location["lat"], s.Location["lng"]
			}
			out = append(out, fmt.Sprintf("location(%s,%s):asc", tsNum(lat), tsNum(lng)))
		case "random":
			// ponytail: Typesense has no random sort; ignore the clause.
		default:
			if s.Field != "" {
				out = append(out, tsDir(s.Field, s.Direction))
			}
		}
	}
	return strings.Join(out, ",")
}

// tsFacetBy unions options["facet_by"] with the facet config keys.
func tsFacetBy(b *scout.Builder) string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range tsStrings(b.GetOptions()["facet_by"]) {
		add(s)
	}
	for name := range b.GetFacetConfig() {
		add(name)
	}
	return strings.Join(out, ",")
}

// tsAggregates renders aggregations into Typesense's aggregate syntax.
func tsAggregates(b *scout.Builder) string {
	var out []string
	for _, name := range tsKeys(b.GetAggregationConfig()) {
		a := b.GetAggregationConfig()[name]
		if a.Field == "" {
			continue
		}
		switch a.Type {
		case "avg":
			out = append(out, a.Field+":avg()")
		case "min":
			out = append(out, a.Field+":min()")
		case "max":
			out = append(out, a.Field+":max()")
		case "count", "value_count":
			out = append(out, a.Field+":count()")
		case "count_distinct":
			out = append(out, a.Field+":count_distinct()")
		case "count_null":
			out = append(out, a.Field+":count_null()")
		case "count_empty":
			out = append(out, a.Field+":count_empty()")
		case "histogram":
			lo, hi, inc := 0.0, 0.0, 1.0
			if a.Options != nil {
				lo = tsFloatOr(a.Options["min"], lo)
				hi = tsFloatOr(a.Options["max"], hi)
				inc = tsFloatOr(a.Options["increment"], inc)
				if inc == 0 {
					inc = tsFloatOr(a.Options["interval"], 1.0)
				}
			}
			out = append(out, fmt.Sprintf("%s:histogram(%s,%s,%s)", a.Field, tsNum(lo), tsNum(hi), tsNum(inc)))
		default:
			// ponytail: terms/range/date_histogram have no aggregate equivalent;
			// facet_by already yields the distribution. Add when exact buckets matter.
		}
	}
	return strings.Join(out, ",")
}

// --- response parsing ---

// parse converts a raw Typesense search response into a scout.Result. When
// grouped_hits is present, the first hit of each group is used, as the PHP
// engine's map() does.
func (e *TypesenseEngine) parse(raw map[string]any, b *scout.Builder) *scout.Result {
	var hits []map[string]any
	var groups []any
	for _, g := range tsAnyList(raw["grouped_hits"]) {
		gm, _ := g.(map[string]any)
		hs := tsAnyList(gm["hits"])
		if len(hs) == 0 {
			continue
		}
		hm, _ := hs[0].(map[string]any)
		doc, _ := hm["document"].(map[string]any)
		if doc == nil {
			doc = map[string]any{}
		}
		hits = append(hits, map[string]any{
			"_id":     doc["id"],
			"_score":  tsFloat(hm["text_match"]),
			"_source": doc,
		})
		groups = append(groups, g)
	}
	if len(hits) == 0 {
		for _, h := range tsAnyList(raw["hits"]) {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			doc, _ := hm["document"].(map[string]any)
			if doc == nil {
				doc = map[string]any{}
			}
			id := doc["id"]
			if id == nil {
				id = hm["id"]
			}
			hits = append(hits, map[string]any{
				"_id":           id,
				"_score":        tsFloat(hm["text_match"]),
				"_source":       doc,
				"highlight":     hm["highlights"],
				"_vector_score": tsFloatOrNil(hm["vector_distance"]),
			})
		}
	}
	total := tsInt(raw["found"])
	if total == 0 {
		total = tsInt(raw["total_hits"])
	}
	res := Result(hits, total)
	extra := map[string]any{}
	// The real API returns facet_counts as an array of {field_name, counts};
	// normalize it into a map keyed by field_name, the shape the PHP plugin
	// exposes. The key is kept even for an empty array.
	if fc, ok := raw["facet_counts"]; ok {
		extra["facet_counts"] = tsFacetCounts(fc)
	}
	if groups != nil {
		extra["groups"] = groups
	}
	if len(extra) > 0 {
		res.Aggregations = extra
	}
	if s, ok := raw["search_time_ms"].(float64); ok {
		res.Took = int(s)
	}
	res.Raw = raw
	for _, p := range b.GetResultProcessors() {
		res = p(res)
	}
	return res
}

// tsFacetCounts normalizes a facet_counts array [{"field_name": ..., "counts":
// [...]}] into a map keyed by field_name; unknown shapes yield an empty map.
func tsFacetCounts(v any) map[string]any {
	out := map[string]any{}
	for _, f := range tsAnyList(v) {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := fm["field_name"].(string); name != "" {
			out[name] = fm
		}
	}
	return out
}
