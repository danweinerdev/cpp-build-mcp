package main

import (
	"strings"
	"testing"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/state"
)

func makeTestInstance(name, buildDir string) *configInstance {
	return &configInstance{
		name:    name,
		cfg:     &config.Config{BuildDir: buildDir},
		builder: &fakeBuilder{},
		store:   state.NewStore(),
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

func TestStoreStatusTokenDirtyTakesPrecedenceOverPhase(t *testing.T) {
	s := state.NewStore()
	s.SetConfigured()
	s.SetDirty()

	token := storeStatusToken(s)
	if token != "dirty" {
		t.Fatalf("expected 'dirty' (takes precedence over configured phase), got %q", token)
	}
}
