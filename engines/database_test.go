package engines

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/erikwang2013/go-scout"
)

// sqlModel is a ScoutModel whose searchable payload and attribute columns are
// injected per test, so the generated SQL can be asserted exactly.
type sqlModel struct {
	table    string
	fields   map[string]any
	keyName  string
	keyType  string
	fullText []string
	prefix   []string
	opts     map[string]any
}

func (m sqlModel) ScoutKey() any                     { return m.fields[m.keyName] }
func (m sqlModel) TableName() string                 { return m.table }
func (m sqlModel) KeyName() string                   { return m.keyName }
func (m sqlModel) KeyType() string                   { return m.keyType }
func (m sqlModel) FullTextColumns() []string         { return m.fullText }
func (m sqlModel) FullTextOptions() map[string]any   { return m.opts }
func (m sqlModel) PrefixColumns() []string           { return m.prefix }
func (m sqlModel) ToSearchableArray() map[string]any { return m.fields }
func (m sqlModel) ShouldBeSearchable() bool          { return true }

// softModel is sqlModel with soft-delete handling enabled.
type softModel struct{ sqlModel }

func (m softModel) Trashed() bool         { return false }
func (m softModel) DeletedAt() *time.Time { return nil }

func recModel() sqlModel {
	return sqlModel{
		table: "recs", fields: map[string]any{"id": 1, "kind": "b", "title": "a"},
		keyName: "id", keyType: "int",
	}
}

func sqlBuilder(m scout.ScoutModel, query string) *scout.Builder {
	return &scout.Builder{
		Model: m, Query: query,
		Wheres: map[string]any{}, WhereIns: map[string][]any{}, WhereNotIns: map[string][]any{},
	}
}

func assertSQL(t *testing.T, got, want string, gotArgs, wantArgs []any) {
	t.Helper()
	if got != want {
		t.Errorf("SQL:\n got: %s\nwant: %s", got, want)
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args:\n got: %v\nwant: %v", gotArgs, wantArgs)
	}
}

func TestDatabaseImplementsEngine(t *testing.T) {
	var _ scout.Engine = NewDatabase(nil, "mysql")
}

func TestDatabaseNoQuery(t *testing.T) {
	got, args, err := BuildSQL(context.Background(), "mysql", sqlBuilder(recModel(), ""), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got, "SELECT * FROM recs ORDER BY recs.id DESC", args, nil)
}

func TestDatabaseTextQuery(t *testing.T) {
	got, args, err := BuildSQL(context.Background(), "mysql", sqlBuilder(recModel(), "alpha"), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.id LIKE ? OR recs.kind LIKE ? OR recs.title LIKE ?) ORDER BY recs.id DESC",
		args, []any{"%alpha%", "%alpha%", "%alpha%"})
}

func TestDatabasePgsqlUsesILike(t *testing.T) {
	got, args, err := BuildSQL(context.Background(), "pgsql", sqlBuilder(recModel(), "alpha"), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.id ILIKE ? OR recs.kind ILIKE ? OR recs.title ILIKE ?) ORDER BY recs.id DESC",
		args, []any{"%alpha%", "%alpha%", "%alpha%"})
}

func TestDatabaseNumericPrimaryKeyMatch(t *testing.T) {
	got, args, err := BuildSQL(context.Background(), "mysql", sqlBuilder(recModel(), "42"), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.id = ? OR recs.kind LIKE ? OR recs.title LIKE ?) ORDER BY recs.id DESC",
		args, []any{"42", "%42%", "%42%"})
}

func TestDatabaseNumericPrimaryKeyGatedOnDriverAndKey(t *testing.T) {
	// pgsql refuses a numeric compare that overflows its int range.
	got, args, err := BuildSQL(context.Background(), "pgsql", sqlBuilder(recModel(), "99999999999999999999"), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.id ILIKE ? OR recs.kind ILIKE ? OR recs.title ILIKE ?) ORDER BY recs.id DESC",
		args, []any{"%99999999999999999999%", "%99999999999999999999%", "%99999999999999999999%"})

	// A non-integer key never gets the primary-key clause.
	m := recModel()
	m.keyType = "string"
	got, args, err = BuildSQL(context.Background(), "mysql", sqlBuilder(m, "42"), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.id LIKE ? OR recs.kind LIKE ? OR recs.title LIKE ?) ORDER BY recs.id DESC",
		args, []any{"%42%", "%42%", "%42%"})
}

func TestDatabasePrefixColumns(t *testing.T) {
	m := recModel()
	m.prefix = []string{"title"}
	got, args, err := BuildSQL(context.Background(), "mysql", sqlBuilder(m, "al"), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.id LIKE ? OR recs.kind LIKE ? OR recs.title LIKE ?) ORDER BY recs.id DESC",
		args, []any{"%al%", "%al%", "al%"})
}

func TestDatabaseConstraints(t *testing.T) {
	b := sqlBuilder(recModel(), "")
	b.Wheres["kind"] = "x"
	b.WhereIns["id"] = []any{1, 2}
	b.WhereNotIns["kind"] = []any{"z"}
	got, args, err := BuildSQL(context.Background(), "mysql", b, 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.kind = ? AND recs.id IN (?,?) AND recs.kind NOT IN (?)) ORDER BY recs.id DESC",
		args, []any{"x", 1, 2, "z"})
}

func TestDatabaseCallbackReceivesSQLQuery(t *testing.T) {
	b := sqlBuilder(recModel(), "")
	b.Callback = func(_ context.Context, _ *scout.Builder, params any) any {
		sq, ok := params.(*SQLQuery)
		if !ok {
			t.Fatalf("callback params = %T, want *SQLQuery", params)
		}
		sq.Where("kind", "=", "pro").Where("id", ">", 5).WhereIn("id", []any{3, 4})
		return nil
	}
	got, args, err := BuildSQL(context.Background(), "mysql", b, 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.kind = ? AND recs.id > ? AND recs.id IN (?,?)) ORDER BY recs.id DESC",
		args, []any{"pro", 5, 3, 4})
}

func TestDatabaseQueryCallbackErrorPropagates(t *testing.T) {
	b := sqlBuilder(recModel(), "")
	b.QueryCallback = func(context.Context, *scout.Builder) error { return errors.New("boom") }
	if _, _, err := BuildSQL(context.Background(), "mysql", b, 0, 0); err == nil || err.Error() != "scout: database query callback: boom" {
		t.Fatalf("err = %v, want wrapped boom", err)
	}
}

func TestDatabaseSoftDeletes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		soft int
		set  bool
		want string
	}{
		{"withoutTrashed", 0, true, "SELECT * FROM recs WHERE recs.deleted_at IS NULL ORDER BY recs.id DESC"},
		{"onlyTrashed", 1, true, "SELECT * FROM recs WHERE recs.deleted_at IS NOT NULL ORDER BY recs.id DESC"},
		{"defaultExcludesTrashed", 0, false, "SELECT * FROM recs WHERE recs.deleted_at IS NULL ORDER BY recs.id DESC"},
	} {
		b := sqlBuilder(softModel{recModel()}, "")
		if tc.set {
			b.Wheres["__soft_deleted"] = tc.soft
		}
		got, args, err := BuildSQL(ctx, "mysql", b, 0, 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		assertSQL(t, got, tc.want, args, nil)
	}

	t.Setenv("SCOUT_SOFT_DELETE", "true")
	b := sqlBuilder(softModel{recModel()}, "")
	b.Config = scout.DefaultConfig()
	got, args, err := BuildSQL(ctx, "mysql", b, 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	// withTrashed: no soft-delete predicate at all.
	assertSQL(t, got, "SELECT * FROM recs ORDER BY recs.id DESC", args, nil)
}

func TestDatabaseOrderByAndRelevance(t *testing.T) {
	m := sqlModel{
		table: "recs", fields: map[string]any{"id": 1, "body": "hello"},
		keyName: "id", keyType: "int", fullText: []string{"body"},
		opts: map[string]any{"language": "french", "mode": "websearch"},
	}
	got, args, err := BuildSQL(context.Background(), "pgsql", sqlBuilder(m, "42"), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got, `SELECT * FROM recs WHERE (recs.id = ? OR recs.body @@ websearch_to_tsquery("french", ?)) ORDER BY ts_rank(to_tsvector("french", recs.body), websearch_to_tsquery("french", ?)) DESC`,
		args, []any{"42", "42", "42"})

	// Explicit orders take precedence over relevance ranking.
	b := sqlBuilder(m, "").OrderBy("kind", "asc")
	got, args, err = BuildSQL(context.Background(), "pgsql", b, 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got, "SELECT * FROM recs ORDER BY recs.kind ASC", args, nil)
}

func TestDatabaseFullTextFallsBackToLikeOutsidePgsql(t *testing.T) {
	m := sqlModel{
		table: "recs", fields: map[string]any{"id": 1, "body": "hello"},
		keyName: "id", keyType: "string", fullText: []string{"body"},
	}
	got, args, err := BuildSQL(context.Background(), "mysql", sqlBuilder(m, "alpha"), 0, 0)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	// Deliberate parity with PHP: full-text columns suppress the pk ORDER BY,
	// and ts_rank relevance ordering is pgsql-only, so the LIKE fallback
	// intentionally has no ORDER BY.
	assertSQL(t, got,
		"SELECT * FROM recs WHERE (recs.id LIKE ? OR recs.body LIKE ?)",
		args, []any{"%alpha%", "%alpha%"})
}

func TestDatabaseLimitOffsetAndCount(t *testing.T) {
	got, args, err := BuildSQL(context.Background(), "mysql", sqlBuilder(recModel(), ""), 10, 5)
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	assertSQL(t, got, "SELECT * FROM recs ORDER BY recs.id DESC LIMIT ? OFFSET ?", args, []any{10, 5})

	got, args, err = BuildCountSQL(context.Background(), "mysql", sqlBuilder(recModel(), ""))
	if err != nil {
		t.Fatalf("BuildCountSQL: %v", err)
	}
	assertSQL(t, got, "SELECT COUNT(*) FROM recs", args, nil)

	b := sqlBuilder(recModel(), "alpha")
	b.Wheres["kind"] = "x"
	got, args, err = BuildCountSQL(context.Background(), "mysql", b)
	if err != nil {
		t.Fatalf("BuildCountSQL: %v", err)
	}
	assertSQL(t, got,
		"SELECT COUNT(*) FROM recs WHERE (recs.id LIKE ? OR recs.kind LIKE ? OR recs.title LIKE ?) AND (recs.kind = ?)",
		args, []any{"%alpha%", "%alpha%", "%alpha%", "x"})
}

func TestDatabaseRejectsInvalidIdentifiers(t *testing.T) {
	ctx := context.Background()
	bad := recModel()
	bad.table = "recs; DROP TABLE x"
	if _, _, err := BuildSQL(ctx, "mysql", sqlBuilder(bad, ""), 0, 0); err == nil {
		t.Error("table injection accepted")
	}

	ugly := recModel()
	ugly.fields = map[string]any{"id": 1, "bad col": "x"}
	if _, _, err := BuildSQL(ctx, "mysql", sqlBuilder(ugly, "x"), 0, 0); err == nil {
		t.Error("column injection accepted")
	}

	sq := NewSQLQuery("recs").Where("bad col", "=", 1)
	if sq.Error() == nil {
		t.Error("SQLQuery accepted an invalid column")
	}
	if len(sq.Clauses()) != 0 {
		t.Errorf("SQLQuery recorded a clause after failing: %v", sq.Clauses())
	}
}

func TestSQLQueryFragments(t *testing.T) {
	sq := NewSQLQuery("recs")
	sq.WhereIn("id", nil)
	if c := sq.Clauses(); len(c) != 1 || c[0] != "0 = 1" || len(sq.Args()) != 0 {
		t.Errorf("empty IN = (%v, %v), want ([0 = 1], [])", c, sq.Args())
	}

	sq2 := NewSQLQuery("recs").WhereNotIn("id", nil)
	if c := sq2.Clauses(); len(c) != 1 || c[0] != "1 = 1" {
		t.Errorf("empty NOT IN = %v, want [1 = 1]", c)
	}

	sq3 := NewSQLQuery("recs").RawSQL("recs.id IN (SELECT id FROM recs)", nil, 7)
	if c, a := sq3.Clauses(), sq3.Args(); len(c) != 1 || c[0] != "recs.id IN (SELECT id FROM recs)" || !reflect.DeepEqual(a, []any{nil, 7}) {
		t.Errorf("RawSQL = (%v, %v)", c, a)
	}

	// An already-qualified column is not re-qualified with the table.
	sq4 := NewSQLQuery("recs").Where("other.id", "=", 1)
	if c := sq4.Clauses(); len(c) != 1 || c[0] != "other.id = ?" {
		t.Errorf("qualified column = %v", c)
	}
}

func TestDatabaseSearchRequiresConnection(t *testing.T) {
	e := NewDatabase(nil, "mysql")
	if _, err := e.Search(context.Background(), sqlBuilder(recModel(), "")); err == nil {
		t.Error("Search with a nil *sql.DB did not fail")
	}
	if _, err := e.Paginate(context.Background(), sqlBuilder(recModel(), ""), 10, 1); err == nil {
		t.Error("Paginate with a nil *sql.DB did not fail")
	}
}
