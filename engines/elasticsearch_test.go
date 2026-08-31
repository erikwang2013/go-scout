package engines

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/erikwang2013/go-scout"
)

func TestElasticsearchImplementsEngine(t *testing.T) {
	var _ scout.Engine = NewElasticSearch(scout.DefaultConfig())
	var _ scout.AdvancedEngine = NewElasticSearch(scout.DefaultConfig())
}

func TestElasticsearchNewConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		e := NewElasticSearch(scout.DefaultConfig())
		if e.Name() != "elasticsearch" {
			t.Errorf("Name() = %q", e.Name())
		}
		if e.base != "http://127.0.0.1:9200" {
			t.Errorf("base = %s", e.base)
		}
	})

	t.Run("host override", func(t *testing.T) {
		t.Setenv("ELASTICSEARCH_HOST", "search.internal:9200")
		e := NewElasticSearch(scout.DefaultConfig())
		if e.base != "search.internal:9200" {
			t.Errorf("base = %s", e.base)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		if e := NewElasticSearch(nil); e.base != "http://127.0.0.1:9200" {
			t.Errorf("base = %s", e.base)
		}
	})

	t.Run("no basic auth by default", func(t *testing.T) {
		e := NewElasticSearch(scout.DefaultConfig())
		if _, ok := e.client.Transport.(*authRoundTripper); ok {
			t.Errorf("client transport = %T, want plain *http.Transport", e.client.Transport)
		}
	})
}

func TestElasticsearchWriteOps(t *testing.T) {
	models := []scout.ScoutModel{&stubModel{ID: 1, Title: "a"}, &stubModel{ID: 2, Title: "b"}}
	ctx := context.Background()
	e := NewElasticSearch(scout.DefaultConfig())

	t.Run("Update", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"result":"created","_id":"1"}`)
		e.base, e.client = srv.URL, srv.Client()
		if err := e.Update(ctx, models); err != nil {
			t.Fatal(err)
		}
		if len(rec.calls) != 2 ||
			rec.calls[0] != "POST /products/_doc/1" || rec.calls[1] != "POST /products/_doc/2" {
			t.Errorf("calls = %v", rec.calls)
		}
		body := rec.JSON(t)
		if body["title"] != "b" || body["price"] != 10.0 {
			t.Errorf("doc = %v", body)
		}
		if _, ok := body["_id"]; ok {
			t.Errorf("doc carries _id; the id lives in the URL path")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"result":"deleted"}`)
		e.base, e.client = srv.URL, srv.Client()
		if err := e.Delete(ctx, models); err != nil {
			t.Fatal(err)
		}
		if len(rec.calls) != 2 ||
			rec.calls[0] != "DELETE /products/_doc/1" || rec.calls[1] != "DELETE /products/_doc/2" {
			t.Errorf("calls = %v", rec.calls)
		}
	})

	t.Run("Flush", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"acknowledged":true}`)
		e.base, e.client = srv.URL, srv.Client()
		if err := e.Flush(ctx, models[0]); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodDelete || rec.path != "/products" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
	})

	t.Run("CreateIndex", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"acknowledged":true}`)
		e.base, e.client = srv.URL, srv.Client()
		if _, err := e.CreateIndex(ctx, "products", map[string]any{"settings": map[string]any{"number_of_shards": 1}}); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodPut || rec.path != "/products" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
		body := rec.JSON(t)
		if _, ok := body["settings"].(map[string]any); !ok {
			t.Errorf("settings = %v", body["settings"])
		}
	})

	t.Run("DeleteIndex", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"acknowledged":true}`)
		e.base, e.client = srv.URL, srv.Client()
		if _, err := e.DeleteIndex(ctx, "products"); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodDelete || rec.path != "/products" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
	})
}

func TestElasticsearchSearch(t *testing.T) {
	resp := `{
		"took": 3,
		"timed_out": false,
		"hits": {
			"total": {"value": 42, "relation": "eq"},
			"hits": [
				{"_index": "products", "_id": "2", "_score": 0.8, "_source": {"id": 2, "title": "B"}},
				{"_index": "products", "_id": "1", "_score": 0.5, "_source": {"id": 1, "title": "A"}}
			]
		},
		"aggregations": {"category": {"buckets": [{"key": "shoes", "doc_count": 5}]}}
	}`
	srv, rec := mtsCapture(t, resp)
	e := NewElasticSearch(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()

	b := newBuilder(t, nil)
	b.Take(20)
	b.Where("category", "shoes")
	b.OrderByDesc("price")

	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != http.MethodPost || rec.path != "/products/_search" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	body := rec.JSON(t)
	if body["size"] != 20.0 {
		t.Errorf("size = %v", body["size"])
	}
	q, ok := body["query"].(map[string]any)
	if !ok {
		t.Fatalf("query = %v", body["query"])
	}
	boolQ, ok := q["bool"].(map[string]any)
	if !ok {
		t.Fatalf("bool = %v", q)
	}
	if got := boolQ["filter"]; !reflect.DeepEqual(got, []any{map[string]any{"term": map[string]any{"category": "shoes"}}}) {
		t.Errorf("filter = %v", got)
	}
	if got := body["sort"]; !reflect.DeepEqual(got, []any{map[string]any{"price": map[string]any{"order": "desc"}}}) {
		t.Errorf("sort = %v", got)
	}

	if res.Total != 42 || res.Took != 3 {
		t.Errorf("total=%d took=%d", res.Total, res.Took)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("hits = %v", res.Hits)
	}
	h := res.Hits[0]
	if h.ID != "2" || h.Score != 0.8 || h.Source["title"] != "B" || h.Index != "products" {
		t.Errorf("hit = %+v", h)
	}
	if res.Aggregations == nil {
		t.Errorf("aggregations = %v", res.Aggregations)
	}
	if got := e.MapIDs(res); !reflect.DeepEqual(got, []any{"2", "1"}) {
		t.Errorf("MapIDs = %v", got)
	}
	if e.GetTotalCount(res) != 42 {
		t.Errorf("GetTotalCount = %d", e.GetTotalCount(res))
	}

	aggs, err := e.GetAggregations(context.Background(), b)
	if err != nil || aggs == nil {
		t.Errorf("GetAggregations = (%v, %v)", aggs, err)
	}
	facets, err := e.GetFacets(context.Background(), b)
	if err != nil || facets == nil {
		t.Errorf("GetFacets = (%v, %v)", facets, err)
	}
}

func TestElasticsearchPaginate(t *testing.T) {
	srv, rec := mtsCapture(t, `{"hits":{"total":{"value":37},"hits":[]}}`)
	e := NewElasticSearch(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()

	res, err := e.Paginate(context.Background(), newBuilder(t, nil), 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.JSON(t)
	if body["size"] != 10.0 || body["from"] != 20.0 {
		t.Errorf("size/from = %v/%v", body["size"], body["from"])
	}
	if res.Total != 37 {
		t.Errorf("total = %d", res.Total)
	}
}
