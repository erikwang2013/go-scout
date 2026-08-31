package engines

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/erikwang2013/go-scout"
)

var (
	// identRe whitelists the SQL identifiers this engine will quote into a
	// statement: a bare name, or a table-qualified "t.c" form.
	identRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.]*$`)
	// languageRe whitelists tsvector config names embedded as SQL literals.
	languageRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// deletedAtColumn is the soft-delete column. ponytail: fixed name -- the Go
// SoftDeleter contract does not expose it, so a model using another column must
// not rely on this engine's soft-delete filter.
const deletedAtColumn = "deleted_at"

// DatabaseEngine serves searches straight from the model's backing table with
// LIKE/ILIKE predicates, so indexing operations are no-ops. driver selects the
// dialect ("mysql", "pgsql"/"postgres", "sqlite").
type DatabaseEngine struct {
	db     *sql.DB
	driver string
}

// NewDatabase returns a DatabaseEngine over db.
func NewDatabase(db *sql.DB, driver string) *DatabaseEngine {
	return &DatabaseEngine{db: db, driver: strings.ToLower(driver)}
}

// Name returns the driver name.
func (e *DatabaseEngine) Name() string { return "database" }

// Update is a no-op: the table is the index.
func (e *DatabaseEngine) Update(context.Context, []scout.ScoutModel) error { return nil }

// Delete is a no-op: the table is the index.
func (e *DatabaseEngine) Delete(context.Context, []scout.ScoutModel) error { return nil }

// Flush is a no-op: the table is the index.
func (e *DatabaseEngine) Flush(context.Context, scout.ScoutModel) error { return nil }

// CreateIndex is a no-op.
func (e *DatabaseEngine) CreateIndex(context.Context, string, map[string]any) (any, error) {
	return nil, nil
}

// DeleteIndex is a no-op.
func (e *DatabaseEngine) DeleteIndex(context.Context, string) (any, error) { return nil, nil }

// Search returns the matching models, capped at b.Limit. Total is the post-cap
// count, mirroring DatabaseEngine::search().
func (e *DatabaseEngine) Search(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	models, err := e.searchModels(ctx, b, b.GetLimit(), b.GetOffset())
	if err != nil {
		return nil, err
	}
	hits := modelHits(models)
	return &scout.Result{Hits: hits, Total: len(hits)}, nil
}

// Paginate returns the perPage/page slice plus a COUNT(*) total.
func (e *DatabaseEngine) Paginate(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	if perPage <= 0 {
		perPage = 15
	}
	if page < 1 {
		page = 1
	}
	offset := b.GetOffset()
	if offset < 0 {
		offset = (page - 1) * perPage
	}
	models, err := e.searchModels(ctx, b, perPage, offset)
	if err != nil {
		return nil, err
	}
	countSQL, countArgs, err := e.statement(ctx, b, -1, -1, true)
	if err != nil {
		return nil, err
	}
	var total int
	if err := e.db.QueryRowContext(ctx, countSQL, countArgs...).Scan(&total); err != nil {
		return nil, fmt.Errorf("scout: database count: %w", err)
	}
	return &scout.Result{Hits: modelHits(models), Total: total}, nil
}

// MapIDs returns the document ids carried on the hits.
func (e *DatabaseEngine) MapIDs(results *scout.Result) []any { return hitIDs(results) }

// Map reloads the models behind the hits by key, filtering to hit keys and
// restoring hit order.
func (e *DatabaseEngine) Map(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	return hydrateOrdered(ctx, b, hitIDs(results))
}

// GetTotalCount returns the total carried on the result.
func (e *DatabaseEngine) GetTotalCount(results *scout.Result) int {
	if results == nil {
		return 0
	}
	return results.Total
}

// searchModels runs the statement, pulls the primary keys out of the rows and
// hydrates them through the builder's source, preserving statement order.
func (e *DatabaseEngine) searchModels(ctx context.Context, b *scout.Builder, limit, offset int) ([]scout.ScoutModel, error) {
	if e.db == nil {
		return nil, fmt.Errorf("scout: database engine: no connection")
	}
	sqlStr, args, err := e.statement(ctx, b, limit, offset, false)
	if err != nil {
		return nil, err
	}
	rows, err := e.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("scout: database search: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("scout: database columns: %w", err)
	}
	keyName := scout.KeyNameOf(b.Model)
	pkIdx := -1
	for i, c := range cols {
		if strings.EqualFold(c, keyName) {
			pkIdx = i
			break
		}
	}
	if pkIdx < 0 {
		return nil, fmt.Errorf("scout: database search: primary key %q missing from result set", keyName)
	}

	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	var ids []any
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("scout: database scan: %w", err)
		}
		ids = append(ids, vals[pkIdx])
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scout: database rows: %w", err)
	}
	return hydrateOrdered(ctx, b, ids)
}

// statement renders the SELECT (or COUNT(*)) for b. limit and offset are
// disabled by passing negative values; count ignores both.
func (e *DatabaseEngine) statement(ctx context.Context, b *scout.Builder, limit, offset int, count bool) (string, []any, error) {
	tbl, err := ident(b.Model.TableName())
	if err != nil {
		return "", nil, err
	}
	var frags []string
	var args []any

	text, textArgs, err := e.searchConditions(b)
	if err != nil {
		return "", nil, err
	}
	if text != "" {
		frags = append(frags, "("+text+")")
		args = append(args, textArgs...)
	}
	conds, condArgs, err := e.constraints(ctx, b)
	if err != nil {
		return "", nil, err
	}
	if conds != "" {
		frags = append(frags, "("+conds+")")
		args = append(args, condArgs...)
	}
	soft, err := e.softDeleteClause(b)
	if err != nil {
		return "", nil, err
	}
	if soft != "" {
		frags = append(frags, soft)
	}

	where := ""
	if len(frags) > 0 {
		where = " WHERE " + strings.Join(frags, " AND ")
	}
	if count {
		return "SELECT COUNT(*) FROM " + tbl + where, args, nil
	}

	var sb strings.Builder
	sb.WriteString("SELECT * FROM ")
	sb.WriteString(tbl)
	sb.WriteString(where)

	order, orderArgs, err := e.orderClause(b)
	if err != nil {
		return "", nil, err
	}
	if order != "" {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(order)
		args = append(args, orderArgs...)
	}
	if limit > 0 {
		sb.WriteString(" LIMIT ?")
		args = append(args, limit)
	}
	if offset > 0 {
		sb.WriteString(" OFFSET ?")
		args = append(args, offset)
	}
	return sb.String(), args, nil
}

// searchConditions builds the OR group over the searchable columns, mirroring
// initializeSearchQuery. Columns are sorted because Go maps have no insertion
// order; PHP uses array_keys, which is irrelevant to OR semantics.
func (e *DatabaseEngine) searchConditions(b *scout.Builder) (string, []any, error) {
	q := strings.TrimSpace(b.Query)
	if q == "" {
		return "", nil, nil
	}
	table := b.Model.TableName()
	columns := scout.FieldNames(b.Model)
	sort.Strings(columns)
	prefix := scout.PrefixColumnsOf(b.Model)
	fullText := scout.FullTextColumnsOf(b.Model)
	keyName := scout.KeyNameOf(b.Model)

	var parts []string
	var args []any

	canPK := isDigits(q) && scout.IsIntegerKey(b.Model) && contains(columns, keyName)
	if canPK && e.isPgsql() {
		if _, err := strconv.ParseInt(q, 10, 64); err != nil {
			canPK = false
		}
	}
	if canPK {
		col, err := qualify(table, keyName)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, col+" = ?")
		args = append(args, q)
	}

	like := "LIKE"
	if e.isPgsql() {
		like = "ILIKE"
	}
	for _, col := range columns {
		if contains(fullText, col) {
			continue
		}
		if canPK && col == keyName {
			continue
		}
		pat := "%" + q + "%"
		if contains(prefix, col) {
			pat = q + "%"
		}
		ic, err := qualify(table, col)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, ic+" "+like+" ?")
		args = append(args, pat)
	}

	if len(fullText) > 0 {
		if e.isPgsql() {
			opts := scout.FullTextOptionsOf(b.Model)
			language := tsLanguage(opts)
			frag := make([]string, 0, len(fullText))
			for _, col := range fullText {
				ic, err := qualify(table, col)
				if err != nil {
					return "", nil, err
				}
				frag = append(frag, fmt.Sprintf("%s @@ %s(%q, ?)", ic, tsMode(opts), language))
			}
			parts = append(parts, strings.Join(frag, " OR "))
			args = append(args, q)
		} else {
			// ponytail: no portable full-text WHERE outside pgsql, so fall back to
			// LIKE. MySQL could use MATCH ... AGAINST when FULLTEXT indexes exist.
			for _, col := range fullText {
				ic, err := qualify(table, col)
				if err != nil {
					return "", nil, err
				}
				parts = append(parts, ic+" "+like+" ?")
				args = append(args, "%"+q+"%")
			}
		}
	}
	return strings.Join(parts, " OR "), args, nil
}

// constraints adds the developer-defined conditions: the callback when present,
// otherwise wheres/whereIns/whereNotIns. The query callback always runs last,
// mirroring addAdditionalConstraints.
func (e *DatabaseEngine) constraints(ctx context.Context, b *scout.Builder) (string, []any, error) {
	if cb := b.GetCallback(); cb != nil {
		sq := NewSQLQuery(b.Model.TableName())
		cb(ctx, b, sq)
		if err := sq.Error(); err != nil {
			return "", nil, err
		}
		if err := e.runQueryCallback(ctx, b); err != nil {
			return "", nil, err
		}
		if len(sq.Clauses()) == 0 {
			return "", nil, nil
		}
		return strings.Join(sq.Clauses(), " AND "), sq.Args(), nil
	}

	table := b.Model.TableName()
	var frags []string
	var args []any
	for _, k := range sortedKeys(b.Wheres) {
		if k == "__soft_deleted" {
			continue
		}
		col, err := qualify(table, k)
		if err != nil {
			return "", nil, err
		}
		frags = append(frags, col+" = ?")
		args = append(args, b.Wheres[k])
	}
	for _, k := range sortedKeys(b.WhereIns) {
		f, a, err := inListClause(table, k, b.WhereIns[k], false)
		if err != nil {
			return "", nil, err
		}
		frags = append(frags, f)
		args = append(args, a...)
	}
	for _, k := range sortedKeys(b.WhereNotIns) {
		f, a, err := inListClause(table, k, b.WhereNotIns[k], true)
		if err != nil {
			return "", nil, err
		}
		frags = append(frags, f)
		args = append(args, a...)
	}
	if err := e.runQueryCallback(ctx, b); err != nil {
		return "", nil, err
	}
	return strings.Join(frags, " AND "), args, nil
}

func (e *DatabaseEngine) runQueryCallback(ctx context.Context, b *scout.Builder) error {
	qcb := b.GetQueryCallback()
	if qcb == nil {
		return nil
	}
	if err := qcb(ctx, b); err != nil {
		return fmt.Errorf("scout: database query callback: %w", err)
	}
	return nil
}

// softDeleteClause mirrors constrainForSoftDeletes.
func (e *DatabaseEngine) softDeleteClause(b *scout.Builder) (string, error) {
	if !scout.UsesSoftDelete(b.Model) {
		return "", nil
	}
	col, err := qualify(b.Model.TableName(), deletedAtColumn)
	if err != nil {
		return "", err
	}
	if want, ok := b.Wheres["__soft_deleted"].(int); ok {
		if want == 1 {
			return col + " IS NOT NULL", nil
		}
		return col + " IS NULL", nil
	}
	if b.Config != nil && b.Config.SoftDelete() {
		return "", nil
	}
	return col + " IS NULL", nil
}

// orderClause mirrors the orderBy chain: explicit orders win, else the key
// descending when there are no full-text columns, plus pgsql relevance.
func (e *DatabaseEngine) orderClause(b *scout.Builder) (string, []any, error) {
	table := b.Model.TableName()
	fullText := scout.FullTextColumnsOf(b.Model)
	orders := b.GetOrders()

	var parts []string
	var args []any
	for _, o := range orders {
		col, err := qualify(table, o.Column)
		if err != nil {
			return "", nil, err
		}
		dir := "DESC"
		if strings.ToLower(strings.TrimSpace(o.Direction)) == "asc" {
			dir = "ASC"
		}
		parts = append(parts, col+" "+dir)
	}
	if len(orders) == 0 && len(fullText) == 0 {
		col, err := qualify(table, scout.KeyNameOf(b.Model))
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, col+" DESC")
	}
	if e.isPgsql() && len(fullText) > 0 && len(orders) == 0 {
		rank, rankArgs, err := relevanceRank(b.Model, table, fullText, b.Query)
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, rank)
		args = append(args, rankArgs...)
	}
	return strings.Join(parts, ", "), args, nil
}

// relevanceRank builds ts_rank(to_tsvector(...) || ..., <mode>(language, ?)).
func relevanceRank(m scout.ScoutModel, table string, cols []string, query string) (string, []any, error) {
	opts := scout.FullTextOptionsOf(m)
	language := tsLanguage(opts)
	vecs := make([]string, 0, len(cols))
	for _, c := range cols {
		col, err := qualify(table, c)
		if err != nil {
			return "", nil, err
		}
		vecs = append(vecs, fmt.Sprintf("to_tsvector(%q, %s)", language, col))
	}
	out := fmt.Sprintf("ts_rank(%s, %s(%q, ?)) DESC", strings.Join(vecs, " || "), tsMode(opts), language)
	return out, []any{query}, nil
}

// BuildSQL renders the search statement for b on driver with LIMIT/OFFSET
// (negative disables each). Exported so generated SQL can be inspected or run
// without a live *sql.DB.
func BuildSQL(ctx context.Context, driver string, b *scout.Builder, limit, offset int) (string, []any, error) {
	return (&DatabaseEngine{driver: strings.ToLower(driver)}).statement(ctx, b, limit, offset, false)
}

// BuildCountSQL renders a COUNT(*) statement sharing BuildSQL's conditions.
func BuildCountSQL(ctx context.Context, driver string, b *scout.Builder) (string, []any, error) {
	return (&DatabaseEngine{driver: strings.ToLower(driver)}).statement(ctx, b, -1, -1, true)
}

// SQLQuery is handed to a database callback (Builder.Callback) so it can add
// WHERE fragments. Column names are whitelist-validated and values are always
// bound as placeholders; the engine ANDs the fragments together.
type SQLQuery struct {
	table   string
	clauses []string
	args    []any
	err     error
}

// NewSQLQuery returns a SQLQuery that qualifies bare column names with table.
func NewSQLQuery(table string) *SQLQuery { return &SQLQuery{table: table} }

// Where adds "column op ?". Supported ops: =, !=, <>, <, >, <=, >=; anything
// else is treated as =.
func (q *SQLQuery) Where(column string, op any, value any) *SQLQuery {
	if q.err != nil {
		return q
	}
	opStr := "="
	if s, ok := op.(string); ok {
		switch s = strings.ToUpper(strings.TrimSpace(s)); s {
		case "=", "!=", "<>", "<", ">", "<=", ">=":
			opStr = s
		}
	}
	col, err := qualify(q.table, column)
	if err != nil {
		q.err = err
		return q
	}
	q.clauses = append(q.clauses, col+" "+opStr+" ?")
	q.args = append(q.args, value)
	return q
}

// WhereIn adds "column IN (?...)". An empty values list never matches.
func (q *SQLQuery) WhereIn(column string, values []any) *SQLQuery {
	return q.inList(column, values, false)
}

// WhereNotIn adds "column NOT IN (?...)". An empty values list always matches.
func (q *SQLQuery) WhereNotIn(column string, values []any) *SQLQuery {
	return q.inList(column, values, true)
}

func (q *SQLQuery) inList(column string, values []any, negate bool) *SQLQuery {
	if q.err != nil {
		return q
	}
	col, err := qualify(q.table, column)
	if err != nil {
		q.err = err
		return q
	}
	if len(values) == 0 {
		if negate {
			q.clauses = append(q.clauses, "1 = 1")
		} else {
			q.clauses = append(q.clauses, "0 = 1")
		}
		return q
	}
	op := "IN"
	if negate {
		op = "NOT IN"
	}
	q.clauses = append(q.clauses, col+" "+op+" ("+placeholders(len(values))+")")
	q.args = append(q.args, values...)
	return q
}

// RawSQL appends a verbatim fragment. It bypasses validation by design and must
// only be called with trusted, non-user SQL.
func (q *SQLQuery) RawSQL(fragment string, args ...any) *SQLQuery {
	if q.err != nil {
		return q
	}
	q.clauses = append(q.clauses, fragment)
	q.args = append(q.args, args...)
	return q
}

// Error returns the first validation error, if any.
func (q *SQLQuery) Error() error { return q.err }

// Clauses returns the accumulated fragments.
func (q *SQLQuery) Clauses() []string { return q.clauses }

// Args returns the bound values, in clause order.
func (q *SQLQuery) Args() []any { return q.args }

// ident rejects anything outside the identifier whitelist.
func ident(name string) (string, error) {
	if name == "" || !identRe.MatchString(name) {
		return "", fmt.Errorf("scout: database engine: invalid identifier %q", name)
	}
	return name, nil
}

// qualify validates column (or table.column) and prefixes the table when the
// caller gave a bare name.
func qualify(table, column string) (string, error) {
	col, err := ident(column)
	if err != nil {
		return "", err
	}
	if strings.Contains(column, ".") {
		return col, nil
	}
	tbl, err := ident(table)
	if err != nil {
		return "", err
	}
	return tbl + "." + col, nil
}

func inListClause(table, column string, values []any, negate bool) (string, []any, error) {
	col, err := qualify(table, column)
	if err != nil {
		return "", nil, err
	}
	if len(values) == 0 {
		if negate {
			return "1 = 1", nil, nil
		}
		return "0 = 1", nil, nil
	}
	op := "IN"
	if negate {
		op = "NOT IN"
	}
	return col + " " + op + " (" + placeholders(len(values)) + ")", values, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(vals []string, v string) bool {
	for _, s := range vals {
		if s == v {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (e *DatabaseEngine) isPgsql() bool {
	switch e.driver {
	case "pgsql", "postgres", "postgresql":
		return true
	}
	return false
}

func tsLanguage(opts map[string]any) string {
	if l, ok := opts["language"].(string); ok && languageRe.MatchString(l) {
		return l
	}
	return "english"
}

func tsMode(opts map[string]any) string {
	switch m, _ := opts["mode"].(string); m {
	case "phrase":
		return "phraseto_tsquery"
	case "websearch":
		return "websearch_to_tsquery"
	default:
		return "plainto_tsquery"
	}
}
