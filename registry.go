package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/danweinerdev/cpp-build-mcp/builder"
	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/state"
)

// configInstance groups the per-configuration dependencies.
type configInstance struct {
	name    string
	cfg     *config.Config
	builder builder.Builder
	store   *state.Store
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
