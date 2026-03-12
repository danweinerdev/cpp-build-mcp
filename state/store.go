// Package state provides persistent state storage for build sessions.
package state

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/danweinerdev/cpp-build-mcp/diagnostics"
)

// Phase represents the current lifecycle phase of the build project.
type Phase int

const (
	PhaseUnconfigured Phase = iota
	PhaseConfigured
	PhaseBuilt
)

// BuildState holds the current snapshot of the build system state.
type BuildState struct {
	Phase                   Phase
	LastBuildTime           time.Time
	LastSuccessfulBuildTime time.Time
	LastExitCode            int
	LastDuration            time.Duration
	Errors                  []diagnostics.Diagnostic
	Warnings                []diagnostics.Diagnostic
	ErrorCount              int
	WarningCount            int
	DirtyFiles              []string // files changed since last successful build (populated by get_changed_files)
	Dirty                   bool     // true if previous build was killed (lock files may exist)
	BuildInProgress         bool
}

// Store provides thread-safe access to the build state. All public methods
// acquire the appropriate lock (RLock for reads, Lock for writes).
type Store struct {
	mu    sync.RWMutex
	state BuildState
}

// NewStore creates a new Store with PhaseUnconfigured.
func NewStore() *Store {
	return &Store{
		state: BuildState{
			Phase: PhaseUnconfigured,
		},
	}
}

// SetConfigured transitions the project to PhaseConfigured.
func (s *Store) SetConfigured() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Phase = PhaseConfigured
}

// StartBuild marks a build as in progress. It returns an error if the project
// is not yet configured or if a build is already running.
//
// The caller is responsible for calling FinishBuild when the build completes
// (including on error/panic). If FinishBuild is never called, BuildInProgress
// will remain true and all subsequent StartBuild calls will fail.
func (s *Store) StartBuild() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state.Phase < PhaseConfigured {
		return errors.New("project not configured, call configure() first")
	}
	if s.state.BuildInProgress {
		return errors.New("build already in progress")
	}
	s.state.BuildInProgress = true
	return nil
}

// FinishBuild records the results of a completed build. It sets the phase to
// PhaseBuilt and updates all diagnostic and timing fields. If exitCode is 0,
// LastSuccessfulBuildTime is also updated.
func (s *Store) FinishBuild(exitCode int, duration time.Duration, errs, warnings []diagnostics.Diagnostic) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	s.state.BuildInProgress = false
	s.state.Phase = PhaseBuilt
	s.state.LastBuildTime = now
	s.state.LastExitCode = exitCode
	s.state.LastDuration = duration
	s.state.Errors = errs
	s.state.Warnings = warnings
	s.state.ErrorCount = len(errs)
	s.state.WarningCount = len(warnings)

	if exitCode == 0 {
		s.state.LastSuccessfulBuildTime = now
	}
}

// SetDirty marks the build as dirty (e.g., after a killed build).
func (s *Store) SetDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Dirty = true
}

// ClearDirty clears the dirty flag.
func (s *Store) ClearDirty() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Dirty = false
}

// IsDirty reports whether the build state is dirty.
func (s *Store) IsDirty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Dirty
}

// Errors returns a copy of the current error diagnostics.
func (s *Store) Errors() []diagnostics.Diagnostic {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.state.Errors == nil {
		return nil
	}
	out := make([]diagnostics.Diagnostic, len(s.state.Errors))
	copy(out, s.state.Errors)
	return out
}

// Warnings returns a copy of the current warning diagnostics.
func (s *Store) Warnings() []diagnostics.Diagnostic {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.state.Warnings == nil {
		return nil
	}
	out := make([]diagnostics.Diagnostic, len(s.state.Warnings))
	copy(out, s.state.Warnings)
	return out
}

// Health returns a one-line summary of the current build health.
// Dirty is checked before phase intentionally: a killed build that was cleaned
// back to PhaseConfigured still needs the dirty warning surfaced so the next
// build triggers --clean-first.
func (s *Store) Health() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.state.Dirty {
		return "DIRTY: previous build was killed, next build will clean first"
	}

	switch s.state.Phase {
	case PhaseUnconfigured:
		return "UNCONFIGURED: no build has run — call configure() then build()"
	case PhaseConfigured:
		return "READY: configured, no build run yet — call build()"
	case PhaseBuilt:
		ago := time.Since(s.state.LastBuildTime).Truncate(time.Second)
		if s.state.LastExitCode == 0 {
			return fmt.Sprintf("OK: %d errors, %d warnings, last build %s ago",
				s.state.ErrorCount, s.state.WarningCount, ago)
		}
		return fmt.Sprintf("FAIL: %d errors, last build %s ago",
			s.state.ErrorCount, ago)
	default:
		return "UNKNOWN"
	}
}

// SetConfigureDiagnostics stores diagnostics from a failed configure step
// without changing the build phase or build-in-progress state. This allows
// get_errors to return cmake configure errors without falsely advancing the
// project to PhaseConfigured.
func (s *Store) SetConfigureDiagnostics(errs, warns []diagnostics.Diagnostic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Errors = errs
	s.state.Warnings = warns
	s.state.ErrorCount = len(errs)
	s.state.WarningCount = len(warns)
}

// SetClean resets the build state to PhaseConfigured and clears all
// diagnostics and counts. It does NOT clear the Dirty flag — Dirty is
// managed independently via SetDirty/ClearDirty.
//
// Callers must ensure SetConfigured has been called before SetClean;
// otherwise this will advance an unconfigured project to PhaseConfigured.
func (s *Store) SetClean() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Phase = PhaseConfigured
	s.state.Errors = nil
	s.state.Warnings = nil
	s.state.ErrorCount = 0
	s.state.WarningCount = 0
}

// LastSuccessfulBuildTime returns the time of the last successful build.
// Returns the zero time if no successful build has been recorded.
func (s *Store) LastSuccessfulBuildTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.LastSuccessfulBuildTime
}

// GetPhase returns the current build phase.
func (s *Store) GetPhase() Phase {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Phase
}

// IsBuilding reports whether a build is currently in progress.
func (s *Store) IsBuilding() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.BuildInProgress
}

// LastExitCode returns the exit code of the last completed build.
// Returns 0 if no build has completed yet.
func (s *Store) LastExitCode() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.LastExitCode
}
