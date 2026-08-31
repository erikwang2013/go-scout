package scout

import (
	"context"
	"reflect"
	"sync"
)

// ModelObserver decides, per model event, whether a record is pushed to or
// pulled from the search index. It mirrors scout's ModelObserver, including the
// forceSaving bypass and the soft-delete branch in Deleted.
type ModelObserver struct {
	cfg         *Config
	index       func(context.Context, ScoutModel) error
	unindex     func(context.Context, ScoutModel) error
	mu          sync.Mutex
	forceSaving bool
	disabled    map[string]bool
}

// NewModelObserver builds an observer. index/unindex are called to sync a
// record; nil callbacks are no-ops.
func NewModelObserver(cfg *Config, index, unindex func(context.Context, ScoutModel) error) *ModelObserver {
	if index == nil {
		index = func(context.Context, ScoutModel) error { return nil }
	}
	if unindex == nil {
		unindex = func(context.Context, ScoutModel) error { return nil }
	}
	return &ModelObserver{cfg: cfg, index: index, unindex: unindex, disabled: map[string]bool{}}
}

// classOf returns the reflect type string of m, used as the sync-disabling key
// (the Go counterpart of get_called_class / get_class).
func classOf(m ScoutModel) string {
	if m == nil {
		return ""
	}
	return reflect.TypeOf(m).String()
}

// EnableSyncingFor turns index sync back on for class.
func (o *ModelObserver) EnableSyncingFor(class string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.disabled, class)
}

// DisableSyncingFor stops the observer from syncing class.
func (o *ModelObserver) DisableSyncingFor(class string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.disabled[class] = true
}

// SyncingDisabledFor reports whether sync is off for class.
func (o *ModelObserver) SyncingDisabledFor(class string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.disabled[class]
}

// WhileForcingUpdate runs fn with forceSaving set, so Saved ignores
// searchIndexShouldBeUpdated. Errors are returned to the caller.
func (o *ModelObserver) WhileForcingUpdate(fn func() error) error {
	o.mu.Lock()
	o.forceSaving = true
	o.mu.Unlock()
	err := fn()
	o.mu.Lock()
	o.forceSaving = false
	o.mu.Unlock()
	return err
}

func (o *ModelObserver) isForceSaving() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.forceSaving
}

// Saved syncs the record after a create or update. A record that is no longer
// searchable is removed when it was indexed before the update.
func (o *ModelObserver) Saved(ctx context.Context, model ScoutModel) error {
	if o.SyncingDisabledFor(classOf(model)) {
		return nil
	}
	if !o.isForceSaving() && !SearchIndexShouldBeUpdated(model) {
		return nil
	}
	if !model.ShouldBeSearchable() {
		if WasSearchableBeforeUpdate(model) {
			return o.unindex(ctx, model)
		}
		return nil
	}
	return o.index(ctx, model)
}

// Deleted handles a soft or hard delete: soft-deletable models are re-indexed
// (with their trashed state) when soft_delete is enabled, otherwise removed.
func (o *ModelObserver) Deleted(ctx context.Context, model ScoutModel) error {
	if o.SyncingDisabledFor(classOf(model)) {
		return nil
	}
	if !WasSearchableBeforeDelete(model) {
		return nil
	}
	if o.cfg != nil && o.cfg.SoftDelete() && UsesSoftDelete(model) {
		return o.WhileForcingUpdate(func() error { return o.Saved(ctx, model) })
	}
	return o.unindex(ctx, model)
}

// ForceDeleted always removes the record from the index.
func (o *ModelObserver) ForceDeleted(ctx context.Context, model ScoutModel) error {
	if o.SyncingDisabledFor(classOf(model)) {
		return nil
	}
	return o.unindex(ctx, model)
}

// Restored re-indexes a restored soft-deleted record, bypassing the
// searchIndexShouldBeUpdated gate.
func (o *ModelObserver) Restored(ctx context.Context, model ScoutModel) error {
	return o.WhileForcingUpdate(func() error { return o.Saved(ctx, model) })
}
