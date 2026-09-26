package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/erikwang2013/go-scout"
)

// --- shared stubs (reused by typesense_test.go) ---

type stubModel struct {
	ID    int64
	Title string
}

func (m *stubModel) ScoutKey() any     { return m.ID }
func (m *stubModel) TableName() string { return "products" }
func (m *stubModel) ToSearchableArray() map[string]any {
	return map[string]any{"title": m.Title, "price": 10.0}
}
func (m *stubModel) ShouldBeSearchable() bool { return true }

type stubSource map[any]*stubModel

func (s stubSource) All(ctx any) ([]scout.ScoutModel, error) { return nil, nil }
func (s stubSource) ByIDs(ctx any, ids []any) ([]scout.ScoutModel, error) {
	out := make([]scout.ScoutModel, 0, len(ids))
	for _, id := range ids {
		// Engine responses round-trip JSON, so integer ids arrive as float64.
		if f, ok := id.(float64); ok {
			id = int64(f)
		}
		if m, ok := s[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}
func (s stubSource) Count(ctx any) (int, error) { return 0, nil }

// mtsCapture records the last request and replies with a fixed JSON body.
type mtsCap struct {
	method string
	path   string
	header http.Header
	raw    []byte
	calls  []string
}

func (c *mtsCap) JSON(t *testing.T) map[string]any {
	t.Helper()
	if len(c.raw) == 0 {
		return nil
	}
	m := map[string]any{}
	if err := json.Unmarshal(c.raw, &m); err != nil {
		t.Fatalf("bad request body %q: %v", c.raw, err)
	}
	return m
}

func (c *mtsCap) List(t *testing.T) []any {
	t.Helper()
	var out []any
	if err := json.Unmarshal(c.raw, &out); err != nil {
		t.Fatalf("bad request body %q: %v", c.raw, err)
	}
	return out
}

func mtsCapture(t *testing.T, respBody string) (*httptest.Server, *mtsCap) {
	t.Helper()
	c := &mtsCap{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.method = r.Method
		c.path = r.URL.RequestURI()
		c.calls = append(c.calls, r.Method+" "+r.URL.RequestURI())
		c.header = r.Header.Clone()
		buf := newRecorder(r)
		c.raw = buf
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func newRecorder(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	buf, _ := io.ReadAll(r.Body)
	return buf
}

func newBuilder(t *testing.T, source scout.Source[scout.ScoutModel]) *scout.Builder {
	t.Helper()
	return scout.NewBuilder(&stubModel{ID: 1}, source, nil, scout.DefaultConfig(), "widget", nil, false)
}

// --- Meilisearch ---

func TestMeilisearchWriteOps(t *testing.T) {
	models := []scout.ScoutModel{&stubModel{ID: 1, Title: "a"}, &stubModel{ID: 2, Title: "b"}}
	ctx := context.Background()
	e := NewMeilisearch(scout.DefaultConfig())

	t.Run("Update", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"taskUid":7,"status":"enqueued"}`)
		e.base, e.key, e.client = srv.URL, "sekrit", srv.Client()
		if err := e.Update(ctx, models); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodPut || rec.path != "/indexes/products/documents" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
		if got := rec.header.Get("X-Meili-API-Key"); got != "sekrit" {
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
		srv, rec := mtsCapture(t, `{"taskUid":8,"status":"enqueued"}`)
		e.base, e.key, e.client = srv.URL, "sekrit", srv.Client()
		if err := e.Delete(ctx, models); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodPost || rec.path != "/indexes/products/documents/delete-bys" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
		if got := rec.JSON(t)["filter"]; got != "id = 1 OR id = 2" {
			t.Errorf("filter = %v", got)
		}
	})

	t.Run("Flush", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"taskUid":9,"status":"enqueued"}`)
		e.base, e.client = srv.URL, srv.Client()
		if err := e.Flush(ctx, models[0]); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodDelete || rec.path != "/indexes/products" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
	})

	t.Run("CreateIndex", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"uid":"products","taskUid":10}`)
		e.base, e.client = srv.URL, srv.Client()
		if _, err := e.CreateIndex(ctx, "products", map[string]any{"primaryKey": "id"}); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodPost || rec.path != "/indexes" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
		body := rec.JSON(t)
		if body["uid"] != "products" {
			t.Errorf("uid = %v", body["uid"])
		}
		if pk, _ := body["settings"].(map[string]any)["primaryKey"].(string); pk != "id" {
			t.Errorf("settings = %v", body["settings"])
		}
	})

	t.Run("DeleteIndex", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"taskUid":11,"status":"enqueued"}`)
		e.base, e.client = srv.URL, srv.Client()
		if _, err := e.DeleteIndex(ctx, "products"); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodDelete || rec.path != "/indexes/products" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
	})
}

func TestMeilisearchSearchParams(t *testing.T) {
	srv, rec := mtsCapture(t, `{"hits":[]}`)
	e := NewMeilisearch(scout.DefaultConfig())
	e.base, e.key, e.client = srv.URL, "sekrit", srv.Client()

	b := newBuilder(t, nil)
	b.Where("category", "shoes")
	b.Where("active", true)
	b.WhereIn("status", []any{"a", "b"})
	b.WhereNotIn("hidden", []any{1})
	b.WhereAdvanced("name", "starts_with", "foo", "", false)
	b.WhereRange("price", map[string]any{"gte": 10, "lte": 50}, true)
	b.WhereGeoDistance("location", 1, 2, 3)
	b.OrderByDesc("price")
	b.Facet("category", nil)
	b.Aggregate("c", "avg", "price", nil)
	b.VectorSearch([]float64{0.1, 0.2}, "embedding", nil)

	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Total != 0 {
		t.Fatalf("res = %v", res)
	}

	body := rec.JSON(t)
	if rec.method != http.MethodPost || rec.path != "/indexes/products/search" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	want := `active = true AND category = "shoes" AND status IN ["a", "b"] AND hidden NOT IN [1] AND name STARTS WITH "foo" AND price >= 10 AND price <= 50 AND _geoRadius(1, 2, 3)`
	if got := body["filter"]; got != want {
		t.Errorf("filter:\n got %v\nwant %v", got, want)
	}
	if !reflect.DeepEqual(body["sort"], []any{"price:desc"}) {
		t.Errorf("sort = %v", body["sort"])
	}
	if !reflect.DeepEqual(body["facetsBy"], []any{"c", "category"}) {
		t.Errorf("facetsBy = %v", body["facetsBy"])
	}
	if !reflect.DeepEqual(body["vector"], []any{0.1, 0.2}) {
		t.Errorf("vector = %v", body["vector"])
	}
	if _, ok := body["hybrid"].(map[string]any); !ok {
		t.Errorf("hybrid = %v", body["hybrid"])
	}
	if body["limit"] != 20.0 || body["offset"] != 0.0 {
		t.Errorf("limit/offset = %v/%v", body["limit"], body["offset"])
	}
	if got := body["attributesToRetrieve"]; !reflect.DeepEqual(got, []any{"id"}) {
		t.Errorf("attributesToRetrieve = %v", got)
	}
	if hl, ok := body["attributesToHighlight"].([]any); !ok || len(hl) != 2 {
		t.Errorf("attributesToHighlight = %v", body["attributesToHighlight"])
	}
	if body["q"] != "widget" {
		t.Errorf("q = %v", body["q"])
	}
}

func TestMeilisearchPaginate(t *testing.T) {
	srv, rec := mtsCapture(t, `{"hits":[],"totalHits":37}`)
	e := NewMeilisearch(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()

	b := newBuilder(t, nil)
	res, err := e.Paginate(context.Background(), b, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.JSON(t)
	if body["limit"] != 10.0 || body["offset"] != 20.0 {
		t.Errorf("limit/offset = %v/%v", body["limit"], body["offset"])
	}
	if res.Total != 37 {
		t.Errorf("total = %d", res.Total)
	}
	if e.GetTotalCount(res) != 37 {
		t.Errorf("GetTotalCount = %d", e.GetTotalCount(res))
	}
}

func TestMeilisearchParse(t *testing.T) {
	resp := `{
		"query": "widget",
		"hits": [
			{"id": 2, "title": "B", "_rankingScore": 0.8, "_formatted": {"title": "<mark>B</mark>"}, "_vectorDistance": 0.11},
			{"id": 1, "title": "A", "_rankingScore": 0.5}
		],
		"estimatedTotalHits": 42,
		"processingTimeMs": 3,
		"facetDistribution": {"category": {"shoes": 5}}
	}`
	srv, _ := mtsCapture(t, resp)
	e := NewMeilisearch(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()

	res, err := e.Search(context.Background(), newBuilder(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 42 {
		t.Errorf("total = %d", res.Total)
	}
	if res.Took != 3 {
		t.Errorf("took = %d", res.Took)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("hits = %v", res.Hits)
	}
	h := res.Hits[0]
	if h.ID != 2.0 || h.Score != 0.8 {
		t.Errorf("hit = %+v", h)
	}
	if h.Source["title"] != "B" {
		t.Errorf("source = %v", h.Source)
	}
	if h.Highlight["title"] != "<mark>B</mark>" {
		t.Errorf("highlight = %v", h.Highlight)
	}
	if h.VectorScore == nil || *h.VectorScore != 0.11 {
		t.Errorf("vectorScore = %v", h.VectorScore)
	}
	if res.Hits[1].Score != 0.5 {
		t.Errorf("hit 2 score = %v", res.Hits[1].Score)
	}
	cats, ok := res.Aggregations["category"].(map[string]any)
	if !ok || cats["shoes"] != 5.0 {
		t.Errorf("aggregations = %v", res.Aggregations)
	}
	if got := e.MapIDs(res); !reflect.DeepEqual(got, []any{float64(2), float64(1)}) {
		t.Errorf("MapIDs = %v", got)
	}

	// totalHits fallback and empty facet result
	srv2, _ := mtsCapture(t, `{"hits":[],"totalHits":9}`)
	e2 := NewMeilisearch(scout.DefaultConfig())
	e2.base, e2.client = srv2.URL, srv2.Client()
	res2, err := e2.Search(context.Background(), newBuilder(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if res2.Total != 9 || res2.Aggregations != nil {
		t.Errorf("total=%d aggregations=%v", res2.Total, res2.Aggregations)
	}

	// AdvancedSearch delegates to Search
	res3, err := e2.AdvancedSearch(context.Background(), newBuilder(t, nil))
	if err != nil || res3.Total != 9 {
		t.Errorf("advanced = %v", res3)
	}

	facets, err := e2.GetFacets(context.Background(), newBuilder(t, nil))
	if err != nil || facets != nil {
		t.Errorf("facets = %v", facets)
	}
}

func TestMeilisearchMap(t *testing.T) {
	srv, _ := mtsCapture(t, `{"hits":[{"id":2},{"id":1}],"totalHits":2}`)
	e := NewMeilisearch(scout.DefaultConfig())
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
