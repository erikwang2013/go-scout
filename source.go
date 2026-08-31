package scout

import (
	"sync"
)

// MemorySource is an in-memory Source[ScoutModel] backed by a slice, standing in
// for a database or ORM during demos and tests. Records are returned in
// insertion order; ByIDs returns them in the order the ids were requested.
type MemorySource struct {
	mu     sync.RWMutex
	models []ScoutModel
}

// NewMemorySource returns a MemorySource holding the given records.
func NewMemorySource(records ...ScoutModel) *MemorySource {
	return &MemorySource{models: append([]ScoutModel(nil), records...)}
}

// Add appends records to the store.
func (s *MemorySource) Add(records ...ScoutModel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = append(s.models, records...)
}

// Remove drops every record whose primary key appears in keys.
func (s *MemorySource) Remove(keys ...any) {
	if len(keys) == 0 {
		return
	}
	want := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		want[KeyString(k)] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.models[:0]
	for _, m := range s.models {
		if _, drop := want[KeyString(m.ScoutKey())]; !drop {
			kept = append(kept, m)
		}
	}
	s.models = kept
}

// All returns every record in the store (a copy, safe to mutate).
func (s *MemorySource) All(ctx any) ([]ScoutModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]ScoutModel(nil), s.models...), nil
}

// ByIDs returns the records whose key matches one of ids, in id order.
// ponytail: linear scan per id (O(ids*models)); add a key index if hydration
// dominates the profile on large stores.
func (s *MemorySource) ByIDs(ctx any, ids []any) ([]ScoutModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ScoutModel, 0, len(ids))
	for _, id := range ids {
		want := KeyString(id)
		for _, m := range s.models {
			if KeyString(m.ScoutKey()) == want {
				out = append(out, m)
				break
			}
		}
	}
	return out, nil
}

// Count returns the number of records in the store.
func (s *MemorySource) Count(ctx any) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.models), nil
}
