package scout

// Version is the go-scout module version.
const Version = "1.4.0"

// Scout wires the configuration, engine registry, event bus and queue together.
// It is the Go counterpart of scout's Scout class plus its container bindings.
type Scout struct {
	// Config is the active configuration tree.
	Config *Config
	// Manager resolves driver names to engines.
	Manager *EngineManager
	// Events is the bus for pipeline events (imports, flushes).
	Events *EventBus
	// Queue dispatches index writes.
	Queue *Queue
	// Observer toggles sync per model type. It carries no index callback, so
	// wiring one through Searchable is what makes saves hit the index.
	Observer *ModelObserver
}

// New builds a Scout with the default configuration and the built-in engines.
func New() *Scout { return NewWithConfig(DefaultConfig()) }

// NewWithConfig builds a Scout from cfg.
func NewWithConfig(cfg *Config) *Scout {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Scout{
		Config:   cfg,
		Manager:  NewEngineManager(cfg),
		Events:   NewEventBus(),
		Queue:    NewQueue(cfg.Queue()),
		Observer: NewModelObserver(cfg, nil, nil),
	}
}

// Engine returns the named engine ("" = configured default).
func (s *Scout) Engine(name string) (Engine, error) { return s.Manager.Engine(name) }

// DefaultEngine returns the configured default engine.
func (s *Scout) DefaultEngine() (Engine, error) { return s.Engine("") }

// Register runs fn with the engine manager, wiring driver factories in.
func (s *Scout) Register(fn func(*EngineManager)) { fn(s.Manager) }

// Searchable returns the Searchable API for the model type, using the
// configured default engine. A missing driver falls back to the null engine so
// the pipeline still works; use SearchableUsing for an explicit engine.
func (s *Scout) Searchable(model ScoutModel, source Source[ScoutModel]) *Searchable {
	e, err := s.DefaultEngine()
	if err != nil {
		e = defaultNullEngine{}
	}
	return NewSearchable(model, source, e, s.Config)
}

// SearchableUsing returns the Searchable API for the model type against a
// specific engine, mirroring scout's searchableUsing.
func (s *Scout) SearchableUsing(model ScoutModel, source Source[ScoutModel], engine Engine) *Searchable {
	return NewSearchable(model, source, engine, s.Config)
}
