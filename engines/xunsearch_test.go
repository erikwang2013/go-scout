package engines

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
}

func TestXunSearchAdvancedEmpty(t *testing.T) {
	e := NewXunSearch(scout.DefaultConfig())
	ctx := context.Background()
	b := newBuilder(t, nil)
	aggs, err := e.GetAggregations(ctx, b)
	if err != nil || len(aggs) != 0 {
		t.Errorf("GetAggregations = %v, %v", aggs, err)
	}
	srv, _ := mtsCapture(t, `{"count":0,"docs":[]}`)
	e.searchBase, e.client = srv.URL, srv.Client()
	facets, err := e.GetFacets(ctx, b)
	if err != nil || len(facets) != 0 {
		t.Errorf("GetFacets = %v, %v", facets, err)
	}
}

// XUNSEARCH_CONFIG_PATH mirrors XunSearchClient::newIndex() in the PHP plugin:
// <config_path>/<project>.ini supplies the project's daemons, charset and name,
// and a missing file is an error.
func TestXunSearchProjectIni(t *testing.T) {
	dir := t.TempDir()
	ini := "; demo project\n[unused]\nproject.name = blog\nproject.default_charset = gbk\n" +
		"server.index = 8391\nserver.search = search.internal:8392\n"
	if err := os.WriteFile(filepath.Join(dir, "posts.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUNSEARCH_CONFIG_PATH", dir)
	e := NewXunSearch(scout.DefaultConfig())

	ep, err := e.endpoint("posts")
	if err != nil {
		t.Fatalf("endpoint(posts): %v", err)
	}
	if ep.name != "blog" || ep.charset != "gbk" {
		t.Errorf("name=%q charset=%q, want blog/gbk", ep.name, ep.charset)
	}
	if ep.indexBase != "http://127.0.0.1:8391" {
		t.Errorf("bare port must reuse the configured host, got %s", ep.indexBase)
	}
	if ep.searchBase != "http://search.internal:8392" {
		t.Errorf("host:port must be honoured, got %s", ep.searchBase)
	}

	t.Run("missing ini is an error", func(t *testing.T) {
		if _, err := e.endpoint("nope"); err == nil {
			t.Error("a project without an ini should fail loudly, like the PHP plugin")
		}
	})

	t.Run("no config path falls back to env", func(t *testing.T) {
		t.Setenv("XUNSEARCH_CONFIG_PATH", "")
		t.Setenv("XUNSEARCH_INDEX_PORT", "8399")
		fallback := NewXunSearch(scout.DefaultConfig())
		ep, err := fallback.endpoint("posts")
		if err != nil {
			t.Fatalf("endpoint without config_path: %v", err)
		}
		if ep.name != "posts" || ep.charset != "utf-8" || ep.indexBase != "http://127.0.0.1:8399" {
			t.Errorf("fallback wrong: %+v", ep)
		}
	})
}

func TestXSIniParsing(t *testing.T) {
	ini := parseXSIni("; comment\n# comment\n[section]\nproject.name = demo\ndefault_charset: utf-8")
	if ini["project.name"] != "demo" {
		t.Errorf("project.name = %q", ini["project.name"])
	}
	if _, ok := ini["default_charset"]; ok {
		t.Error("a line without '=' must be ignored, not parsed")
	}
	cases := []struct{ in, host, want string }{
		{"8383", "http://127.0.0.1", "http://127.0.0.1:8383"},
		{"idx.internal:8390", "http://127.0.0.1", "http://idx.internal:8390"},
		{"https://idx.internal:8390/", "http://127.0.0.1", "https://idx.internal:8390"},
	}
	for _, c := range cases {
		if got := xsHostPort(c.in, c.host); got != c.want {
			t.Errorf("xsHostPort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
