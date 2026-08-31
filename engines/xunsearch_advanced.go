package engines

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/erikwang2013/go-scout"
)

// This file holds the advanced surface the PHP plugin's AdvancedXunSearchEngine
// ships: the query-expression builder (buildAdvancedQuery), facets and
// aggregations. The search daemon answers one protocol for both engines.

// GetFacets returns the facet counts for the query, mirroring the PHP engine's
// buildFacets: one search with facet params, per-field counts passed through
// from the daemon.
func (e *XunSearchEngine) GetFacets(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	res, err := e.run(ctx, b, b.GetLimit(), b.GetOffset())
	if err != nil {
		return nil, err
	}
	facets, _ := res.Aggregations["facets"].(map[string]any)
	if facets == nil {
		facets = map[string]any{}
	}
	return facets, nil
}

// GetAggregations computes the configured aggregations, mirroring the PHP
// engine's buildAggregations: terms and stats read facet counts from one
// search, ranges issue per-range count queries.
func (e *XunSearchEngine) GetAggregations(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	aggs := b.GetAggregationConfig()
	aggResults := map[string]any{}
	if len(aggs) == 0 {
		return aggResults, nil
	}
	var facetFields []string
	maxSize := 10
	var ranges []string
	for _, name := range tsKeys(aggs) {
		a := aggs[name]
		switch a.Type {
		case "terms":
			size := xsInt(a.Options["size"])
			if size <= 0 {
				size = 10
			}
			facetFields = append(facetFields, a.Field)
			if size > maxSize {
				maxSize = size
			}
		case "stats":
			facetFields = append(facetFields, a.Field)
			if 1000 > maxSize {
				maxSize = 1000
			}
		case "range":
			ranges = append(ranges, name)
		default:
			// ponytail: unsupported types come back empty, as PHP logs and skips.
			aggResults[name] = []any{}
		}
	}
	if len(facetFields) > 0 {
		// The options ride through run() as query params.
		b.SetOption("facet", strings.Join(facetFields, ","))
		b.SetOption("facet_size", fmt.Sprint(maxSize))
	}
	res, err := e.run(ctx, b, b.GetLimit(), b.GetOffset())
	if err != nil {
		return nil, err
	}
	facets := map[string]any{}
	if raw, ok := res.Raw.(map[string]any); ok {
		facets = asMap(raw["facets"])
	}
	for _, name := range tsKeys(aggs) {
		a := aggs[name]
		switch a.Type {
		case "terms":
			aggResults[name] = asMap(facets[a.Field])
		case "stats":
			aggResults[name] = xsStats(asMap(facets[a.Field]))
		case "range":
			aggResults[name] = e.xsRangeAggs(ctx, b.GetIndex(), a.Field, xsAnyList(a.Options["ranges"]))
		}
	}
	return aggResults, nil
}

// xsStats computes count/min/max/avg/sum over the facet term counts, exactly
// as the PHP engine's calculateStats does via getTerms (a quirk: the stats are
// over the counts, not the raw field values).
func xsStats(counts map[string]any) map[string]any {
	vals := make([]float64, 0, len(counts))
	for _, v := range counts {
		vals = append(vals, xsNum(v))
	}
	if len(vals) == 0 {
		return map[string]any{"count": 0, "min": 0, "max": 0, "avg": 0, "sum": 0}
	}
	sum, min, max := 0.0, vals[0], vals[0]
	for _, v := range vals {
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return map[string]any{
		"count": len(vals), "min": min, "max": max,
		"avg": sum / float64(len(vals)), "sum": sum,
	}
}

// xsRangeAggs counts each range with its own query, mirroring the PHP engine's
// calculateRanges: only ranges with both bounds are counted, and the count
// ignores the builder filters, as PHP's count() does.
func (e *XunSearchEngine) xsRangeAggs(ctx context.Context, project, field string, ranges []any) map[string]any {
	out := map[string]any{}
	for _, r := range ranges {
		rm, _ := r.(map[string]any)
		from, to := rm["from"], rm["to"]
		if from == nil || to == nil {
			continue
		}
		key := anyStr(rm["key"], "")
		if key == "" {
			key = fmt.Sprintf("%v-%v", from, to)
		}
		expr := fmt.Sprintf("%s:[%s TO %s]", field, xsRangeEscape(from), xsRangeEscape(to))
		n, err := e.xsCount(ctx, project, expr)
		if err != nil {
			// ponytail: PHP catches per-range failures and counts 0.
			n = 0
		}
		out[key] = n
	}
	return out
}

// xsCount runs one count-only query against the search daemon, mirroring
// XSSearch::count for the range aggregations.
func (e *XunSearchEngine) xsCount(ctx context.Context, project, query string) (int, error) {
	q := url.Values{
		"q":        {query},
		"project":  {project},
		"charset":  {e.charset},
		"per_page": {"1"},
		"start":    {"0"},
	}
	raw, err := DoJSON(ctx, e.client, http.MethodGet, e.searchBase+"/search?"+q.Encode(), nil, nil)
	if err != nil {
		return 0, err
	}
	n := xsInt(raw["total"])
	if n == 0 {
		n = xsInt(raw["count"])
	}
	return n, nil
}

// xsFacetParams returns the facet/facet_size query params from the facet
// config; the size is the largest configured limit (the wire takes one).
func xsFacetParams(b *scout.Builder) (string, string) {
	if len(b.GetFacetConfig()) == 0 {
		return "", ""
	}
	fields := make([]string, 0, len(b.GetFacetConfig()))
	size := 10
	for name, opts := range b.GetFacetConfig() {
		fields = append(fields, name)
		if v, ok := opts["size"]; ok {
			if n := xsInt(v); n > size {
				size = n
			}
		}
	}
	sort.Strings(fields)
	return strings.Join(fields, ","), fmt.Sprint(size)
}

// --- query expression builder ---

// xsQuery builds the XunSearch query expression for b, mirroring the PHP
// engine's buildAdvancedQuery: the keyword query is scoped to the search
// fields unless it is already a boolean expression, and every where clause is
// folded in as an exact-match or range term. Builders without conditions keep
// the plain query.
func xsQuery(b *scout.Builder) string {
	if len(b.Wheres) == 0 && len(b.GetWhereIns()) == 0 && len(b.GetWhereNotIns()) == 0 && len(b.GetAdvancedWheres()) == 0 {
		// ponytail: the PHP engine always scopes the keyword query to the model's
		// search fields; this port keeps the bare query unless an explicit
		// "fields" option asks for scoping, preserving the existing wire shape.
		if q := b.Query; q != "" && !xsBooleanQuery(q) {
			if v := b.GetOptions()["fields"]; v != nil {
				return fmt.Sprintf(`%s:"%s"`, xsJoinFields(v), xsQuote(q))
			}
		}
		return b.Query
	}
	var parts []string
	if q := b.Query; q != "" {
		if xsBooleanQuery(q) {
			parts = append(parts, q)
		} else if fields := xsQueryFields(b); fields != "" {
			parts = append(parts, fmt.Sprintf(`%s:"%s"`, fields, xsQuote(q)))
		} else {
			parts = append(parts, q)
		}
	} else {
		parts = append(parts, "*")
	}
	for _, field := range tsKeys(b.Wheres) {
		if !xsValidField(field) {
			continue
		}
		value := b.Wheres[field]
		if list, ok := value.([]any); ok && len(list) > 0 {
			parts = append(parts, xsOrQuery(field, list, ""))
		} else {
			parts = append(parts, fmt.Sprintf(`%s:"%s"`, field, xsQuote(value)))
		}
	}
	for _, field := range tsKeys(b.GetWhereIns()) {
		if !xsValidField(field) {
			continue
		}
		if c := xsOrQuery(field, b.GetWhereIns()[field], ""); c != "" {
			parts = append(parts, c)
		}
	}
	for _, field := range tsKeys(b.GetWhereNotIns()) {
		if !xsValidField(field) {
			continue
		}
		if c := xsOrQuery(field, b.GetWhereNotIns()[field], "NOT "); c != "" {
			parts = append(parts, c)
		}
	}
	for _, c := range b.GetAdvancedWheres() {
		if s := xsCondition(c); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

// xsBooleanQuery mirrors the PHP isBooleanQuery: parenthesised queries or
// queries carrying a boolean operator pass through untouched.
var xsBooleanRe = regexp.MustCompile(`(?i)\b(?:AND|OR|NOT|NEAR|SENTENCE|PARAGRAPH)\b`)

func xsBooleanQuery(q string) bool {
	return strings.ContainsAny(q, "()") || xsBooleanRe.MatchString(q)
}

// xsQueryFields returns the fields the keyword query is scoped to, mirroring
// the PHP getQueryFields: the options "fields" entry wins, then the model's
// search fields.
func xsQueryFields(b *scout.Builder) string {
	if v := b.GetOptions()["fields"]; v != nil {
		return xsJoinFields(v)
	}
	return strings.Join(b.SearchFields(), ",")
}

// xsJoinFields normalizes a fields option into the comma-joined form the
// daemon expects.
func xsJoinFields(v any) string {
	list := esFieldList(v)
	fields := make([]string, len(list))
	for i, f := range list {
		fields[i] = fmt.Sprint(f)
	}
	return strings.Join(fields, ",")
}

// xsOrQuery renders IN/NOT IN as OR expressions, mirroring the PHP engine's
// buildOrQuery: (field:"v1" OR field:"v2") with an optional NOT prefix.
func xsOrQuery(field string, values []any, prefix string) string {
	if len(values) == 0 {
		return ""
	}
	conds := make([]string, 0, len(values))
	for _, v := range values {
		conds = append(conds, fmt.Sprintf(`%s:"%s"`, field, xsQuote(v)))
	}
	return prefix + "(" + strings.Join(conds, " OR ") + ")"
}

// xsCondition translates one structured condition, mirroring the PHP engine's
// buildConditionQuery. Unknown operators fall through to the PHP default of
// field:op"value".
func xsCondition(c scout.AdvancedWhere) string {
	if c.Field == "" || !xsValidField(c.Field) {
		return ""
	}
	field, val := c.Field, c.Value
	switch c.Operator {
	case "range", "date_range":
		return xsRange(field, val)
	case "in":
		return xsOrQuery(field, xsAnyList(val), "")
	case "not_in":
		return xsOrQuery(field, xsAnyList(val), "NOT ")
	case ">":
		return fmt.Sprintf(`%s:>"%s"`, field, xsQuote(val))
	case ">=":
		return fmt.Sprintf(`%s:>="%s"`, field, xsQuote(val))
	case "<":
		return fmt.Sprintf(`%s:<"%s"`, field, xsQuote(val))
	case "<=":
		return fmt.Sprintf(`%s:<="%s"`, field, xsQuote(val))
	case "!=":
		return fmt.Sprintf(`NOT %s:"%s"`, field, xsQuote(val))
	case "like", "contains":
		// PHP names the operator "like"; Go builders say "contains".
		return fmt.Sprintf(`%s:*"%s"*`, field, xsQuote(val))
	case "starts_with":
		return fmt.Sprintf(`%s:"%s"*`, field, xsQuote(val))
	case "ends_with":
		return fmt.Sprintf(`%s:*"%s"`, field, xsQuote(val))
	case "exists":
		return field + ":[* TO *]"
	case "missing":
		return "NOT " + field + ":[* TO *]"
	case "fuzzy":
		dist := 1
		if v, ok := c.Options["distance"]; ok {
			dist = xsInt(v)
		}
		return fmt.Sprintf(`%s:%s~%d`, field, xsQuote(val), dist)
	case "proximity":
		dist := 5
		if v, ok := c.Options["distance"]; ok {
			dist = xsInt(v)
		}
		return fmt.Sprintf(`"%s:%s"~%d`, field, xsQuote(val), dist)
	default:
		return fmt.Sprintf(`%s:%s%s`, field, c.Operator, xsQuote(val))
	}
}

// xsRange renders range/date_range bounds as XunSearch range syntax. The Go
// builder stores gte/gt/lte/lt keys; PHP's positional [min, max] array maps to
// the same output when both bounds are present.
func xsRange(field string, val any) string {
	m, _ := val.(map[string]any)
	if m == nil {
		return ""
	}
	bounds, ok := m["range"].(map[string]any)
	if !ok {
		bounds = m
	}
	min, hasMin := bounds["gte"]
	if !hasMin {
		min, hasMin = bounds["gt"]
	}
	max, hasMax := bounds["lte"]
	if !hasMax {
		max, hasMax = bounds["lt"]
	}
	switch {
	case hasMin && hasMax:
		return fmt.Sprintf("%s:[%s TO %s]", field, xsRangeEscape(min), xsRangeEscape(max))
	case hasMin:
		return fmt.Sprintf("%s:>=%s", field, xsRangeEscape(min))
	case hasMax:
		return fmt.Sprintf("%s:<=%s", field, xsRangeEscape(max))
	}
	return ""
}

var xsFieldRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// xsValidField mirrors the PHP isValidField guard against query injection.
func xsValidField(field string) bool { return xsFieldRe.MatchString(field) }

// xsStr casts a value the way PHP's (string) cast does: booleans become "1"/"0".
func xsStr(v any) string {
	if b, ok := v.(bool); ok {
		if b {
			return "1"
		}
		return "0"
	}
	return fmt.Sprint(v)
}

// xsQuote escapes " and \, mirroring PHP's addcslashes($value, '"\\').
func xsQuote(v any) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(xsStr(v))
}

// xsRangeEscape escapes range values, mirroring the PHP engine's
// escapeRangeValue: [ ] " and \ are backslash-escaped to block "TO" injection.
func xsRangeEscape(v any) string {
	return strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, `"`, `\"`).Replace(xsStr(v))
}
