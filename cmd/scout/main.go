// Command scout exercises the go-scout pipeline end to end against the
// in-memory collection engine: create an index, import records in chunks
// (directly or via queued key ranges), sync settings, flush and delete.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/erikwang2013/go-scout"
	"github.com/erikwang2013/go-scout/engines"
)

// post is the demo model. It implements SoftDeleter so the soft-delete
// metadata path is exercisable.
type post struct {
	ID        int
	Title     string
	Body      string
	Tags      []string
	Price     float64
	CreatedAt time.Time
	TrashedAt *time.Time
}

func (p post) ScoutKey() any            { return p.ID }
func (p post) TableName() string        { return "posts" }
func (p post) ShouldBeSearchable() bool { return p.Title != "" }
func (p post) Trashed() bool            { return p.TrashedAt != nil }
func (p post) DeletedAt() *time.Time    { return p.TrashedAt }

func (p post) ToSearchableArray() map[string]any {
	return map[string]any{
		"title":      p.Title,
		"body":       p.Body,
		"tags":       p.Tags,
		"price":      p.Price,
		"created_at": p.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// demoPosts seeds the store. The blank title shows ShouldBeSearchable
// filtering: that record is skipped during import.
func demoPosts() []post {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	return []post{
		{ID: 1, Title: "Go module layout", Body: "When to split packages versus subpackages.", Tags: []string{"go", "tooling"}, Price: 19, CreatedAt: day},
		{ID: 2, Title: "Indexing strategies", Body: "Inverted indexes, ngrams and hybrid search.", Tags: []string{"search", "database"}, Price: 39, CreatedAt: day.Add(2 * 24 * time.Hour)},
		{ID: 3, Title: "Full text search engines", Body: "Comparing Meilisearch, Typesense and OpenSearch.", Tags: []string{"search", "opensearch"}, Price: 49, CreatedAt: day.Add(5 * 24 * time.Hour)},
		{ID: 4, Title: "", Body: "Draft with no title, never indexed.", Tags: []string{"draft"}, Price: 0, CreatedAt: day.Add(6 * 24 * time.Hour)},
		{ID: 5, Title: "Vector similarity search", Body: "KNN, HNSW and embedding pipelines.", Tags: []string{"search", "vector"}, Price: 59, CreatedAt: day.Add(9 * 24 * time.Hour)},
		{ID: 6, Title: "Soft deletes in search", Body: "Keeping trashed records out of results.", Tags: []string{"database", "search"}, Price: 29, CreatedAt: day.Add(12 * 24 * time.Hour)},
		{ID: 7, Title: "Chunked imports", Body: "Streaming large datasets through a queue.", Tags: []string{"tooling", "queue"}, Price: 34, CreatedAt: day.Add(20 * 24 * time.Hour)},
	}
}

// indexSettingsSyncer mirrors scout's UpdatesIndexSettings contract.
type indexSettingsSyncer interface {
	UpdateIndexSettings(ctx context.Context, name string, settings map[string]any) error
}

// allIndexesDeleter mirrors scout's method_exists($engine, 'deleteAllIndexes').
type allIndexesDeleter interface {
	DeleteAllIndexes(ctx context.Context) error
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	s, model, src := bootstrap()
	args := os.Args[2:]
	ctx := context.Background()
	switch os.Args[1] {
	case "import":
		doImport(ctx, s, model, src, args)
	case "flush":
		doFlush(ctx, s, model, src, args)
	case "index":
		doIndex(ctx, s, args)
	case "delete-index":
		doDeleteIndex(ctx, s, args)
	case "queue-import":
		doQueueImport(ctx, s, model, src, args)
	case "sync-index-settings":
		doSyncIndexSettings(ctx, s, args)
	case "delete-all-indexes":
		doDeleteAllIndexes(ctx, s)
	default:
		usage()
		os.Exit(2)
	}
}

// bootstrap builds a Scout wired to the collection engine and a seeded store.
// ponytail: Config reads the driver from SCOUT_DRIVER at construction time, so
// the demo sets the env var rather than needing a Config setter.
func bootstrap() (*scout.Scout, scout.ScoutModel, *scout.MemorySource) {
	os.Setenv("SCOUT_DRIVER", "collection")
	s := scout.NewWithConfig(scout.DefaultConfig())
	engines.Register(s.Manager)
	posts := demoPosts()
	return s, post{}, scout.NewMemorySource(postsAsModels(posts)...)
}

// postsAsModels adapts the concrete demo records to the ScoutModel interface.
func postsAsModels(posts []post) []scout.ScoutModel {
	out := make([]scout.ScoutModel, len(posts))
	for i, p := range posts {
		out[i] = p
	}
	return out
}

func doImport(ctx context.Context, s *scout.Scout, model scout.ScoutModel, src *scout.MemorySource, args []string) {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	chunk := fs.Int("chunk", 0, "records per chunk (0 = scout.chunk.searchable)")
	fresh := fs.Bool("fresh", false, "flush the index before importing")
	fs.Parse(args)
	_ = fs.Arg(0) // model class; the demo ships one model type

	sr := s.Searchable(model, src)
	sr.Events().Subscribe(scout.EventModelsImported, func(ev any) {
		mi, ok := ev.(*scout.ModelsImported)
		if !ok || len(mi.Models) == 0 {
			return
		}
		fmt.Printf("Imported [post] models up to ID: %v\n", mi.Models[len(mi.Models)-1].ScoutKey())
	})
	if *fresh {
		if err := sr.RemoveAllFromSearch(ctx); err != nil {
			fatal(err)
		}
		fmt.Printf("Flushed index %q before import.\n", s.Config.Prefix()+model.TableName())
	}
	if err := sr.MakeAllSearchable(ctx, *chunk); err != nil {
		fatal(err)
	}
	if n, err := src.Count(ctx); err == nil {
		fmt.Printf("All [%d] [post] records have been imported (1 skipped, not searchable).\n", n)
	}
}

func doFlush(ctx context.Context, s *scout.Scout, model scout.ScoutModel, src *scout.MemorySource, args []string) {
	fs := flag.NewFlagSet("flush", flag.ContinueOnError)
	fs.Parse(args)
	_ = fs.Arg(0)
	sr := s.Searchable(model, src)
	if err := sr.RemoveAllFromSearch(ctx); err != nil {
		fatal(err)
	}
	fmt.Printf("All [post] records have been flushed from index %q.\n", s.Config.Prefix()+model.TableName())
}

func doIndex(ctx context.Context, s *scout.Scout, args []string) {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	key := fs.String("key", "", "primary key column name")
	fs.Parse(args)
	name := fs.Arg(0)
	if name == "" {
		fmt.Fprintln(os.Stderr, "index: missing index name")
		os.Exit(2)
	}
	engine, err := s.DefaultEngine()
	if err != nil {
		fatal(err)
	}
	options := map[string]any{}
	if *key != "" {
		options["primaryKey"] = *key
	}
	full := s.Config.Prefix() + name
	if _, err := engine.CreateIndex(ctx, full, options); err != nil {
		if scout.IsNotSupported(err) {
			fmt.Printf("The [%s] engine does not support index creation.\n", engine.Name())
			return
		}
		fatal(err)
	}
	fmt.Printf("Index [%q] created successfully.\n", full)
}

func doDeleteIndex(ctx context.Context, s *scout.Scout, args []string) {
	fs := flag.NewFlagSet("delete-index", flag.ContinueOnError)
	fs.Parse(args)
	name := fs.Arg(0)
	if name == "" {
		fmt.Fprintln(os.Stderr, "delete-index: missing index name")
		os.Exit(2)
	}
	engine, err := s.DefaultEngine()
	if err != nil {
		fatal(err)
	}
	full := s.Config.Prefix() + name
	if _, err := engine.DeleteIndex(ctx, full); err != nil {
		if scout.IsNotSupported(err) {
			fmt.Printf("The [%s] engine does not support index deletion.\n", engine.Name())
			return
		}
		fatal(err)
	}
	fmt.Printf("Index %q deleted.\n", full)
}

func doQueueImport(ctx context.Context, s *scout.Scout, model scout.ScoutModel, src *scout.MemorySource, args []string) {
	fs := flag.NewFlagSet("queue-import", flag.ContinueOnError)
	chunk := fs.Int("chunk", 0, "records per queued range (0 = scout.chunk.searchable)")
	minKey := fs.String("min", "", "lowest primary key (default: lowest)")
	maxKey := fs.String("max", "", "highest primary key (default: highest)")
	workers := fs.Int("workers", 2, "in-process queue workers")
	fs.Parse(args)
	_ = fs.Arg(0)

	sr := s.Searchable(model, src)
	models, err := sr.MakeAllSearchableQuery(ctx)
	if err != nil {
		fatal(err)
	}
	if len(models) == 0 {
		fmt.Println("No records found for [post].")
		return
	}
	keys := intKeys(models)
	lo, hi := keys[0], keys[len(keys)-1]
	if *minKey != "" {
		if v, err := strconv.Atoi(*minKey); err == nil {
			lo = v
		}
	}
	if *maxKey != "" {
		if v, err := strconv.Atoi(*maxKey); err == nil {
			hi = v
		}
	}
	size := *chunk
	if size <= 0 {
		size = s.Config.ChunkSearchable()
	}
	if size <= 0 {
		size = 500
	}

	q := sr.Queue()
	q.Start(*workers)
	defer q.Stop()
	for start := lo; start <= hi; start += size {
		end := start + size - 1
		if end > hi {
			end = hi
		}
		fmt.Printf("Queued [post] models up to ID: %d\n", end)
		if err := q.PushNamed("scout_make_range", rangeJob(sr, models, start, end)); err != nil {
			fatal(err)
		}
	}
	q.Stop()
	fmt.Println("All [post] records have been queued for importing.")
}

// rangeJob returns a queued job that indexes the records whose key lies in
// [lo, hi], mirroring the PHP scout_make_range range job.
func rangeJob(sr *scout.Searchable, models []scout.ScoutModel, lo, hi int) func() error {
	batch := make([]scout.ScoutModel, 0, hi-lo+1)
	for _, m := range models {
		if k, err := strconv.Atoi(scout.KeyString(m.ScoutKey())); err == nil && k >= lo && k <= hi {
			batch = append(batch, m)
		}
	}
	return func() error {
		if err := sr.MakeSearchableSync(context.Background(), batch...); err != nil {
			return err
		}
		sr.Events().Publish(scout.EventModelsImported, &scout.ModelsImported{Models: batch})
		return nil
	}
}

func doSyncIndexSettings(ctx context.Context, s *scout.Scout, args []string) {
	fs := flag.NewFlagSet("sync-index-settings", flag.ContinueOnError)
	driver := fs.String("driver", "", "driver to sync (default: configured default)")
	fs.Parse(args)
	if *driver == "" {
		*driver = s.Config.Driver()
	}
	engine, err := s.Engine(*driver)
	if err != nil {
		fatal(err)
	}
	syncer, ok := engine.(indexSettingsSyncer)
	if !ok {
		fmt.Printf("The %q engine does not support updating index settings.\n", *driver)
		return
	}
	indexes := s.Config.Map(*driver + ".index-settings")
	if len(indexes) == 0 {
		fmt.Printf("No index settings found for the %q engine.\n", *driver)
		return
	}
	for name, v := range indexes {
		settings := map[string]any{}
		switch s := v.(type) {
		case map[string]any:
			settings = s
		case string:
			name = s // PHP allows a class name as the value
		}
		full := s.Config.Prefix() + name
		if err := syncer.UpdateIndexSettings(ctx, full, settings); err != nil {
			fatal(err)
		}
		fmt.Printf("Settings for the [%s] index synced successfully.\n", full)
	}
}

func doDeleteAllIndexes(ctx context.Context, s *scout.Scout) {
	engine, err := s.DefaultEngine()
	if err != nil {
		fatal(err)
	}
	del, ok := engine.(allIndexesDeleter)
	if !ok {
		fmt.Printf("The [%s] engine does not support deleting all indexes.\n", s.Config.Driver())
		return
	}
	if err := del.DeleteAllIndexes(ctx); err != nil {
		fatal(err)
	}
	fmt.Println("All indexes deleted successfully.")
}

// intKeys renders the model keys as ints; MakeAllSearchableQuery returns them
// sorted ascending, so first/last are the min/max range.
func intKeys(models []scout.ScoutModel) []int {
	out := make([]int, len(models))
	for i, m := range models {
		out[i], _ = strconv.Atoi(scout.KeyString(m.ScoutKey()))
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `go-scout v`+scout.Version+`

Usage: scout <command> [args]

Commands:
  import [model] [--chunk N] [--fresh]       import every record into the index
  flush [model]                              remove every record from the index
  index <name> [--key <column>]              create a search index
  delete-index <name>                        delete a search index
  queue-import [model] [--chunk N] [--min N] [--max N] [--workers N]
                                             import key ranges via queued jobs
  sync-index-settings [--driver <name>]      sync configured index settings
  delete-all-indexes                         drop every index

The demo runs the in-memory "collection" engine over MemorySource seeded with
7 posts. Try:
  scout index posts --key id
  scout import --chunk 3 --fresh
  scout flush`)
}
