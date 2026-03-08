package state

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/diagnostics"
)

func TestNewStoreStartsUnconfigured(t *testing.T) {
	s := NewStore()
	if s.GetPhase() != PhaseUnconfigured {
		t.Fatalf("expected PhaseUnconfigured, got %d", s.GetPhase())
	}
}

func TestSetConfiguredTransitions(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if s.GetPhase() != PhaseConfigured {
		t.Fatalf("expected PhaseConfigured, got %d", s.GetPhase())
	}
}

func TestStartBuildFailsWhenUnconfigured(t *testing.T) {
	s := NewStore()
	err := s.StartBuild()
	if err == nil {
		t.Fatal("expected error when starting build in unconfigured state")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error message: %s", err.Error())
	}
}

func TestStartBuildSucceedsWhenConfigured(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	err := s.StartBuild()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.IsBuilding() {
		t.Fatal("expected BuildInProgress to be true")
	}
}

func TestStartBuildFailsWhenAlreadyBuilding(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error on first StartBuild: %v", err)
	}
	err := s.StartBuild()
	if err == nil {
		t.Fatal("expected error when build already in progress")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("unexpected error message: %s", err.Error())
	}
}

func TestFinishBuildUpdatesState(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	errs := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityError, Message: "undeclared identifier"},
	}
	warns := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 20, Severity: diagnostics.SeverityWarning, Message: "unused variable"},
		{File: "util.cpp", Line: 5, Severity: diagnostics.SeverityWarning, Message: "implicit conversion"},
	}
	dur := 3 * time.Second

	before := time.Now()
	s.FinishBuild(1, dur, errs, warns)
	after := time.Now()

	if s.GetPhase() != PhaseBuilt {
		t.Fatalf("expected PhaseBuilt, got %d", s.GetPhase())
	}
	if s.IsBuilding() {
		t.Fatal("expected BuildInProgress to be false after FinishBuild")
	}

	// Access internal state for detailed checks.
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.state.LastExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", s.state.LastExitCode)
	}
	if s.state.LastDuration != dur {
		t.Fatalf("expected duration %v, got %v", dur, s.state.LastDuration)
	}
	if s.state.ErrorCount != 1 {
		t.Fatalf("expected 1 error, got %d", s.state.ErrorCount)
	}
	if s.state.WarningCount != 2 {
		t.Fatalf("expected 2 warnings, got %d", s.state.WarningCount)
	}
	if s.state.LastBuildTime.Before(before) || s.state.LastBuildTime.After(after) {
		t.Fatalf("LastBuildTime %v not in expected range [%v, %v]", s.state.LastBuildTime, before, after)
	}
}

func TestFinishBuildWithExitZeroUpdatesSuccessTime(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	before := time.Now()
	s.FinishBuild(0, time.Second, nil, nil)
	after := time.Now()

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.state.LastSuccessfulBuildTime.IsZero() {
		t.Fatal("expected LastSuccessfulBuildTime to be set")
	}
	if s.state.LastSuccessfulBuildTime.Before(before) || s.state.LastSuccessfulBuildTime.After(after) {
		t.Fatalf("LastSuccessfulBuildTime %v not in expected range [%v, %v]",
			s.state.LastSuccessfulBuildTime, before, after)
	}
}

func TestFinishBuildWithNonZeroExitDoesNotUpdateSuccessTime(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s.FinishBuild(2, time.Second, nil, nil)

	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.state.LastSuccessfulBuildTime.IsZero() {
		t.Fatalf("expected LastSuccessfulBuildTime to be zero, got %v", s.state.LastSuccessfulBuildTime)
	}
}

func TestDirtyFlag(t *testing.T) {
	s := NewStore()

	if s.IsDirty() {
		t.Fatal("new store should not be dirty")
	}

	s.SetDirty()
	if !s.IsDirty() {
		t.Fatal("expected dirty after SetDirty")
	}

	s.ClearDirty()
	if s.IsDirty() {
		t.Fatal("expected not dirty after ClearDirty")
	}
}

func TestSetCleanResetsToConfigured(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 1, Severity: diagnostics.SeverityError, Message: "fail"},
	}
	warns := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 2, Severity: diagnostics.SeverityWarning, Message: "warn"},
	}
	s.FinishBuild(1, time.Second, errs, warns)

	s.SetClean()

	if s.GetPhase() != PhaseConfigured {
		t.Fatalf("expected PhaseConfigured after SetClean, got %d", s.GetPhase())
	}
	if got := s.Errors(); got != nil {
		t.Fatalf("expected nil errors after SetClean, got %v", got)
	}
	if got := s.Warnings(); got != nil {
		t.Fatalf("expected nil warnings after SetClean, got %v", got)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state.ErrorCount != 0 {
		t.Fatalf("expected 0 error count, got %d", s.state.ErrorCount)
	}
	if s.state.WarningCount != 0 {
		t.Fatalf("expected 0 warning count, got %d", s.state.WarningCount)
	}
}

func TestHealthUnconfigured(t *testing.T) {
	s := NewStore()
	h := s.Health()
	if !strings.HasPrefix(h, "UNCONFIGURED:") {
		t.Fatalf("expected UNCONFIGURED health, got %q", h)
	}
}

func TestHealthReady(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	h := s.Health()
	if !strings.HasPrefix(h, "READY:") {
		t.Fatalf("expected READY health, got %q", h)
	}
}

func TestHealthOK(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 1, Severity: diagnostics.SeverityWarning, Message: "w1"},
		{File: "b.cpp", Line: 2, Severity: diagnostics.SeverityWarning, Message: "w2"},
	}
	s.FinishBuild(0, time.Second, nil, warns)

	h := s.Health()
	if !strings.HasPrefix(h, "OK:") {
		t.Fatalf("expected OK health, got %q", h)
	}
	if !strings.Contains(h, "0 errors") {
		t.Fatalf("expected '0 errors' in health, got %q", h)
	}
	if !strings.Contains(h, "2 warnings") {
		t.Fatalf("expected '2 warnings' in health, got %q", h)
	}
}

func TestHealthFail(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 1, Severity: diagnostics.SeverityError, Message: "e1"},
		{File: "b.cpp", Line: 2, Severity: diagnostics.SeverityError, Message: "e2"},
		{File: "c.cpp", Line: 3, Severity: diagnostics.SeverityError, Message: "e3"},
	}
	s.FinishBuild(1, time.Second, errs, nil)

	h := s.Health()
	if !strings.HasPrefix(h, "FAIL:") {
		t.Fatalf("expected FAIL health, got %q", h)
	}
	if !strings.Contains(h, "3 errors") {
		t.Fatalf("expected '3 errors' in health, got %q", h)
	}
}

func TestHealthDirty(t *testing.T) {
	s := NewStore()
	s.SetDirty()
	h := s.Health()
	if !strings.HasPrefix(h, "DIRTY:") {
		t.Fatalf("expected DIRTY health, got %q", h)
	}
}

func TestErrorsReturnsCopy(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 1, Severity: diagnostics.SeverityError, Message: "e1"},
	}
	s.FinishBuild(1, time.Second, errs, nil)

	got := s.Errors()
	if len(got) != 1 {
		t.Fatalf("expected 1 error, got %d", len(got))
	}
	// Mutate the returned slice and verify internal state is unchanged.
	got[0].Message = "mutated"
	got2 := s.Errors()
	if got2[0].Message == "mutated" {
		t.Fatal("Errors() should return a copy, but internal state was mutated")
	}
}

func TestWarningsReturnsCopy(t *testing.T) {
	s := NewStore()
	s.SetConfigured()
	if err := s.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 1, Severity: diagnostics.SeverityWarning, Message: "w1"},
	}
	s.FinishBuild(0, time.Second, nil, warns)

	got := s.Warnings()
	if len(got) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(got))
	}
	got[0].Message = "mutated"
	got2 := s.Warnings()
	if got2[0].Message == "mutated" {
		t.Fatal("Warnings() should return a copy, but internal state was mutated")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	s.SetConfigured()

	var wg sync.WaitGroup

	// Writer goroutine: repeatedly start/finish builds.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := s.StartBuild(); err != nil {
				continue // may fail if already building; that's fine
			}
			errs := []diagnostics.Diagnostic{
				{File: "a.cpp", Line: i, Severity: diagnostics.SeverityError, Message: "err"},
			}
			warns := []diagnostics.Diagnostic{
				{File: "b.cpp", Line: i, Severity: diagnostics.SeverityWarning, Message: "warn"},
			}
			s.FinishBuild(i%2, time.Millisecond, errs, warns)
		}
	}()

	// Reader goroutines: concurrent reads while the writer is active.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = s.GetPhase()
				_ = s.IsBuilding()
				_ = s.IsDirty()
				_ = s.Errors()
				_ = s.Warnings()
				_ = s.Health()
			}
		}()
	}

	// Additional writer goroutines for dirty flag.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.SetDirty()
				s.ClearDirty()
			}
		}()
	}

	wg.Wait()
}
