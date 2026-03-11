package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/danweinerdev/cpp-build-mcp/builder"
	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/state"
)

// configInstance groups the per-configuration dependencies.
type configInstance struct {
	name        string
	cfg         *config.Config
	originalCfg config.Config // config as loaded from disk, before resolveToolchain mutation
	builder     builder.Builder
	store       *state.Store
}

// ConfigSummary is the JSON-serializable summary of a configuration.
type ConfigSummary struct {
	Name     string `json:"name"`
	BuildDir string `json:"build_dir"`
	Status   string `json:"status"`
}

// configRegistry manages named build configurations.
type configRegistry struct {
	mu        sync.RWMutex
	instances map[string]*configInstance
	dflt      string
}

// newConfigRegistry creates a configRegistry with the given default name.
func newConfigRegistry(dflt string) *configRegistry {
	return &configRegistry{
		instances: make(map[string]*configInstance),
		dflt:      dflt,
	}
}

// add adds an instance to the registry.
func (r *configRegistry) add(inst *configInstance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.instances[inst.name] = inst
}

// get returns the named instance or an error listing available config names.
func (r *configRegistry) get(name string) (*configInstance, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	inst, ok := r.instances[name]
	if !ok {
		names := make([]string, 0, len(r.instances))
		for n := range r.instances {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown configuration %q (available: %s)", name, strings.Join(names, ", "))
	}
	return inst, nil
}

// defaultInstance returns the default config instance. It panics if the
// default name is not found, which indicates a programming error.
func (r *configRegistry) defaultInstance() *configInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	inst, ok := r.instances[r.dflt]
	if !ok {
		panic(fmt.Sprintf("default configuration %q not found in registry", r.dflt))
	}
	return inst
}

// getDefault returns the name of the default configuration.
func (r *configRegistry) getDefault() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.dflt
}

// list returns summaries of all configurations sorted by name.
func (r *configRegistry) list() []ConfigSummary {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.instances))
	for n := range r.instances {
		names = append(names, n)
	}
	sort.Strings(names)

	summaries := make([]ConfigSummary, len(names))
	for i, n := range names {
		inst := r.instances[n]
		summaries[i] = ConfigSummary{
			Name:     n,
			BuildDir: inst.cfg.BuildDir,
			Status:   storeStatusToken(inst.store),
		}
	}
	return summaries
}

// len returns the number of instances in the registry.
func (r *configRegistry) len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.instances)
}

// all returns all instances sorted by name. The caller must not modify the
// returned instances.
func (r *configRegistry) all() []*configInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.instances))
	for n := range r.instances {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]*configInstance, len(names))
	for i, n := range names {
		out[i] = r.instances[n]
	}
	return out
}

// reloadResult describes what changed during a registry reload.
type reloadResult struct {
	Added     []string
	Removed   []string
	Changed   []string
	Unchanged []string
}

// reload replaces the registry contents based on freshly loaded configs.
//
// For each config name it compares the incoming config against the existing
// instance's originalCfg (the config as loaded from disk, before any
// resolveToolchain mutation). Unchanged configs preserve their existing
// configInstance (builder + store state). Changed or new configs get a fresh
// builder (via builderFactory) and a fresh state.Store. Removed configs are
// dropped from the registry.
//
// If the current default config is removed, dflt is set to defaultName.
func (r *configRegistry) reload(
	configs map[string]*config.Config,
	defaultName string,
	builderFactory func(*config.Config) (builder.Builder, error),
	toolchainResolver func(*configInstance),
) (reloadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result reloadResult

	// Build the new instances map.
	newInstances := make(map[string]*configInstance, len(configs))

	// Process configs in sorted order for deterministic result slices.
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cfg := configs[name]

		if existing, ok := r.instances[name]; ok {
			// Compare against the original (pre-mutation) config.
			if reflect.DeepEqual(existing.originalCfg, *cfg) {
				// Unchanged: preserve existing instance.
				newInstances[name] = existing
				result.Unchanged = append(result.Unchanged, name)
				continue
			}
			// Changed: create fresh instance.
			result.Changed = append(result.Changed, name)
		} else {
			// New config.
			result.Added = append(result.Added, name)
		}

		// Create fresh builder and store for new/changed configs.
		b, err := builderFactory(cfg)
		if err != nil {
			return reloadResult{}, fmt.Errorf("creating builder for config %q: %w", name, err)
		}
		inst := &configInstance{
			name:        name,
			cfg:         cfg,
			originalCfg: *cfg,
			builder:     b,
			store:       state.NewStore(),
		}
		toolchainResolver(inst)
		newInstances[name] = inst
	}

	// Detect removed configs (in old but not in new).
	oldNames := make([]string, 0, len(r.instances))
	for name := range r.instances {
		oldNames = append(oldNames, name)
	}
	sort.Strings(oldNames)

	for _, name := range oldNames {
		if _, ok := newInstances[name]; !ok {
			result.Removed = append(result.Removed, name)
		}
	}

	// Replace registry contents.
	r.instances = newInstances

	// Update default: if the old default was removed, use the provided defaultName.
	if _, ok := newInstances[r.dflt]; !ok {
		r.dflt = defaultName
	}

	return result, nil
}

// storeStatusToken maps a Store's state to a compact status token.
// It checks building before dirty because a build can be in progress while
// dirty. It checks dirty before phase because a dirty build that was cleaned
// to PhaseConfigured still needs the dirty warning.
func storeStatusToken(s *state.Store) string {
	if s.IsBuilding() {
		return "building"
	}
	if s.IsDirty() {
		return "dirty"
	}
	switch s.GetPhase() {
	case state.PhaseUnconfigured:
		return "unconfigured"
	case state.PhaseConfigured:
		return "configured"
	case state.PhaseBuilt:
		return "built"
	default:
		return "unknown"
	}
}

// aggregateHealthToken maps a Store's state to an uppercase token for the
// aggregate build://health format. The tokens match the design spec:
// OK, FAIL(N errors), UNCONFIGURED, READY, DIRTY, BUILDING.
func aggregateHealthToken(s *state.Store) string {
	if s.IsBuilding() {
		return "BUILDING"
	}
	if s.IsDirty() {
		return "DIRTY"
	}
	switch s.GetPhase() {
	case state.PhaseUnconfigured:
		return "UNCONFIGURED"
	case state.PhaseConfigured:
		return "READY"
	case state.PhaseBuilt:
		if s.LastExitCode() == 0 {
			return "OK"
		}
		return fmt.Sprintf("FAIL(%d errors)", len(s.Errors()))
	default:
		return "UNKNOWN"
	}
}
