package scout

import (
	"os"
	"strconv"
	"strings"
)

// Config is the Scout configuration tree. It mirrors the PHP plugin's
// config/plugin/.../app.php array, loaded with env overrides and a sensible
// default driver. Get resolves dot paths ("opensearch.host") for nested settings.
type Config struct {
	// raw holds the nested configuration tree.
	raw map[string]any
	// prefix is the index name prefix (config key "prefix").
	prefix string
}

// DefaultConfig builds the default configuration tree, mirroring the PHP app.php
// defaults with env overrides (SCOUT_DRIVER, SCOUT_PREFIX, SCOUT_QUEUE, ...).
func DefaultConfig() *Config {
	driver := envOr("SCOUT_DRIVER", "database")
	if driver == "" || driver == "false" {
		driver = "null"
	}

	cfg := &Config{
		prefix: envOr("SCOUT_PREFIX", ""),
		raw: map[string]any{
			"enable":       true,
			"driver":       driver,
			"prefix":       envOr("SCOUT_PREFIX", ""),
			"queue":        envBool("SCOUT_QUEUE"),
			"after_commit": false,
			"chunk": map[string]any{
				"searchable":   envInt("SCOUT_CHUNK_SEARCHABLE", 500),
				"unsearchable": envInt("SCOUT_CHUNK_UNSEARCHABLE", 500),
			},
			"soft_delete": envBool("SCOUT_SOFT_DELETE"),
			"identify":    envBool("SCOUT_IDENTIFY"),

			"algolia": map[string]any{
				"id":             envOr("ALGOLIA_APP_ID", ""),
				"secret":         envOr("ALGOLIA_SECRET", ""),
				"host":           envOr("ALGOLIA_HOST", ""),
				"headers":        map[string]any{},
				"index-settings": map[string]any{},
			},
			"meilisearch": map[string]any{
				"host":           envOr("MEILISEARCH_HOST", "http://127.0.0.1:7700"),
				"key":            envOr("MEILISEARCH_KEY", ""),
				"index-settings": map[string]any{},
			},
			"typesense": map[string]any{
				"client-settings": map[string]any{
					"nodes": []any{map[string]any{
						"host":     envOr("TYPESENSE_HOST", "127.0.0.1"),
						"port":     envOr("TYPESENSE_PORT", "8108"),
						"protocol": envOr("TYPESENSE_PROTOCOL", "http"),
					}},
					"api_key":                    envOr("TYPESENSE_API_KEY", "xyz"),
					"connection_timeout_seconds": envInt("TYPESENSE_CONNECTION_TIMEOUT", 2),
				},
				"max_total_results": envInt("TYPESENSE_MAX_TOTAL_RESULTS", 1000),
				"model-settings":    map[string]any{},
				"import_action":     envOr("TYPESENSE_IMPORT_ACTION", "upsert"),
			},
			"elasticsearch": map[string]any{
				"hosts": []any{envOr("ELASTICSEARCH_HOST", "http://127.0.0.1:9200")},
				"auth":  map[string]any{},
			},
			"opensearch": map[string]any{
				"host":               envOr("OPENSEARCH_HTTP_HOST", "https://127.0.0.1:6205"),
				"username":           envOr("OPENSEARCH_USERNAME", "admin"),
				"password":           envOr("OPENSEARCH_PASSWORD", "admin"),
				"ssl_verification":   envBool("OPENSEARCH_SSL_VERIFICATION"),
				"ssl_cert":           envOr("OPENSEARCH_SSL_CERT", ""),
				"ssl_key":            envOr("OPENSEARCH_SSL_KEY", ""),
				"connection_timeout": envInt("OPENSEARCH_CONNECTION_TIMEOUT", 10),
				"timeout":            envInt("OPENSEARCH_TIMEOUT", 30),
				"indices":            map[string]any{},
				"bulk_size":          envInt("SCOUT_BULK_SIZE", 100),
			},
			"xunsearch": map[string]any{
				"config_path":   envOr("XUNSEARCH_CONFIG_PATH", ""),
				"default_index": envOr("XUNSEARCH_DEFAULT_INDEX", "default"),
				"charset":       envOr("XUNSEARCH_CHARSET", "utf-8"),
				"index_host":    envOr("XUNSEARCH_INDEX_HOST", "http://127.0.0.1"),
				"index_port":    envOr("XUNSEARCH_INDEX_PORT", "8383"),
				"search_host":   envOr("XUNSEARCH_SEARCH_HOST", "http://127.0.0.1"),
				"search_port":   envOr("XUNSEARCH_SEARCH_PORT", "8384"),
				"batch_size":    envInt("XUNSEARCH_BATCH_SIZE", 100),
			},
		},
	}
	return cfg
}

// Get resolves a dot-separated key against the config tree, returning default
// when any segment is missing. Mirrors scout_config($key, $default).
func (c *Config) Get(key string, def any) any {
	if key == "" {
		return c.raw
	}
	segments := strings.Split(key, ".")
	cur, ok := c.raw[segments[0]]
	if !ok {
		return def
	}
	if len(segments) == 1 {
		return cur
	}
	for _, seg := range segments[1:] {
		m, ok := cur.(map[string]any)
		if !ok {
			return def
		}
		next, ok := m[seg]
		if !ok {
			return def
		}
		cur = next
	}
	return cur
}

// String returns a string config value, or def.
func (c *Config) String(key string, def string) string {
	if s, ok := c.Get(key, nil).(string); ok {
		return s
	}
	return def
}

// Bool returns a bool config value, or def.
func (c *Config) Bool(key string, def bool) bool {
	switch v := c.Get(key, nil).(type) {
	case bool:
		return v
	case string:
		if n, err := strconv.ParseBool(v); err == nil {
			return n
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	}
	return def
}

// Int returns an int config value, or def.
func (c *Config) Int(key string, def int) int {
	switch v := c.Get(key, nil).(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

// Map returns the config value as a nested map, or an empty map.
func (c *Config) Map(key string) map[string]any {
	if m, ok := c.Get(key, nil).(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// List returns the config value as a string slice, or an empty slice.
func (c *Config) List(key string) []string {
	switch v := c.Get(key, nil).(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Driver returns the configured driver name (default "null"). Mirrors
// EngineManager::getDefaultDriver.
func (c *Config) Driver() string {
	d := c.String("driver", "")
	if d == "" || d == "false" {
		return "null"
	}
	return d
}

// Prefix returns the index name prefix.
func (c *Config) Prefix() string { return c.prefix }

// Queue reports whether data syncing is queued.
func (c *Config) Queue() bool { return c.Bool("queue", false) }

// AfterCommit reports whether syncing waits for DB commit.
func (c *Config) AfterCommit() bool { return c.Bool("after_commit", false) }

// SoftDelete reports whether soft-deleted records stay in indexes.
func (c *Config) SoftDelete() bool { return c.Bool("soft_delete", false) }

// Identify reports whether the engine is told who is searching (algolia).
func (c *Config) Identify() bool { return c.Bool("identify", false) }

// ChunkSearchable returns the searchable chunk size.
func (c *Config) ChunkSearchable() int { return c.Int("chunk.searchable", 500) }

// ChunkUnsearchable returns the unsearchable chunk size.
func (c *Config) ChunkUnsearchable() int { return c.Int("chunk.unsearchable", 500) }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return v != "0" && v != "false"
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}
