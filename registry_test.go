package main

import (
	"strings"
	"testing"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/diagnostics"
	"github.com/danweinerdev/cpp-build-mcp/state"
)

func makeTestInstance(name, buildDir string) *configInstance {
	cfg := &config.Config{BuildDir: buildDir}
	return &configInstance{
		name:        name,
		cfg:         cfg,
		originalCfg: *cfg,
		builder:     &fakeBuilder{},
		store:       state.NewStore(),
	}
}

func TestRegistryGetValid(t *testing.T) {
	reg := newConfigRegistry("default")
	inst := makeTestInstance("alpha", "build-alpha")
	reg.add(inst)

	got, err := reg.get("alpha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != inst {
		t.Fatal("expected the same instance pointer to be returned")
	}
	if got.name != "alpha" {
		t.Fatalf("expected name alpha, got %s", got.name)
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	reg := newConfigRegistry("default")
	reg.add(makeTestInstance("bar", "build-bar"))
	reg.add(makeTestInstance("baz", "build-baz"))

	_, err := reg.get("foo")
	if err == nil {
		t.Fatal("expected error for unknown configuration")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unknown configuration") {
		t.Fatalf("expected 'unknown configuration' in error, got %q", msg)
	}
	if !strings.Contains(msg, `"foo"`) {
		t.Fatalf("expected '\"foo\"' in error, got %q", msg)
	}
	// Available names should be sorted alphabetically.
	if !strings.Contains(msg, "bar, baz") {
		t.Fatalf("expected 'bar, baz' in error, got %q", msg)
	}
}

func TestRegistryDefaultInstance(t *testing.T) {
	reg := newConfigRegistry("primary")
	primary := makeTestInstance("primary", "build-primary")
	secondary := makeTestInstance("secondary", "build-secondary")
	reg.add(primary)
	reg.add(secondary)

	got := reg.defaultInstance()
	if got != primary {
		t.Fatal("expected defaultInstance to return the primary instance")
	}
}

func TestRegistryList(t *testing.T) {
	reg := newConfigRegistry("debug")

	debug := makeTestInstance("debug", "build-debug")
	debug.store.SetConfigured()

	release := makeTestInstance("release", "build-release")
	// release stays unconfigured

	reg.add(debug)
	reg.add(release)

	summaries := reg.list()
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	// Should be sorted by name: debug before release.
	if summaries[0].Name != "debug" {
		t.Fatalf("expected first summary name 'debug', got %q", summaries[0].Name)
	}
	if summaries[0].BuildDir != "build-debug" {
		t.Fatalf("expected first summary build_dir 'build-debug', got %q", summaries[0].BuildDir)
	}
	if summaries[0].Status != "configured" {
		t.Fatalf("expected first summary status 'configured', got %q", summaries[0].Status)
	}

	if summaries[1].Name != "release" {
		t.Fatalf("expected second summary name 'release', got %q", summaries[1].Name)
	}
	if summaries[1].BuildDir != "build-release" {
		t.Fatalf("expected second summary build_dir 'build-release', got %q", summaries[1].BuildDir)
	}
	if summaries[1].Status != "unconfigured" {
		t.Fatalf("expected second summary status 'unconfigured', got %q", summaries[1].Status)
	}
}

func TestRegistryLen(t *testing.T) {
	reg := newConfigRegistry("default")
	if reg.len() != 0 {
		t.Fatalf("expected len 0, got %d", reg.len())
	}

	reg.add(makeTestInstance("a", "build-a"))
	if reg.len() != 1 {
		t.Fatalf("expected len 1, got %d", reg.len())
	}

	reg.add(makeTestInstance("b", "build-b"))
	if reg.len() != 2 {
		t.Fatalf("expected len 2, got %d", reg.len())
	}
}

func TestStoreStatusTokenUnconfigured(t *testing.T) {
	s := state.NewStore()
	token := storeStatusToken(s)
	if token != "unconfigured" {
		t.Fatalf("expected 'unconfigured', got %q", token)
	}
}

func TestStoreStatusTokenConfigured(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	token := storeStatusToken(s)
	if token != "configured" {
		t.Fatalf("expected 'configured', got %q", token)
	}
}

func TestStoreStatusTokenBuilt(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.FinishBuild(0, time.Second, nil, nil)

	token := storeStatusToken(s)
	if token != "built" {
		t.Fatalf("expected 'built', got %q", token)
	}
}

func TestStoreStatusTokenBuilding(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Do not call FinishBuild — build is still in progress.

	token := storeStatusToken(s)
	if token != "building" {
		t.Fatalf("expected 'building', got %q", token)
	}
}

func TestStoreStatusTokenDirty(t *testing.T) {
	s := state.NewStore()
	s.SetDirty()

	token := storeStatusToken(s)
	if token != "dirty" {
		t.Fatalf("expected 'dirty', got %q", token)
	}
}

func TestStoreStatusTokenBuildingTakesPrecedenceOverDirty(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	s.SetDirty()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both dirty and building — building should win.
	token := storeStatusToken(s)
	if token != "building" {
		t.Fatalf("expected 'building' (takes precedence over dirty), got %q", token)
	}
}

func TestStoreStatusTokenDirtyTakesPrecedenceOverPhase(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	s.SetDirty()

	token := storeStatusToken(s)
	if token != "dirty" {
		t.Fatalf("expected 'dirty' (takes precedence over configured phase), got %q", token)
	}
}

// --- aggregateHealthToken tests ---

func TestAggregateHealthTokenUnconfigured(t *testing.T) {
	s := state.NewStore()
	if got := aggregateHealthToken(s); got != "UNCONFIGURED" {
		t.Fatalf("expected UNCONFIGURED, got %q", got)
	}
}

func TestAggregateHealthTokenReady(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	if got := aggregateHealthToken(s); got != "READY" {
		t.Fatalf("expected READY, got %q", got)
	}
}

func TestAggregateHealthTokenOK(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.FinishBuild(0, time.Second, nil, nil)
	if got := aggregateHealthToken(s); got != "OK" {
		t.Fatalf("expected OK, got %q", got)
	}
}

func TestAggregateHealthTokenFail(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{Severity: diagnostics.SeverityError, Message: "e1"},
		{Severity: diagnostics.SeverityError, Message: "e2"},
		{Severity: diagnostics.SeverityError, Message: "e3"},
	}
	s.FinishBuild(1, time.Second, errs, nil)
	if got := aggregateHealthToken(s); got != "FAIL(3 errors)" {
		t.Fatalf("expected FAIL(3 errors), got %q", got)
	}
}

func TestAggregateHealthTokenDirty(t *testing.T) {
	s := state.NewStore()
	s.SetDirty()
	if got := aggregateHealthToken(s); got != "DIRTY" {
		t.Fatalf("expected DIRTY, got %q", got)
	}
}

func TestAggregateHealthTokenBuilding(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := aggregateHealthToken(s); got != "BUILDING" {
		t.Fatalf("expected BUILDING, got %q", got)
	}
}

// --- registry.all() tests ---

func TestRegistryAll(t *testing.T) {
	reg := newConfigRegistry("debug")
	reg.add(makeTestInstance("release", "build-release"))
	reg.add(makeTestInstance("debug", "build-debug"))
	reg.add(makeTestInstance("asan", "build-asan"))

	all := reg.all()
	if len(all) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(all))
	}
	// Should be sorted alphabetically: asan, debug, release.
	if all[0].name != "asan" {
		t.Fatalf("expected first instance 'asan', got %q", all[0].name)
	}
	if all[1].name != "debug" {
		t.Fatalf("expected second instance 'debug', got %q", all[1].name)
	}
	if all[2].name != "release" {
		t.Fatalf("expected third instance 'release', got %q", all[2].name)
	}
}
