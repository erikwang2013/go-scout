package engines

// dsl.go builds the JSON query DSL shared by the OpenSearch and Elasticsearch
// engines. The structure mirrors scout's AdvancedOpenSearchEngine build*
// methods one for one.

import (
	"strconv"
	"strings"
	"time"

	"github.com/erikwang2013/go-scout"
)

// BuildQuery assembles the top-level {"bool": {...}} query for a builder.
// An entirely empty bool becomes match_all, but a bool carrying only
// must_not/should clauses is preserved (it is still a real constraint).
func BuildQuery(b *scout.Builder) map[string]any {
	boolQ := map[string]any{}
	set := func(bucket string, clause map[string]any) {
		if clause == nil {
			return
		}
		list, _ := boolQ[bucket].([]map[string]any)
		boolQ[bucket] = append(list, clause)
	}

	if b.Query != "" {
		set("must", map[string]any{"multi_match": map[string]any{
			"query":     b.Query,
			"fields":    b.SearchFields(),
			"type":      "best_fields",
			"operator":  "AND",
			"fuzziness": "AUTO",
			"boost":     anyFloat(b.GetOptions()["boost"], 1.0),
		}})
	}

	for field, value := range b.Wheres {
		v := CoerceTermValue(value)
		if v == nil {
			continue
		}
		if list, ok := v.([]any); ok {
			set("filter", map[string]any{"terms": map[string]any{field: list}})
		} else if list, ok := v.([]string); ok {
			arr := make([]any, len(list))
			for i, s := range list {
				arr[i] = s
			}
			set("filter", map[string]any{"terms": map[string]any{field: arr}})
		} else {
			set("filter", map[string]any{"term": map[string]any{field: v}})
		}
	}

	for _, cond := range b.GetAdvancedWheres() {
		set(MapBooleanToBoolKey(cond.Boolean), buildAdvancedCondition(cond))
	}

	for field, values := range b.GetWhereIns() {
		set("filter", map[string]any{"terms": map[string]any{field: values}})
	}
	for field, values := range b.GetWhereNotIns() {
		set("must_not", map[string]any{"terms": map[string]any{field: values}})
	}

	if len(boolQ) == 0 {
		return map[string]any{"match_all": map[string]any{}}
	}
	return map[string]any{"bool": boolQ}
}

// buildAdvancedCondition translates one structured condition into a DSL clause.
func buildAdvancedCondition(cond scout.AdvancedWhere) map[string]any {
	field, op := cond.Field, cond.Operator
	opts := cond.Options
	if opts == nil {
		opts = map[string]any{}
	}

	switch op {
	case "range":
		if clause, ok := buildRangeCondition(field, cond.Value, anyFloat(opts["boost"], 1.0)); ok {
			return clause
		}
		return nil
	case "date_range":
		r := asMap(asMap(cond.Value)["range"])
		return map[string]any{"range": map[string]any{field: map[string]any{
			"gte":       r["gte"],
			"lte":       r["lte"],
			"format":    anyStr(opts["format"], "yyyy-MM-dd HH:mm:ss"),
			"time_zone": anyStr(opts["time_zone"], "+08:00"),
		}}}
	case "geo_distance":
		v := asMap(cond.Value)
		radius := anyFloat(v["radius"], 10)
		return map[string]any{"geo_distance": map[string]any{
			"distance":          strconv.FormatFloat(radius, 'f', -1, 64) + anyStr(v["unit"], "km"),
			field:               map[string]any{"lat": v["lat"], "lon": v["lng"]},
			"distance_type":     anyStr(opts["distance_type"], "plane"),
			"validation_method": anyStr(opts["validation_method"], "STRICT"),
			"boost":             anyFloat(opts["boost"], 1.0),
		}}
	case "geo_bounding_box":
		v := asMap(cond.Value)
		return map[string]any{"geo_bounding_box": map[string]any{
			field:  map[string]any{"top_left": v["top_left"], "bottom_right": v["bottom_right"]},
			"type": anyStr(opts["type"], "memory"),
		}}
	case "exists":
		return map[string]any{"exists": map[string]any{"field": field}}
	case "missing":
		return map[string]any{"bool": map[string]any{
			"must_not": []map[string]any{{"exists": map[string]any{"field": field}}},
		}}
	case "wildcard":
		return map[string]any{"wildcard": map[string]any{field: map[string]any{
			"value": cond.Value, "boost": anyFloat(opts["boost"], 1.0),
		}}}
	case "regexp":
		return map[string]any{"regexp": map[string]any{field: map[string]any{
			"value": cond.Value, "flags": anyStr(opts["flags"], "ALL"), "boost": anyFloat(opts["boost"], 1.0),
		}}}
	case "prefix":
		return map[string]any{"prefix": map[string]any{field: map[string]any{
			"value": cond.Value, "boost": anyFloat(opts["boost"], 1.0),
		}}}
	case "match":
		return map[string]any{"match": map[string]any{field: map[string]any{
			"query": cond.Value, "operator": anyStr(opts["operator"], "or"), "boost": anyFloat(opts["boost"], 1.0),
		}}}
	case "match_phrase":
		return map[string]any{"match_phrase": map[string]any{field: map[string]any{
			"query": cond.Value, "slop": anyFloat(opts["slop"], 0), "boost": anyFloat(opts["boost"], 1.0),
		}}}
	case "fulltext":
		v := asMap(cond.Value)
		return map[string]any{"multi_match": map[string]any{
			"query":     v["query"],
			"fields":    v["fields"],
			"type":      "best_fields",
			"operator":  anyStr(opts["operator"], "and"),
			"fuzziness": anyStr(opts["fuzziness"], "auto"),
			"boost":     anyFloat(opts["boost"], 1.0),
		}}
	case "gt", ">":
		return map[string]any{"range": map[string]any{field: map[string]any{"gt": cond.Value}}}
	case "gte", ">=":
		return map[string]any{"range": map[string]any{field: map[string]any{"gte": cond.Value}}}
	case "lt", "<":
		return map[string]any{"range": map[string]any{field: map[string]any{"lt": cond.Value}}}
	case "lte", "<=":
		return map[string]any{"range": map[string]any{field: map[string]any{"lte": cond.Value}}}
	case "in":
		return map[string]any{"terms": map[string]any{field: esAnyList(cond.Value)}}
	case "not_in":
		return map[string]any{"bool": map[string]any{
			"must_not": []map[string]any{{"terms": map[string]any{field: esAnyList(cond.Value)}}},
		}}
	case "fuzzy":
		return map[string]any{"fuzzy": map[string]any{field: map[string]any{
			"value": cond.Value, "fuzziness": anyStr(opts["fuzziness"], "AUTO"), "boost": anyFloat(opts["boost"], 1.0),
		}}}
	case "match_phrase_prefix":
		return map[string]any{"match_phrase_prefix": map[string]any{field: map[string]any{
			"query": cond.Value, "slop": anyFloat(opts["slop"], 0),
			"max_expansions": anyFloat(opts["max_expansions"], 50), "boost": anyFloat(opts["boost"], 1.0),
		}}}
	case "multi_match":
		v := asMap(cond.Value)
		q, f := v["query"], v["fields"]
		if q == nil {
			q = cond.Value
		}
		if f == nil {
			f = []any{field}
		}
		return map[string]any{"multi_match": map[string]any{
			"query": q, "fields": f,
			"type": anyStr(opts["type"], "best_fields"), "operator": anyStr(opts["operator"], "or"),
			"fuzziness": anyStr(opts["fuzziness"], "AUTO"), "boost": anyFloat(opts["boost"], 1.0),
		}}
	case "nested":
		v := asMap(cond.Value)
		q := v["query"]
		if q == nil {
			q = cond.Value
		}
		return map[string]any{"nested": map[string]any{
			"path":       field,
			"query":      buildQueryForNested(q),
			"score_mode": anyStr(v["score_mode"], "avg"),
		}}
	case "has_child":
		v := asMap(cond.Value)
		q := v["query"]
		if q == nil {
			q = cond.Value
		}
		return map[string]any{"has_child": map[string]any{
			"type": field, "query": buildQueryForNested(q),
		}}
	case "has_parent":
		v := asMap(cond.Value)
		q := v["query"]
		if q == nil {
			q = cond.Value
		}
		return map[string]any{"has_parent": map[string]any{
			"parent_type": field, "query": buildQueryForNested(q),
		}}
	case "parent_id":
		v := asMap(cond.Value)
		id := v["id"]
		if id == nil {
			id = cond.Value
		}
		return map[string]any{"parent_id": map[string]any{"type": field, "id": id}}
	default:
		return map[string]any{"term": map[string]any{field: map[string]any{
			"value": cond.Value, "boost": anyFloat(opts["boost"], 1.0),
		}}}
	}
}

// buildQueryForNested compiles a nested-condition list into a bool query,
// mirroring the PHP engine's buildQueryForNested. Each entry carries
// field/operator/value/boolean/options and is compiled by buildAdvancedCondition.
func buildQueryForNested(v any) map[string]any {
	boolQ := map[string]any{}
	for _, c := range esAnyList(v) {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		field, _ := cm["field"].(string)
		op, _ := cm["operator"].(string)
		if field == "" || op == "" {
			continue
		}
		boolean := anyStr(cm["boolean"], "must")
		clause := buildAdvancedCondition(scout.AdvancedWhere{
			Field:    field,
			Operator: op,
			Value:    cm["value"],
			Boolean:  boolean,
			Options:  asMap(cm["options"]),
		})
		if clause == nil {
			continue
		}
		bucket := MapBooleanToBoolKey(boolean)
		list, _ := boolQ[bucket].([]map[string]any)
		boolQ[bucket] = append(list, clause)
	}
	return map[string]any{"bool": boolQ}
}

// buildRangeCondition emits a range clause with only valid bounds. Values that
// are false, "false" or nil are dropped, because OpenSearch rejects them with a
// query_shard_exception.
//
// The range payload may be either a map of gte/lte/gt/lt bounds (the form the
// Builder's WhereRange produces) or a two-element list, in which case index 0
// is the lower bound and index 1 the upper.
func buildRangeCondition(field string, value any, boost float64) (map[string]any, bool) {
	clause := map[string]any{}
	r := asMap(value)
	if r != nil {
		if inner := asMap(r["range"]); inner != nil {
			r = inner
		}
		for _, key := range []string{"gt", "gte", "lt", "lte"} {
			if n, ok := numeric(r[key]); ok {
				clause[key] = n
			}
		}
	} else if list, ok := value.([]any); ok {
		for i, key := range []string{"gte", "lte"} {
			if i < len(list) {
				if n, ok := numeric(list[i]); ok {
					clause[key] = n
				}
			}
		}
	}
	if len(clause) == 0 {
		return nil, false
	}
	if boost != 1.0 {
		clause["boost"] = boost
	}
	return map[string]any{"range": map[string]any{field: clause}}, true
}

// CoerceTermValue normalises term/terms values so no "false"/"true" strings and
// no stringified numbers reach the engine.
func CoerceTermValue(v any) any {
	switch n := v.(type) {
	case nil:
		return nil
	case []any, []string, []map[string]any:
		return n
	case bool:
		if n {
			return 1
		}
		return 0
	case string:
		switch n {
		case "false":
			return 0
		case "true":
			return 1
		}
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			if strings.Contains(n, ".") {
				return f
			}
			return int64(f)
		}
		return n
	default:
		if num, ok := numeric(v); ok {
			return num
		}
		return v
	}
}

// MapBooleanToBoolKey maps Laravel-style booleans onto OpenSearch bool keys.
func MapBooleanToBoolKey(boolean string) string {
	switch strings.ToLower(boolean) {
	case "or":
		return "should"
	case "not":
		return "must_not"
	case "and", "must", "filter":
		return "filter"
	default:
		return "filter"
	}
}

// numeric converts a JSON-decoded or Go scalar into a number, reporting whether
// the value is usable as a range bound.
func numeric(v any) (any, bool) {
	switch n := v.(type) {
	case nil, bool:
		return nil, false
	case string:
		if n == "" || n == "false" || n == "true" {
			return nil, false
		}
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			if strings.Contains(n, ".") {
				return f, true
			}
			return int64(f), true
		}
		return nil, false
	case int:
		return n, true
	case int64:
		return n, true
	case float64:
		return n, true
	case time.Time:
		return int64(n.Unix()), true
	default:
		return nil, false
	}
}

// asMap returns v as a map, or nil.
func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// anyFloat reads a float from an optional map entry.
func anyFloat(v any, def float64) float64 {
	if v == nil {
		return def
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f
		}
	}
	return def
}

// anyStr reads a string from an optional map entry.
func anyStr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

// dir normalises a sort direction onto the values OpenSearch accepts.
func dir(d string) string {
	if strings.EqualFold(strings.TrimSpace(d), "asc") {
		return "asc"
	}
	return "desc"
}

// buildSorts renders plain orders and advanced sort directives as the "sort"
// clause. Unrecognised sort types degrade to a plain field sort.
func buildSorts(b *scout.Builder) []map[string]any {
	var out []map[string]any
	for _, o := range b.GetOrders() {
		out = append(out, map[string]any{o.Column: map[string]any{"order": dir(o.Direction)}})
	}
	for _, s := range b.GetSorts() {
		switch s.Type {
		case "", "field":
			continue // OrderBy already appends to GetOrders; emitting both duplicates the column
		case "vector_similarity":
			out = append(out, map[string]any{"_score": map[string]any{"order": "desc"}})
		case "geo_distance":
			lat, lng := 0.0, 0.0
			if s.Location != nil {
				lat, lng = s.Location["lat"], s.Location["lng"]
			}
			out = append(out, map[string]any{"_geo_distance": []any{
				map[string]any{s.Field: map[string]any{"lat": lat, "lon": lng},
					"unit": anyStr(s.Options["unit"], "km")},
				dir(s.Direction),
			}})
		case "random":
			// ponytail: _score asc matches the PHP engine's stand-in for random;
			// use a random field or search_after if a real shuffle is needed.
			out = append(out, map[string]any{"_score": map[string]any{"order": "asc"}})
		default:
			out = append(out, map[string]any{s.Field: map[string]any{"order": dir(s.Direction)}})
		}
	}
	return out
}

// buildAggregations renders the builder's aggregation definitions.
func buildAggregations(b *scout.Builder) map[string]any {
	out := map[string]any{}
	for name, a := range b.GetAggregationConfig() {
		if clause := buildAggregation(a); clause != nil {
			out[name] = clause
		}
	}
	return out
}

func buildAggregation(a scout.Aggregation) map[string]any {
	o := a.Options
	if o == nil {
		o = map[string]any{}
	}
	switch a.Type {
	case "terms":
		return map[string]any{"terms": map[string]any{
			"field": a.Field, "size": anyFloat(o["size"], 10),
			"min_doc_count": anyFloat(o["min_doc_count"], 1),
		}}
	case "histogram":
		return map[string]any{"histogram": map[string]any{
			"field": a.Field, "interval": anyFloat(o["interval"], 1),
			"min_doc_count": anyFloat(o["min_doc_count"], 1),
		}}
	case "date_histogram":
		return map[string]any{"date_histogram": map[string]any{
			"field":             a.Field,
			"calendar_interval": anyStr(o["calendar_interval"], "month"),
			"time_zone":         anyStr(o["time_zone"], "+08:00"),
		}}
	case "range":
		return map[string]any{"range": map[string]any{
			"field": a.Field, "ranges": o["ranges"],
		}}
	case "date_range":
		return map[string]any{"date_range": map[string]any{
			"field": a.Field, "ranges": o["ranges"],
			"format": anyStr(o["format"], "yyyy-MM-dd HH:mm:ss"),
		}}
	case "stats", "avg", "min", "max", "sum", "cardinality", "value_count", "geo_centroid":
		return map[string]any{a.Type: map[string]any{"field": a.Field}}
	case "top_hits":
		return map[string]any{"top_hits": map[string]any{"size": anyFloat(o["size"], 10)}}
	case "nested":
		return map[string]any{"nested": map[string]any{"path": o["path"]}}
	default:
		return nil
	}
}

// buildFacets renders the builder's facet definitions as terms aggregations,
// keyed by field. The caller merges the result into the request "aggs" map.
func buildFacets(b *scout.Builder) map[string]any {
	out := map[string]any{}
	for field, o := range b.GetFacetConfig() {
		if o == nil {
			o = map[string]any{}
		}
		out[field] = map[string]any{"terms": map[string]any{
			"field": field,
			"size":  anyFloat(o["size"], anyFloat(o["count"], 10)),
		}}
	}
	return out
}

// buildHighlight renders the highlight clause from the builder options.
// A map value is used verbatim; a truthy value enables default highlighting
// over the model's search fields.
func buildHighlight(b *scout.Builder) map[string]any {
	v := b.GetOptions()["highlight"]
	switch o := v.(type) {
	case map[string]any:
		return o
	case bool:
		if !o {
			return nil
		}
	case string:
		if o == "" || o == "false" {
			return nil
		}
	default:
		return nil
	}
	fields := map[string]any{}
	for _, f := range b.SearchFields() {
		fields[f] = map[string]any{}
	}
	return map[string]any{"fields": fields}
}

// buildSuggest renders the suggester clause from the builder options.
// It is only emitted when a "suggest" option was set.
func buildSuggest(b *scout.Builder) map[string]any {
	m, ok := b.GetOptions()["suggest"].(map[string]any)
	if !ok {
		return nil
	}
	return m
}
