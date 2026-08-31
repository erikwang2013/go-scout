package engines

import (
	"context"
	"testing"

	"github.com/erikwang2013/go-scout"
)

func TestNullEngineImplementsEngine(t *testing.T) {
	var _ scout.Engine = NewNull()
}

func TestNullEngineSearchAndPaginate(t *testing.T) {
	e := NewNull()
	if e.Name() != "null" {
		t.Fatalf("Name() = %q, want %q", e.Name(), "null")
	}
	ctx := context.Background()
	b := &scout.Builder{}

	res, err := e.Search(ctx, b)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 0 || res.Total != 0 {
		t.Errorf("Search = (%v, %d), want empty", res.Hits, res.Total)
	}

	page, err := e.Paginate(ctx, b, 10, 2)
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if len(page.Hits) != 0 || page.Total != 0 {
		t.Errorf("Paginate = (%v, %d), want empty", page.Hits, page.Total)
	}
}

func TestNullEngineMapAndCount(t *testing.T) {
	e := NewNull()
	ctx := context.Background()

	if got := e.MapIDs(&scout.Result{}); got != nil {
		t.Errorf("MapIDs = %v, want nil", got)
	}
	if got := e.MapIDs(nil); got != nil {
		t.Errorf("MapIDs(nil) = %v, want nil", got)
	}
	models, err := e.Map(ctx, &scout.Builder{}, &scout.Result{})
	if err != nil || models != nil {
		t.Errorf("Map = (%v, %v), want (nil, nil)", models, err)
	}
	if got := e.GetTotalCount(&scout.Result{Total: 99}); got != 0 {
		t.Errorf("GetTotalCount = %d, want 0", got)
	}
}

func TestNullEngineMutationsAreNoOps(t *testing.T) {
	e := NewNull()
	ctx := context.Background()
	m := &rec{visible: true}

	for name, err := range map[string]error{
		"Update": e.Update(ctx, []scout.ScoutModel{m}),
		"Delete": e.Delete(ctx, []scout.ScoutModel{m}),
		"Flush":  e.Flush(ctx, m),
	} {
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if out, err := e.CreateIndex(ctx, "idx", map[string]any{"a": 1}); err != nil || out != nil {
		t.Errorf("CreateIndex = (%v, %v), want (nil, nil)", out, err)
	}
	if out, err := e.DeleteIndex(ctx, "idx"); err != nil || out != nil {
		t.Errorf("DeleteIndex = (%v, %v), want (nil, nil)", out, err)
	}
}
