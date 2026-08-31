package engines

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/erikwang2013/go-scout"
)

func TestXunSearchImplementsEngine(t *testing.T) {
	var _ scout.Engine = NewXunSearch(scout.DefaultConfig())
	var _ scout.AdvancedEngine = NewAdvancedXunSearch(scout.DefaultConfig())
}

func TestXunSearchNewConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		e := NewXunSearch(scout.DefaultConfig())
		if e.Name() != "xunsearch" {
			t.Errorf("Name() = %q", e.Name())
		}
		if e.indexBase != "http://127.0.0.1:8383" || e.searchBase != "http://127.0.0.1:8384" {
			t.Errorf("indexBase=%s searchBase=%s", e.indexBase, e.searchBase)
		}
		if e.charset != "utf-8" {
			t.Errorf("charset=%s", e.charset)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		t.Setenv("XUNSEARCH_INDEX_HOST", "http://idx.internal")
		t.Setenv("XUNSEARCH_INDEX_PORT", "8385")
		t.Setenv("XUNSEARCH_SEARCH_HOST", "http://search.internal")
		t.Setenv("XUNSEARCH_SEARCH_PORT", "8386")
		t.Setenv("XUNSEARCH_DEFAULT_INDEX", "blog")
		t.Setenv("XUNSEARCH_CHARSET", "gbk")
		e := NewXunSearch(scout.DefaultConfig())
		if e.indexBase != "http://idx.internal:8385" || e.searchBase != "http://search.internal:8386" {
			t.Errorf("indexBase=%s searchBase=%s", e.indexBase, e.searchBase)
		}
		if e.charset != "gbk" {
			t.Errorf("charset=%s", e.charset)
		}
	})

	t.Run("advanced is the same engine", func(t *testing.T) {
		a := NewAdvancedXunSearch(scout.DefaultConfig())
		if a.Name() != "xunsearch" {
			t.Errorf("advanced Name() = %q", a.Name())
		}
	})
}

func TestXunSearchWriteOps(t *testing.T) {
	models := []scout.ScoutModel{&stubModel{ID: 1, Title: "a"}, &stubModel{ID: 2, Title: "b"}}
	ctx := context.Background()
	e := NewXunSearch(scout.DefaultConfig())

	t.Run("Update", func(t *testing.T) {
		srv, rec := mtsCapture(t, `OK`)
		e.indexBase, e.client = srv.URL, srv.Client()
		if err := e.Update(ctx, models); err != nil {
			t.Fatal(err)
		}
		if len(rec.calls) != 2 || rec.method != http.MethodPost {
			t.Fatalf("calls = %v", rec.calls)
		}
		form, err := url.ParseQuery(string(rec.raw))
		if err != nil {
			t.Fatal(err)
		}
		if form.Get("cmd") != "add" || form.Get("project") != "products" {
			t.Errorf("form = %v", form)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(form.Get("data")), &doc); err != nil {
			t.Fatalf("data %q: %v", form.Get("data"), err)
		}
		if doc["id"] != 2.0 || doc["title"] != "b" {
			t.Errorf("doc = %v", doc)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		srv, rec := mtsCapture(t, `OK`)
		e.indexBase, e.client = srv.URL, srv.Client()
		if err := e.Delete(ctx, models); err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(rec.raw))
		if err != nil {
			t.Fatal(err)
		}
		if form.Get("cmd") != "del" || form.Get("project") != "products" {
			t.Errorf("form = %v", form)
		}
		// PHP parity: del carries the bare primary key, not a JSON document.
		if form.Get("data") != "2" {
			t.Errorf("data = %s", form.Get("data"))
		}
	})

	t.Run("Flush", func(t *testing.T) {
		srv, rec := mtsCapture(t, `OK`)
		e.indexBase, e.client = srv.URL, srv.Client()
		if err := e.Flush(ctx, models[0]); err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(rec.raw))
		if err != nil {
			t.Fatal(err)
		}
		if form.Get("cmd") != "clean" || form.Get("project") != "products" {
			t.Errorf("form = %v", form)
		}
		if _, ok := form["data"]; ok {
			t.Errorf("clean should carry no data, got %v", form)
		}
	})
}

func TestXunSearchSearch(t *testing.T) {
	resp := `{"count":2,"cost":3,"docs":[
		{"fields":{"id":2,"title":"B"},"percent":90,"docid":11},
		{"fields":{"id":1,"title":"A"},"percent":50,"docid":12}
	]}`
	srv, rec := mtsCapture(t, resp)
	e := NewXunSearch(scout.DefaultConfig())
	e.searchBase, e.client = srv.URL, srv.Client()

	b := newBuilder(t, nil)
	b.Take(20)

	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if rec.method != http.MethodGet {
		t.Errorf("method = %s", rec.method)
	}
	u, err := url.Parse(rec.path)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("project") != "products" || q.Get("q") != "widget" ||
		q.Get("per_page") != "20" || q.Get("start") != "0" || q.Get("charset") != "utf-8" {
		t.Errorf("query = %v", q)
	}
	if res.Total != 2 || res.Took != 3 {
		t.Errorf("total=%d took=%d", res.Total, res.Took)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("hits = %v", res.Hits)
	}
	h := res.Hits[0]
	if h.ID != 2.0 || h.Score != 90 || h.Source["title"] != "B" {
		t.Errorf("hit = %+v", h)
	}
	if got := e.MapIDs(res); !reflect.DeepEqual(got, []any{2.0, 1.0}) {
		t.Errorf("MapIDs = %v", got)
	}
	if e.GetTotalCount(res) != 2 {
		t.Errorf("GetTotalCount = %d", e.GetTotalCount(res))
	}
}

func TestXunSearchPaginate(t *testing.T) {
	srv, rec := mtsCapture(t, `{"count":37,"docs":[]}`)
	e := NewXunSearch(scout.DefaultConfig())
	e.searchBase, e.client = srv.URL, srv.Client()

	res, err := e.Paginate(context.Background(), newBuilder(t, nil), 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(rec.path)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("per_page") != "10" || q.Get("start") != "10" {
		t.Errorf("per_page/start = %v/%v", q.Get("per_page"), q.Get("start"))
	}
	if res.Total != 37 {
		t.Errorf("total = %d", res.Total)
	}
}

func TestXunSearchUnsupported(t *testing.T) {
	e := NewXunSearch(scout.DefaultConfig())
	ctx := context.Background()
	if _, err := e.CreateIndex(ctx, "x", nil); !errors.Is(err, scout.ErrNotSupported) {
		t.Errorf("CreateIndex = %v, want ErrNotSupported", err)
	}
	if _, err := e.DeleteIndex(ctx, "x"); !errors.Is(err, scout.ErrNotSupported) {
		t.Errorf("DeleteIndex = %v, want ErrNotSupported", err)
	}
	b := newBuilder(t, nil)
	if _, err := e.GetAggregations(ctx, b); !errors.Is(err, scout.ErrNotSupported) {
		t.Errorf("GetAggregations = %v, want ErrNotSupported", err)
	}
	if _, err := e.GetFacets(ctx, b); !errors.Is(err, scout.ErrNotSupported) {
		t.Errorf("GetFacets = %v, want ErrNotSupported", err)
	}
}
