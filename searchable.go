package scout

import (
	"context"
	"fmt"
	"sort"
)

// Searchable drives one model type through the search index: building queries,
// indexing records and removing them. It mirrors scout's Searchable trait.
type Searchable struct {
	model    ScoutModel
	source   Source[ScoutModel]
	engine   Engine
	cfg      *Config
	queue    *Queue
	events   *EventBus
	observer *ModelObserver
}

// NewSearchable builds a Searchable with a fresh queue and event bus.
func NewSearchable(model ScoutModel, source Source[ScoutModel], engine Engine, cfg *Config) *Searchable {
	return NewSearchableWith(model, source, engine, cfg, NewQueue(cfg.Queue()), NewEventBus())
}

// NewSearchableWith builds a Searchable with an injected queue and event bus,
// for tests or when one bus must be shared across model types.
func NewSearchableWith(model ScoutModel, source Source[ScoutModel], engine Engine, cfg *Config, q *Queue, events *EventBus) *Searchable {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if q == nil {
		q = NewQueue(cfg.Queue())
	}
	if events == nil {
		events = NewEventBus()
	}
	s := &Searchable{model: model, source: source, engine: engine, cfg: cfg, queue: q, events: events}
	// Observer callbacks use the queued paths, as scout's ModelObserver does
	// by calling $model->searchable().
	s.observer = NewModelObserver(cfg,
		func(ctx context.Context, m ScoutModel) error { return s.Searchable(ctx, m) },
		func(ctx context.Context, m ScoutModel) error { return s.Unsearchable(ctx, m) },
	)
	return s
}

// --- accessors ---

// Model returns the prototype instance of the searched type.
func (s *Searchable) Model() ScoutModel { return s.model }

// Source returns the record store backing the model type.
func (s *Searchable) Source() Source[ScoutModel] { return s.source }

// Engine returns the search backend.
func (s *Searchable) Engine() Engine { return s.engine }

// Config returns the active configuration.
func (s *Searchable) Config() *Config { return s.cfg }

// Observer returns the model observer.
func (s *Searchable) Observer() *ModelObserver { return s.observer }

// Queue returns the dispatch queue.
func (s *Searchable) Queue() *Queue { return s.queue }

// Events returns the event bus.
func (s *Searchable) Events() *EventBus { return s.events }

// Search returns a Builder for the model's index, mirroring Scout::search.
// softDelete is enabled only when the model is soft-deletable and the
// soft_delete config is on.
func (s *Searchable) Search(ctx context.Context, query string, cb func(context.Context, *Builder, any) any) *Builder {
	softDelete := UsesSoftDelete(s.model) && s.cfg.SoftDelete()
	return NewBuilder(s.model, s.source, s.engine, s.cfg, query, cb, softDelete)
}

// --- indexing, single record ---

// SearchableSync indexes one record synchronously.
func (s *Searchable) SearchableSync(ctx context.Context, model ScoutModel) error {
	return s.MakeSearchableSync(ctx, model)
}

// Searchable indexes one record, dispatched through the queue when enabled.
func (s *Searchable) Searchable(ctx context.Context, model ScoutModel) error {
	if !s.cfg.Queue() {
		return s.SearchableSync(ctx, model)
	}
	return s.queue.PushNamed("scout_make", func() error { return s.SearchableSync(ctx, model) })
}

// UnsearchableSync removes one record from the index synchronously.
func (s *Searchable) UnsearchableSync(ctx context.Context, model ScoutModel) error {
	return s.RemoveFromSearchSync(ctx, model)
}

// Unsearchable removes one record, dispatched through the queue when enabled.
func (s *Searchable) Unsearchable(ctx context.Context, model ScoutModel) error {
	return s.RemoveFromSearch(ctx, model)
}

// --- indexing, batches ---

// MakeSearchableSync indexes every record, applying the model's
// MakeSearchableUsing filter first (mirrors syncMakeSearchable).
func (s *Searchable) MakeSearchableSync(ctx context.Context, models ...ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	filtered := MakeSearchableUsing(models[0], models)
	if len(filtered) == 0 {
		return nil
	}
	if err := s.engine.Update(ctx, filtered); err != nil {
		return fmt.Errorf("scout: update index: %w", err)
	}
	return nil
}

// MakeSearchable indexes every record, dispatched through the queue when
// enabled (mirrors queueMakeSearchable).
func (s *Searchable) MakeSearchable(ctx context.Context, models ...ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	if !s.cfg.Queue() {
		return s.MakeSearchableSync(ctx, models...)
	}
	return s.queue.PushNamed("scout_make", func() error { return s.MakeSearchableSync(ctx, models...) })
}

// RemoveFromSearchSync deletes records from the index synchronously.
func (s *Searchable) RemoveFromSearchSync(ctx context.Context, models ...ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	if err := s.engine.Delete(ctx, models); err != nil {
		return fmt.Errorf("scout: delete index: %w", err)
	}
	return nil
}

// RemoveFromSearch deletes records, dispatched through the queue when enabled.
func (s *Searchable) RemoveFromSearch(ctx context.Context, models ...ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	if !s.cfg.Queue() {
		return s.RemoveFromSearchSync(ctx, models...)
	}
	return s.queue.PushNamed("scout_remove", func() error { return s.RemoveFromSearchSync(ctx, models...) })
}

// --- bulk import / flush ---

// MakeAllSearchableQuery returns every record ordered by primary key,
// mirroring makeAllSearchableQuery (withTrashed + orderByKey).
func (s *Searchable) MakeAllSearchableQuery(ctx context.Context) ([]ScoutModel, error) {
	if s.source == nil {
		return nil, fmt.Errorf("%w: search source not set", ErrScout)
	}
	models, err := s.source.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("scout: load records: %w", err)
	}
	sort.SliceStable(models, func(i, j int) bool {
		return keyLess(models[i].ScoutKey(), models[j].ScoutKey())
	})
	return models, nil
}

// MakeAllSearchable indexes every record in chunks of chunk (<= 0 uses
// chunk.searchable), filtering each chunk to searchable records and publishing
// ModelsImported with the chunk. Mirrors makeAllSearchable.
func (s *Searchable) MakeAllSearchable(ctx context.Context, chunk int) error {
	if chunk <= 0 {
		chunk = s.cfg.ChunkSearchable()
	}
	if chunk <= 0 {
		chunk = 500
	}
	models, err := s.MakeAllSearchableQuery(ctx)
	if err != nil {
		return err
	}
	for start := 0; start < len(models); start += chunk {
		end := start + chunk
		if end > len(models) {
			end = len(models)
		}
		batch := models[start:end]

		searchable := make([]ScoutModel, 0, len(batch))
		for _, m := range batch {
			if m.ShouldBeSearchable() {
				searchable = append(searchable, m)
			}
		}
		if err := s.MakeSearchable(ctx, searchable...); err != nil {
			return fmt.Errorf("scout: import chunk from key %v: %w", batch[0].ScoutKey(), err)
		}
		s.events.Publish(EventModelsImported, &ModelsImported{Models: batch})
	}
	return nil
}

// RemoveAllFromSearch flushes every record of the model from the index.
func (s *Searchable) RemoveAllFromSearch(ctx context.Context) error {
	if err := s.engine.Flush(ctx, s.model); err != nil {
		return fmt.Errorf("scout: flush index: %w", err)
	}
	s.events.Publish(EventModelsFlushed, &ModelsFlushed{Models: nil})
	return nil
}

// --- sync control ---

// WithoutSyncingToSearch runs fn with index sync disabled for the model type,
// re-enabling it afterwards (mirrors withoutSyncingToSearch's try/finally).
func (s *Searchable) WithoutSyncingToSearch(fn func() error) error {
	class := classOf(s.model)
	s.observer.DisableSyncingFor(class)
	defer s.observer.EnableSyncingFor(class)
	return fn()
}

// EnableSearchSyncing re-enables index sync for the model type.
func (s *Searchable) EnableSearchSyncing() { s.observer.EnableSyncingFor(classOf(s.model)) }

// DisableSearchSyncing disables index sync for the model type.
func (s *Searchable) DisableSearchSyncing() { s.observer.DisableSyncingFor(classOf(s.model)) }

// --- metadata ---

// PushSoftDeleteMetadata returns the index document with __soft_deleted (0/1)
// merged in, mirroring pushSoftDeleteMetadata / withScoutMetadata.
func (s *Searchable) PushSoftDeleteMetadata(ctx context.Context, model ScoutModel) map[string]any {
	doc := model.ToSearchableArray()
	if doc == nil {
		doc = map[string]any{}
	}
	if sd, ok := model.(SoftDeleter); ok && sd.Trashed() {
		doc["__soft_deleted"] = 1
	} else {
		doc["__soft_deleted"] = 0
	}
	return doc
}

// --- key ordering helpers ---

// ponytail: comparator handles the integer and string keys models actually
// use; anything else falls back to KeyString ordering.
func keyLess(a, b any) bool {
	an, aok := toNumber(a)
	bn, bok := toNumber(b)
	if aok && bok {
		return an < bn
	}
	if aok != bok {
		return aok
	}
	return KeyString(a) < KeyString(b)
}

func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
