package engines

import (
	"context"
	"testing"
	"time"

	"github.com/erikwang2013/go-scout"
)

// rec is a searchable record used by the collection and database tests.
type rec struct {
	id      int
	title   string
	kind    string
	visible bool
	trashed bool
}

func (r *rec) ScoutKey() any            { return r.id }
func (r *rec) TableName() string        { return "recs" }
func (r *rec) KeyName() string          { return "id" }
func (r *rec) ShouldBeSearchable() bool { return r.visible }
func (r *rec) Trashed() bool            { return r.trashed }
func (r *rec) DeletedAt() *time.Time    { return nil }
func (r *rec) ToSearchableArray() map[string]any {
	return map[string]any{"id": r.id, "title": r.title, "kind": r.kind}
}

// recSource is an in-memory scout.Source for the tests.
type recSource struct{ rows []*rec }

func (s *recSource) All(any) ([]scout.ScoutModel, error) {
	out := make([]scout.ScoutModel, len(s.rows))
	for i, r := range s.rows {
		out[i] = r
	}
	return out, nil
}

func (s *recSource) ByIDs(_ any, ids []any) ([]scout.ScoutModel, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[scout.KeyString(id)] = true
	}
	var out []scout.ScoutModel
	for _, r := range s.rows {
		if want[scout.KeyString(r.id)] {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *recSource) Count(any) (int, error) { return len(s.rows), nil }

func mkBuilder(rows []*rec, query string) *scout.Builder {
	return &scout.Builder{
		Model:       &rec{},
		Source:      &recSource{rows: rows},
		Query:       query,
		Wheres:      map[string]any{},
		WhereIns:    map[string][]any{},
		WhereNotIns: map[string][]any{},
	}
}

// mkSoftBuilder enables the soft_delete config, which is what actually makes
// WithTrashed() include trashed rows -- mirroring scout_config('soft_delete').
func mkSoftBuilder(t *testing.T, rows []*rec, query string) *scout.Builder {
	t.Helper()
	t.Setenv("SCOUT_SOFT_DELETE", "true")
	b := mkBuilder(rows, query)
	b.Config = scout.DefaultConfig()
	return b
}

func testRows() []*rec {
	return []*rec{
		{id: 1, title: "alpha one", kind: "a", visible: true},
		{id: 2, title: "beta two", kind: "b", visible: true},
		{id: 3, title: "ALPHA three", kind: "a", visible: true, trashed: true},
		{id: 4, title: "gamma", kind: "c", visible: false},
	}
}

func keysOf(t *testing.T, res *scout.Result) []int {
	t.Helper()
	out := make([]int, 0, len(res.Hits))
	for _, h := range res.Hits {
		if id, ok := h.ID.(int); ok {
			out = append(out, id)
		}
	}
	return out
}

func TestCollectionSearchTextFilter(t *testing.T) {
	e := NewCollection()
	res, err := e.Search(context.Background(), mkBuilder(testRows(), "alpha"))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// id 3 matches the text but is soft-deleted; id 4 is unsearchable.
	if got := keysOf(t, res); len(got) != 1 || got[0] != 1 {
		t.Fatalf("hits = %v, want [1]", got)
	}
	if res.Total != 1 {
		t.Errorf("Total = %d, want 1", res.Total)
	}
}

func TestCollectionSearchWithTrashedAndLimit(t *testing.T) {
	e := NewCollection()
	b := mkSoftBuilder(t, testRows(), "alpha").WithTrashed().Take(1)
	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := keysOf(t, res); len(got) != 1 || got[0] != 3 {
		t.Fatalf("hits = %v, want [3] (key desc, then capped)", got)
	}
	if res.Total != 1 {
		t.Errorf("Total = %d, want 1", res.Total)
	}
}

func TestCollectionSearchWheres(t *testing.T) {
	e := NewCollection()

	res, err := e.Search(context.Background(), mkSoftBuilder(t, testRows(), "").Where("kind", "a").WithTrashed())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := keysOf(t, res); !equalInts(got, []int{3, 1}) {
		t.Fatalf("wheres hits = %v, want [3 1]", got)
	}

	res, err = e.Search(context.Background(), mkBuilder(testRows(), "").WhereIn("kind", []any{"a", "b"}))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := keysOf(t, res); !equalInts(got, []int{2, 1}) {
		t.Fatalf("whereIn hits = %v, want [2 1]", got)
	}

	res, err = e.Search(context.Background(), mkSoftBuilder(t, testRows(), "").WhereNotIn("kind", []any{"c"}).WithTrashed())
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := keysOf(t, res); !equalInts(got, []int{3, 2, 1}) {
		t.Fatalf("whereNotIn hits = %v, want [3 2 1]", got)
	}
}

func TestCollectionSearchExplicitOrder(t *testing.T) {
	e := NewCollection()
	b := mkSoftBuilder(t, testRows(), "").WhereNotIn("kind", []any{"c"}).WithTrashed().OrderBy("id", "asc")
	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := keysOf(t, res); !equalInts(got, []int{1, 2, 3}) {
		t.Fatalf("hits = %v, want [1 2 3]", got)
	}
}

func TestCollectionSearchOnlyTrashed(t *testing.T) {
	e := NewCollection()
	b := mkBuilder(testRows(), "").OnlyTrashed()
	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := keysOf(t, res); !equalInts(got, []int{3}) {
		t.Fatalf("hits = %v, want [3]", got)
	}
}

func TestCollectionCallbackSkipsWheres(t *testing.T) {
	e := NewCollection()
	called := false
	b := mkBuilder(testRows(), "").Where("kind", "z")
	b.Callback = func(context.Context, *scout.Builder, any) any {
		called = true
		return nil
	}
	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !called {
		t.Fatal("callback was not invoked")
	}
	// The wheres filter must be bypassed: every searchable record survives.
	if got := keysOf(t, res); !equalInts(got, []int{2, 1}) {
		t.Fatalf("hits = %v, want [2 1]", got)
	}
}

func TestCollectionPaginate(t *testing.T) {
	e := NewCollection()
	b := mkSoftBuilder(t, testRows(), "").WithTrashed()
	res, err := e.Paginate(context.Background(), b, 2, 2)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if got := keysOf(t, res); !equalInts(got, []int{1}) {
		t.Fatalf("page 2 hits = %v, want [1]", got)
	}
	if res.Total != 3 {
		t.Errorf("Total = %d, want 3", res.Total)
	}

	res, err = e.Paginate(context.Background(), b, 2, 99)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if len(res.Hits) != 0 || res.Total != 3 {
		t.Errorf("out-of-range page = (%d hits, total %d), want (0, 3)", len(res.Hits), res.Total)
	}
}

func TestCollectionMapIDsMapAndTotal(t *testing.T) {
	e := NewCollection()
	b := mkBuilder(testRows(), "").WhereIn("kind", []any{"a", "b"})
	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got := e.MapIDs(res); !equalAny(got, []any{2, 1}) {
		t.Fatalf("MapIDs = %v, want [2 1]", got)
	}
	if got := e.GetTotalCount(res); got != 2 {
		t.Errorf("GetTotalCount = %d, want 2", got)
	}

	models, err := e.Map(context.Background(), b, res)
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	got := make([]int, 0, len(models))
	for _, m := range models {
		got = append(got, m.ScoutKey().(int))
	}
	if !equalInts(got, []int{2, 1}) {
		t.Fatalf("Map order = %v, want [2 1]", got)
	}

	empty, err := e.Map(context.Background(), b, &scout.Result{})
	if err != nil || empty != nil {
		t.Errorf("Map(empty) = (%v, %v), want (nil, nil)", empty, err)
	}

	if got := e.MapIDs(nil); got != nil {
		t.Errorf("MapIDs(nil) = %v, want nil", got)
	}
}

func TestCollectionNoSource(t *testing.T) {
	e := NewCollection()
	b := &scout.Builder{Model: &rec{}}
	res, err := e.Search(context.Background(), b)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 0 || res.Total != 0 {
		t.Errorf("no-source Search = (%d hits, total %d), want empty", len(res.Hits), res.Total)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalAny(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
