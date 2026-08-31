package engines

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/erikwang2013/go-scout"
)

func TestTypesenseNewConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		e := NewTypesense(scout.DefaultConfig())
		if e.base != "http://127.0.0.1:8108" {
			t.Errorf("base = %s", e.base)
		}
		if e.key != "xyz" || e.action != "upsert" {
			t.Errorf("key=%s action=%s", e.key, e.action)
		}
		if e.MaxTotalResults() != 1000 {
			t.Errorf("maxTotal = %d", e.MaxTotalResults())
		}
	})

	t.Run("node override", func(t *testing.T) {
		t.Setenv("TYPESENSE_HOST", "search.internal")
		t.Setenv("TYPESENSE_PORT", "9000")
		t.Setenv("TYPESENSE_PROTOCOL", "https")
		t.Setenv("TYPESENSE_API_KEY", "k")
		t.Setenv("TYPESENSE_MAX_TOTAL_RESULTS", "5000")
		t.Setenv("TYPESENSE_IMPORT_ACTION", "insert")
		e := NewTypesense(scout.DefaultConfig())
		if e.base != "https://search.internal:9000" {
			t.Errorf("base = %s", e.base)
		}
		if e.key != "k" || e.action != "insert" || e.MaxTotalResults() != 5000 {
			t.Errorf("key=%s action=%s max=%d", e.key, e.action, e.MaxTotalResults())
		}
	})
}

func TestTypesenseWriteOps(t *testing.T) {
	models := []scout.ScoutModel{&stubModel{ID: 1, Title: "a"}, &stubModel{ID: 2, Title: "b"}}
	ctx := context.Background()
	e := NewTypesense(scout.DefaultConfig())

	t.Run("Update", func(t *testing.T) {
		srv, rec := mtsCapture(t, `[{"success":true,"code":201},{"success":true,"code":201}]`)
		e.base, e.key, e.client = srv.URL, "ts-key", srv.Client()
		if err := e.Update(ctx, models); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodPost || rec.path != "/collections/products/documents?action=upsert" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
		if got := rec.header.Get("X-TYPESENSE-API-KEY"); got != "ts-key" {
			t.Errorf("api key header = %q", got)
		}
		docs := rec.List(t)
		if len(docs) != 2 {
			t.Fatalf("docs = %v", docs)
		}
		first := docs[0].(map[string]any)
		if first["id"] != 1.0 || first["title"] != "a" {
			t.Errorf("doc 0 = %v", first)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"id":"2","action":"remove","document":null}`)
		e.base, e.key, e.client = srv.URL, "ts-key", srv.Client()
		if err := e.Delete(ctx, models); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodDelete || rec.path != "/collections/products/documents/2" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
		if len(rec.calls) != 2 {
			t.Errorf("calls = %v", rec.calls)
		}
		if rec.calls[0] != "DELETE /collections/products/documents/1" {
			t.Errorf("calls[0] = %v", rec.calls[0])
		}
	})

	t.Run("DeleteIgnoresMissing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"Document not found"}`)
		}))
		t.Cleanup(srv.Close)
		e.base, e.client = srv.URL, srv.Client()
		if err := e.Delete(ctx, models); err != nil {
			t.Fatalf("missing documents must not error: %v", err)
		}
	})

	t.Run("Flush", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"action":"delete","collection_name":"products","status":"completed"}`)
		e.base, e.client = srv.URL, srv.Client()
		if err := e.Flush(ctx, models[0]); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodDelete || rec.path != "/collections/products" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
	})

	t.Run("CreateIndex", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"name":"products"}`)
		e.base, e.client = srv.URL, srv.Client()
		fields := []any{map[string]any{"name": "id", "type": "string"}}
		if _, err := e.CreateIndex(ctx, "products", map[string]any{"fields": fields}); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodPost || rec.path != "/collections" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
		body := rec.JSON(t)
		if body["name"] != "products" {
			t.Errorf("name = %v", body["name"])
		}
		if !reflect.DeepEqual(body["fields"], fields) {
			t.Errorf("fields = %v", body["fields"])
		}
	})

	t.Run("CreateIndexDefaultSchema", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"name":"products"}`)
		e.base, e.client = srv.URL, srv.Client()
		if _, err := e.CreateIndex(ctx, "products", nil); err != nil {
			t.Fatal(err)
		}
		fields := rec.JSON(t)["fields"]
		if !reflect.DeepEqual(fields, []any{map[string]any{"name": "*", "type": "auto"}}) {
			t.Errorf("fields = %v", fields)
		}
	})

	t.Run("DeleteIndex", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"action":"delete"}`)
		e.base, e.client = srv.URL, srv.Client()
		if _, err := e.DeleteIndex(ctx, "products"); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodDelete || rec.path != "/collections/products" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
	})
}

func TestTypesenseSearchParams(t *testing.T) {
	srv, rec := mtsCapture(t, `{"hits":[],"found":0}`)
	e := NewTypesense(scout.DefaultConfig())
	e.base, e.key, e.client = srv.URL, "ts-key", srv.Client()

	b := newBuilder(t, nil)
	b.Where("category", "shoes")
	b.Where("active", true)
	b.WhereIn("status", []any{"a", "b"})
	b.WhereNotIn("hidden", []any{1})
	b.WhereAdvanced("name", "starts_with", "foo", "", false)
	b.WhereRange("price", map[string]any{"gte": 10, "lte": 50}, true)
	b.WhereGeoDistance("location", 1, 2, 3)
	b.OrderByDesc("price")
	b.OrderByVectorSimilarity([]float64{0.1, 0.2}, "embedding")
	b.Facet("category", nil)
	b.SetOption("facet_by", "status")
	b.Aggregate("c", "avg", "price", nil)
	b.Aggregate("h", "histogram", "price", map[string]any{"min": 0.0, "max": 100.0, "increment": 10.0})
	b.VectorSearch([]float64{0.1, 0.2}, "embedding", map[string]any{"top_k": 5})

	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Total != 0 {
		t.Fatalf("res = %v", res)
	}

	body := rec.JSON(t)
	if rec.method != http.MethodPost || rec.path != "/collections/products/documents/search" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	want := `active:=true && category:="shoes" && status:["a", "b"] && hidden:!=[1] && name:foo* && price:>=10 && price:<=50 && location:(1, 2, 3 km)`
	if got := body["filter_by"]; got != want {
		t.Errorf("filter_by:\n got %v\nwant %v", got, want)
	}
	if body["sort_by"] != "price:desc,vector_distance:desc" {
		t.Errorf("sort_by = %v", body["sort_by"])
	}
	if body["facet_by"] != "status,category" {
		t.Errorf("facet_by = %v", body["facet_by"])
	}
	if body["aggregate"] != "price:avg(),price:histogram(0,100,10)" {
		t.Errorf("aggregate = %v", body["aggregate"])
	}
	if body["per_page"] != 20.0 || body["page"] != 1.0 {
		t.Errorf("per_page/page = %v/%v", body["per_page"], body["page"])
	}
	if body["q"] != "widget" {
		t.Errorf("q = %v", body["q"])
	}
	qb, _ := body["query_by"].(string)
	if qb != "price,title" && qb != "title,price" {
		t.Errorf("query_by = %v", body["query_by"])
	}
	if body["vector_query"] != "embedding:[0.1,0.2]" {
		t.Errorf("vector_query = %v", body["vector_query"])
	}
	if body["k"] != 5.0 {
		t.Errorf("k = %v", body["k"])
	}
	if body["location_field"] != "location" || body["location_value"] != "1,2,3km" {
		t.Errorf("geo = %v / %v", body["location_field"], body["location_value"])
	}
}

func TestTypesensePaginate(t *testing.T) {
	srv, rec := mtsCapture(t, `{"hits":[],"found":37}`)
	e := NewTypesense(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()

	res, err := e.Paginate(context.Background(), newBuilder(t, nil), 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.JSON(t)
	if body["per_page"] != 5.0 || body["page"] != 2.0 {
		t.Errorf("per_page/page = %v/%v", body["per_page"], body["page"])
	}
	if res.Total != 37 || e.GetTotalCount(res) != 37 {
		t.Errorf("total = %d", res.Total)
	}
}

func TestTypesenseParse(t *testing.T) {
	resp := `{
		"q": "widget",
		"found": 3,
		"out_of": 10,
		"page": 1,
		"per_page": 10,
		"search_time_ms": 2,
		"hits": [
			{"document": {"id": 7, "title": "X"}, "text_match": 0.9, "highlights": {"title": ["<mark>X</mark>"]}, "vector_distance": 0.3},
			{"document": {"id": 4, "title": "Y"}, "text_match": 0.6}
		],
		"facet_counts": [{"field_name": "category", "counts": [{"value": "shoes", "count": 2}]}]
	}`
	srv, _ := mtsCapture(t, resp)
	e := NewTypesense(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()

	res, err := e.Search(context.Background(), newBuilder(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 || res.Took != 2 {
		t.Errorf("total=%d took=%d", res.Total, res.Took)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("hits = %v", res.Hits)
	}
	h := res.Hits[0]
	if h.ID != 7.0 || h.Score != 0.9 {
		t.Errorf("hit = %+v", h)
	}
	if h.Source["title"] != "X" {
		t.Errorf("source = %v", h.Source)
	}
	if _, ok := h.Highlight["title"]; !ok {
		t.Errorf("highlight = %v", h.Highlight)
	}
	if h.VectorScore == nil || *h.VectorScore != 0.3 {
		t.Errorf("vectorScore = %v", h.VectorScore)
	}
	if res.Hits[1].ID != 4.0 {
		t.Errorf("hit 2 = %+v", res.Hits[1])
	}
	if _, ok := res.Aggregations["facet_counts"]; !ok {
		t.Errorf("aggregations = %v", res.Aggregations)
	}
	if got := e.MapIDs(res); !reflect.DeepEqual(got, []any{float64(7), float64(4)}) {
		t.Errorf("MapIDs = %v", got)
	}

	// grouped_hits: first hit per group, groups surfaced in aggregations
	srv2, _ := mtsCapture(t, `{"found":2,"grouped_hits":[
		{"group_key":"shoes","hits":[{"document":{"id":1,"title":"A"},"text_match":0.8},{"document":{"id":2,"title":"B"},"text_match":0.7}]},
		{"group_key":"hats","hits":[{"document":{"id":3,"title":"C"},"text_match":0.5}]}
	]}`)
	e2 := NewTypesense(scout.DefaultConfig())
	e2.base, e2.client = srv2.URL, srv2.Client()
	res2, err := e2.AdvancedSearch(context.Background(), newBuilder(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := e2.MapIDs(res2); !reflect.DeepEqual(got, []any{float64(1), float64(3)}) {
		t.Errorf("grouped MapIDs = %v", got)
	}
	groups, ok := res2.Aggregations["groups"].([]any)
	if !ok || len(groups) != 2 {
		t.Errorf("groups = %v", res2.Aggregations)
	}

	// total_hits fallback and aggregations accessors
	srv3, _ := mtsCapture(t, `{"hits":[],"total_hits":12,"facet_counts":[]}`)
	e3 := NewTypesense(scout.DefaultConfig())
	e3.base, e3.client = srv3.URL, srv3.Client()
	res3, err := e3.Search(context.Background(), newBuilder(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res3.Total != 12 {
		t.Errorf("total = %d", res3.Total)
	}
	facets, err := e3.GetFacets(context.Background(), newBuilder(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := facets["facet_counts"]; !ok {
		t.Errorf("facets = %v", facets)
	}
}

func TestTypesenseAutoCreatesCollection(t *testing.T) {
	var calls []string
	var misses int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/collections/products/documents/search" && misses == 0 {
			misses++
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"The object collection could not be found"}`)
			return
		}
		fmt.Fprint(w, `{"hits":[],"found":0}`)
	}))
	t.Cleanup(srv.Close)

	e := NewTypesense(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()
	res, err := e.Search(context.Background(), newBuilder(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 {
		t.Errorf("total = %d", res.Total)
	}
	if len(calls) != 3 || calls[1] != "POST /collections" {
		t.Errorf("calls = %v", calls)
	}
}

func TestTypesenseMap(t *testing.T) {
	srv, _ := mtsCapture(t, `{"hits":[{"document":{"id":2}},{"document":{"id":1}}],"found":2}`)
	e := NewTypesense(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()

	b := newBuilder(t, stubSource{
		int64(1): &stubModel{ID: 1, Title: "A"},
		int64(2): &stubModel{ID: 2, Title: "B"},
	})
	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	models, err := e.Map(context.Background(), b, res)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %v", models)
	}
	if models[0].ScoutKey() != int64(2) || models[1].ScoutKey() != int64(1) {
		t.Errorf("order = %v %v", models[0].ScoutKey(), models[1].ScoutKey())
	}
}
