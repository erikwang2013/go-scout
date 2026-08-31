package engines

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/erikwang2013/go-scout"
)

func TestOpenSearchImplementsEngine(t *testing.T) {
	var _ scout.Engine = NewOpenSearch(scout.DefaultConfig())
	var _ scout.AdvancedEngine = NewOpenSearch(scout.DefaultConfig())
}

func TestOpenSearchNewConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		e := NewOpenSearch(scout.DefaultConfig())
		if e.Name() != "opensearch" {
			t.Errorf("Name() = %q", e.Name())
		}
		if e.base != "https://127.0.0.1:6205" {
			t.Errorf("base = %s", e.base)
		}
	})

	t.Run("host override", func(t *testing.T) {
		t.Setenv("OPENSEARCH_HTTP_HOST", "search.internal:9200")
		e := NewOpenSearch(scout.DefaultConfig())
		if e.base != "search.internal:9200" {
			t.Errorf("base = %s", e.base)
		}
	})

	t.Run("default credentials wire basic auth", func(t *testing.T) {
		e := NewOpenSearch(scout.DefaultConfig())
		if _, ok := e.client.Transport.(*authRoundTripper); !ok {
			t.Errorf("client transport = %T, want *authRoundTripper", e.client.Transport)
		}
	})
}

// TestHTTPClientBasicAuthHeader verifies the shared client injects basic auth
// into every request, as the opensearch engine's default admin/admin rely on.
func TestHTTPClientBasicAuthHeader(t *testing.T) {
	srv, rec := mtsCapture(t, `{}`)
	client := HTTPClient(0, "user", "pass", false)
	if _, err := DoJSON(context.Background(), client, http.MethodGet, srv.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := rec.header.Get("Authorization"); got != "Basic dXNlcjpwYXNz" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestOpenSearchWriteOps(t *testing.T) {
	models := []scout.ScoutModel{&stubModel{ID: 1, Title: "a"}}
	ctx := context.Background()
	e := NewOpenSearch(scout.DefaultConfig())

	t.Run("Update", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"result":"created","_id":"1"}`)
		e.base, e.client = srv.URL, srv.Client()
		if err := e.Update(ctx, models); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodPost || rec.path != "/products/_doc/1" {
			t.Errorf("got %s %s", rec.method, rec.path)
		}
		body := rec.JSON(t)
		if body["title"] != "a" || body["price"] != 10.0 {
			t.Errorf("doc = %v", body)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		srv, rec := mtsCapture(t, `{"result":"deleted"}`)
		e.base, e.client = srv.URL, srv.Client()
		if err := e.Delete(ctx, models); err != nil {
			t.Fatal(err)
		}
		if rec.method != http.MethodDelete || rec.path != "/products/_doc/1" {
			t.Errorf("got %s %s", rec.method, rec.path)
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

func TestOpenSearchSearch(t *testing.T) {
	resp := `{
		"took": 1,
		"hits": {
			"total": {"value": 7, "relation": "eq"},
			"hits": [
				{"_index": "products", "_id": "3", "_score": 0.9, "_source": {"id": 3, "title": "C"}}
			]
		}
	}`
	srv, rec := mtsCapture(t, resp)
	e := NewOpenSearch(scout.DefaultConfig())
	e.base, e.client = srv.URL, srv.Client()

	res, err := e.Search(context.Background(), newBuilder(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != http.MethodPost || rec.path != "/products/_search" {
		t.Errorf("got %s %s", rec.method, rec.path)
	}
	if res.Total != 7 || res.Took != 1 {
		t.Errorf("total=%d took=%d", res.Total, res.Took)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits = %v", res.Hits)
	}
	h := res.Hits[0]
	if h.ID != "3" || h.Score != 0.9 || h.Source["title"] != "C" {
		t.Errorf("hit = %+v", h)
	}
	if got := e.MapIDs(res); !reflect.DeepEqual(got, []any{"3"}) {
		t.Errorf("MapIDs = %v", got)
	}
	if e.GetTotalCount(res) != 7 {
		t.Errorf("GetTotalCount = %d", e.GetTotalCount(res))
	}
}
