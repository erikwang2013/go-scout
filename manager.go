package scout

import (
	"context"
	"fmt"
	"maps"
	"sync"
)

// Manager resolves driver names to Engine instances, caching created engines.
// It mirrors scout's abstract Manager.
type Manager struct {
	mu        sync.RWMutex
	cfg       *Config
	factories map[string]Factory
	drivers   map[string]Engine
}

// NewManager returns a Manager with no registered drivers.
func NewManager(cfg *Config) *Manager {
	return &Manager{
		cfg:       cfg,
		factories: map[string]Factory{},
		drivers:   map[string]Engine{},
	}
}

// SetConfig replaces the configuration used by driver factories.
func (m *Manager) SetConfig(cfg *Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
}

// DefaultDriver returns the configured driver name ("null" when unset).
func (m *Manager) DefaultDriver() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultDriverLocked()
}

func (m *Manager) defaultDriverLocked() string {
	if m.cfg == nil {
		return "null"
	}
	return m.cfg.Driver()
}

// Driver returns the engine for name, creating and caching it on first use.
// An empty name resolves to the configured default driver.
func (m *Manager) Driver(name string) (Engine, error) {
	m.mu.RLock()
	if name == "" {
		name = m.defaultDriverLocked()
	}
	if e, ok := m.drivers[name]; ok {
		m.mu.RUnlock()
		return e, nil
	}
	m.mu.RUnlock()

	e, err := m.createDriver(name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.drivers[name] = e
	m.mu.Unlock()
	return e, nil
}

// Extend registers a factory under name. A later Extend wins, and any cached
// engine for name is discarded so the new factory takes effect.
//
// ponytail: a single factories map stands in for scout's customCreators map
// plus its create<Studly>Driver method dispatch. Register all drivers through
// Extend; engines.Register fills this map for the shipped backends.
func (m *Manager) Extend(name string, f Factory) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factories[name] = f
	delete(m.drivers, name)
	return m
}

// ForgetDrivers drops every cached engine instance.
func (m *Manager) ForgetDrivers() *Manager {
	m.mu.Lock()
	m.drivers = map[string]Engine{}
	m.mu.Unlock()
	return m
}

// Drivers returns a copy of the engines created so far.
func (m *Manager) Drivers() map[string]Engine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return maps.Clone(m.drivers)
}

// createDriver looks name up in the registry and builds it.
func (m *Manager) createDriver(name string) (Engine, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: unable to resolve empty driver name", ErrScout)
	}
	m.mu.RLock()
	f, ok := m.factories[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: driver [%s] not supported", ErrNotSupported, name)
	}
	e := f(m.cfg)
	if e == nil {
		return nil, fmt.Errorf("%w: driver [%s] factory returned nil", ErrScout, name)
	}
	return e, nil
}

// EngineManager is Scout's concrete Manager: it resolves search drivers.
type EngineManager struct{ mgr *Manager }

// NewEngineManager returns an EngineManager. A built-in "null" engine is
// registered as a fallback so Scout works before engines.Register runs.
func NewEngineManager(cfg *Config) *EngineManager {
	em := &EngineManager{mgr: NewManager(cfg)}
	em.Extend("null", func(*Config) Engine { return defaultNullEngine{} })
	return em
}

// Engine returns the named engine ("" = configured default).
func (m *EngineManager) Engine(name string) (Engine, error) { return m.mgr.Driver(name) }

// Extend registers a driver factory on the underlying manager.
func (m *EngineManager) Extend(name string, f Factory) *EngineManager {
	m.mgr.Extend(name, f)
	return m
}

// ForgetEngines drops every cached engine instance.
func (m *EngineManager) ForgetEngines() *EngineManager {
	m.mgr.ForgetDrivers()
	return m
}

// DefaultDriver returns the configured default driver name.
func (m *EngineManager) DefaultDriver() string { return m.mgr.DefaultDriver() }

// Manager exposes the underlying Manager.
func (m *EngineManager) Manager() *Manager { return m.mgr }

// defaultNullEngine is the built-in fallback engine: searches return empty
// results and writes are dropped. Mirrors scout's Engines\NullEngine.
type defaultNullEngine struct{}

func (defaultNullEngine) Name() string { return "null" }

func (defaultNullEngine) Update(context.Context, []ScoutModel) error { return nil }
func (defaultNullEngine) Delete(context.Context, []ScoutModel) error { return nil }
func (defaultNullEngine) Flush(context.Context, ScoutModel) error    { return nil }
func (defaultNullEngine) CreateIndex(context.Context, string, map[string]any) (any, error) {
	return nil, nil
}
func (defaultNullEngine) DeleteIndex(context.Context, string) (any, error) { return nil, nil }

func (defaultNullEngine) Search(context.Context, *Builder) (*Result, error) {
	return &Result{Hits: []Hit{}}, nil
}
func (defaultNullEngine) Paginate(context.Context, *Builder, int, int) (*Result, error) {
	return &Result{Hits: []Hit{}}, nil
}
func (defaultNullEngine) MapIDs(*Result) []any { return nil }
func (defaultNullEngine) Map(context.Context, *Builder, *Result) ([]ScoutModel, error) {
	return nil, nil
}
func (defaultNullEngine) GetTotalCount(*Result) int { return 0 }
