package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/danweinerdev/cpp-build-mcp/builder"
	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/diagnostics"
	"github.com/danweinerdev/cpp-build-mcp/state"
)

// fakeBuilder implements builder.Builder for testing without invoking cmake.
type fakeBuilder struct {
	buildResult *builder.BuildResult
	buildErr    error

	configureResult *builder.BuildResult
	configureErr    error

	cleanResult *builder.BuildResult
	cleanErr    error

	// Captured arguments from the last Build call.
	lastTargets []string
	lastJobs    int

	// Captured arguments from the last Configure call.
	lastConfigureArgs []string

	// Captured arguments from the last Clean call.
	lastCleanTargets []string

	// Captured dirty flag from the last SetDirty call.
	lastDirtySet bool

	// Set to true when Configure is called.
	configureCalled bool
}

func (f *fakeBuilder) Configure(_ context.Context, args []string) (*builder.BuildResult, error) {
	f.configureCalled = true
	f.lastConfigureArgs = args
	if f.configureErr != nil {
		return nil, f.configureErr
	}
	if f.configureResult != nil {
		return f.configureResult, nil
	}
	return &builder.BuildResult{}, nil
}

func (f *fakeBuilder) Build(_ context.Context, targets []string, jobs int) (*builder.BuildResult, error) {
	f.lastTargets = targets
	f.lastJobs = jobs
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return f.buildResult, nil
}

func (f *fakeBuilder) Clean(_ context.Context, targets []string) (*builder.BuildResult, error) {
	f.lastCleanTargets = targets
	if f.cleanErr != nil {
		return nil, f.cleanErr
	}
	if f.cleanResult != nil {
		return f.cleanResult, nil
	}
	return &builder.BuildResult{}, nil
}

func (f *fakeBuilder) SetDirty(dirty bool) { f.lastDirtySet = dirty }

// newTestServer creates an mcpServer with a fakeBuilder and fresh state store.
// It returns both the server and the store for direct state manipulation in tests.
func newTestServer(fb *fakeBuilder) (*mcpServer, *state.Store) {
	cfg := &config.Config{
		BuildDir:  "build",
		SourceDir: ".",
		Toolchain: "auto",
		Generator: "ninja",
	}
	store := state.NewStore()
	registry := newConfigRegistry("default")
	registry.add(&configInstance{
		name:    "default",
		cfg:     cfg,
		builder: fb,
		store:   store,
	})
	return &mcpServer{
		registry: registry,
	}, store
}

// makeCallToolRequest builds a CallToolRequest with the given arguments map.
// The Arguments field must be map[string]any for GetArguments() to work.
func makeCallToolRequest(args map[string]interface{}) mcp.CallToolRequest {
	var arguments any
	if args != nil {
		arguments = args
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: arguments,
		},
	}
}

func TestBuildToolUnconfiguredReturnsError(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{ExitCode: 0, Duration: time.Second},
	}
	srv, _ := newTestServer(fb)
	// Do NOT call SetConfigured — store is in PhaseUnconfigured.

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error result when unconfigured")
	}
	text := extractText(t, result)
	if text == "" {
		t.Fatal("expected non-empty error text")
	}
}

func TestBuildToolInProgressReturnsError(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{ExitCode: 0, Duration: time.Second},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Start a build to mark as in-progress.
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error result when build in progress")
	}
}

func TestBuildToolSuccessfulBuild(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Stdout:   "",
			Stderr:   "",
			Duration: 2500 * time.Millisecond,
		},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp buildResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Fatalf("expected exit_code 0, got %d", resp.ExitCode)
	}
	if resp.ErrorCount != 0 {
		t.Fatalf("expected error_count 0, got %d", resp.ErrorCount)
	}
	if resp.WarningCount != 0 {
		t.Fatalf("expected warning_count 0, got %d", resp.WarningCount)
	}
	if resp.DurationMs != 2500 {
		t.Fatalf("expected duration_ms 2500, got %d", resp.DurationMs)
	}
	if resp.FilesCompiled != 0 {
		t.Fatalf("expected files_compiled 0, got %d", resp.FilesCompiled)
	}
}

func TestBuildToolPassesTargetsAndJobs(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Duration: time.Second,
		},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(map[string]interface{}{
		"targets": []interface{}{"app", "lib"},
		"jobs":    float64(4), // JSON numbers are float64
	})

	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if len(fb.lastTargets) != 2 || fb.lastTargets[0] != "app" || fb.lastTargets[1] != "lib" {
		t.Fatalf("expected targets [app lib], got %v", fb.lastTargets)
	}
	if fb.lastJobs != 4 {
		t.Fatalf("expected jobs 4, got %d", fb.lastJobs)
	}
}

func TestBuildToolProcessSpawnError(t *testing.T) {
	fb := &fakeBuilder{
		buildErr: context.DeadlineExceeded,
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error result when build process fails to spawn")
	}

	// State should not be left with build-in-progress.
	if store.IsBuilding() {
		t.Fatal("expected BuildInProgress to be false after process spawn error")
	}
}

func TestBuildToolUpdatesState(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: 1,
			Stdout:   "",
			Stderr:   "",
			Duration: 3 * time.Second,
		},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	// Phase should be PhaseBuilt after build completes.
	if store.GetPhase() != state.PhaseBuilt {
		t.Fatalf("expected PhaseBuilt, got %d", store.GetPhase())
	}
	// Build should no longer be in progress.
	if store.IsBuilding() {
		t.Fatal("expected BuildInProgress to be false after build finishes")
	}
}

func TestBuildToolDirtyFlagClearedOnSuccess(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Duration: time.Second,
		},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()
	store.SetDirty()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if store.IsDirty() {
		t.Fatal("expected dirty flag to be cleared after successful build")
	}
}

func TestBuildToolKilledSetsDirty(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: -1,
			Duration: time.Second,
			Killed:   true,
		},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if !store.IsDirty() {
		t.Fatal("expected dirty flag to be set after killed build")
	}
}

func TestBuildToolDirtyFlagNotClearedOnFailure(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: 1,
			Duration: time.Second,
		},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()
	store.SetDirty()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if !store.IsDirty() {
		t.Fatal("expected dirty flag to remain set after failed build")
	}
}

func TestGetErrorsNoErrors(t *testing.T) {
	fb := &fakeBuilder{}
	srv, _ := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetErrors(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp getErrorsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("expected 0 errors, got %d", len(resp.Errors))
	}
}

func TestGetErrorsWithErrors(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Simulate a build that produced errors.
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Column: 5, Severity: diagnostics.SeverityError, Message: "undeclared identifier 'x'", Code: "undeclared_var"},
		{File: "util.cpp", Line: 20, Severity: diagnostics.SeverityError, Message: "missing semicolon"},
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetErrors(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp getErrorsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(resp.Errors))
	}

	// Verify first error.
	if resp.Errors[0].File != "main.cpp" {
		t.Fatalf("expected file main.cpp, got %s", resp.Errors[0].File)
	}
	if resp.Errors[0].Line != 10 {
		t.Fatalf("expected line 10, got %d", resp.Errors[0].Line)
	}
	if resp.Errors[0].Column != 5 {
		t.Fatalf("expected column 5, got %d", resp.Errors[0].Column)
	}
	if resp.Errors[0].Severity != "error" {
		t.Fatalf("expected severity error, got %s", resp.Errors[0].Severity)
	}
	if resp.Errors[0].Message != "undeclared identifier 'x'" {
		t.Fatalf("expected message 'undeclared identifier 'x'', got %s", resp.Errors[0].Message)
	}
	if resp.Errors[0].Code != "undeclared_var" {
		t.Fatalf("expected code undeclared_var, got %s", resp.Errors[0].Code)
	}

	// Verify second error has no code (omitempty should exclude it).
	if resp.Errors[1].Code != "" {
		t.Fatalf("expected empty code for second error, got %s", resp.Errors[1].Code)
	}
}

func TestGetErrorsCapsAt20(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Create 25 errors.
	var errs []diagnostics.Diagnostic
	for i := 0; i < 25; i++ {
		errs = append(errs, diagnostics.Diagnostic{
			File:     "main.cpp",
			Line:     i + 1,
			Severity: diagnostics.SeverityError,
			Message:  "error",
		})
	}

	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetErrors(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp getErrorsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Errors) != 20 {
		t.Fatalf("expected 20 errors (capped), got %d", len(resp.Errors))
	}
}

func TestGetErrorsOmitsEmptyFields(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Create an error with minimal fields — column=0, code="" should be omitted.
	errs := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 1, Severity: diagnostics.SeverityError, Message: "fail"},
	}

	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetErrors(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(t, result)

	// Verify the raw JSON does not contain "column" or "code" fields.
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	errList := raw["errors"].([]interface{})
	entry := errList[0].(map[string]interface{})

	if _, found := entry["column"]; found {
		t.Fatal("expected column field to be omitted when 0")
	}
	if _, found := entry["code"]; found {
		t.Fatal("expected code field to be omitted when empty")
	}
}

func TestBuildResponseJSONShape(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: 2,
			Duration: 1500 * time.Millisecond,
		},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(t, result)

	// Verify all expected JSON keys are present.
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	requiredKeys := []string{"exit_code", "error_count", "warning_count", "duration_ms", "files_compiled"}
	for _, key := range requiredKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("expected key %q in response JSON", key)
		}
	}

	// Verify duration_ms is an integer.
	if dms, ok := raw["duration_ms"].(float64); !ok {
		t.Fatalf("expected duration_ms to be a number, got %T", raw["duration_ms"])
	} else if dms != 1500 {
		t.Fatalf("expected duration_ms 1500, got %v", dms)
	}
}

func TestBuildHealthUnconfigured(t *testing.T) {
	srv, _ := newTestServer(&fakeBuilder{})
	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 resource content, got %d", len(result))
	}
	tc, ok := result[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", result[0])
	}
	if tc.URI != "build://health" {
		t.Fatalf("expected URI build://health, got %s", tc.URI)
	}
	if tc.Text == "" {
		t.Fatal("expected non-empty health text")
	}
	// Should contain UNCONFIGURED for a fresh store.
	if !strings.Contains(tc.Text, "UNCONFIGURED") {
		t.Fatalf("expected UNCONFIGURED in health text, got %q", tc.Text)
	}
}

func TestBuildHealthConfigured(t *testing.T) {
	srv, store := newTestServer(&fakeBuilder{})
	store.SetConfigured()
	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result[0].(mcp.TextResourceContents)
	if !strings.Contains(tc.Text, "READY") {
		t.Fatalf("expected READY in health text, got %q", tc.Text)
	}
}

func TestBuildHealthDirty(t *testing.T) {
	srv, store := newTestServer(&fakeBuilder{})
	store.SetConfigured()
	store.SetDirty()
	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result[0].(mcp.TextResourceContents)
	if !strings.Contains(tc.Text, "DIRTY") {
		t.Fatalf("expected DIRTY in health text, got %q", tc.Text)
	}
}

func TestBuildHealthAfterBuild(t *testing.T) {
	srv, store := newTestServer(&fakeBuilder{})
	store.SetConfigured()
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.FinishBuild(0, time.Second, nil, nil)

	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result[0].(mcp.TextResourceContents)
	if !strings.Contains(tc.Text, "OK") {
		t.Fatalf("expected OK in health text, got %q", tc.Text)
	}
}

func TestBuildHealthAfterFailedBuild(t *testing.T) {
	srv, store := newTestServer(&fakeBuilder{})
	store.SetConfigured()
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{{Severity: diagnostics.SeverityError, Message: "fail"}}
	store.FinishBuild(1, time.Second, errs, nil)

	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result[0].(mcp.TextResourceContents)
	if !strings.Contains(tc.Text, "FAIL") {
		t.Fatalf("expected FAIL in health text, got %q", tc.Text)
	}
}

// --- aggregate health tests ---

// TestBuildHealthSingleConfigReturnsVerboseFormat verifies that when only one
// config exists, build://health returns the existing verbose format unchanged
// for backward compatibility.
func TestBuildHealthSingleConfigReturnsVerboseFormat(t *testing.T) {
	srv, store := newTestServer(&fakeBuilder{})
	store.SetConfigured()
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.FinishBuild(0, time.Second, nil, nil)

	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result[0].(mcp.TextResourceContents)
	// Single config should return the verbose Health() format with details.
	if !strings.HasPrefix(tc.Text, "OK:") {
		t.Fatalf("expected verbose format with 'OK:' prefix, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "errors") {
		t.Fatalf("expected verbose format with 'errors' detail, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "warnings") {
		t.Fatalf("expected verbose format with 'warnings' detail, got %q", tc.Text)
	}
}

// TestBuildHealthMultiConfigAggregateFormat verifies that when multiple configs
// exist, build://health returns the pipe-separated aggregate format with
// correct per-config tokens sorted by name.
func TestBuildHealthMultiConfigAggregateFormat(t *testing.T) {
	registry := newConfigRegistry("debug")

	// debug: configured + successful build -> OK
	debug := makeTestInstance("debug", "build-debug")
	debug.store.SetConfigured()
	if err := debug.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	debug.store.FinishBuild(0, time.Second, nil, nil)
	registry.add(debug)

	// release: configured + failed build with 3 errors -> FAIL(3 errors)
	release := makeTestInstance("release", "build-release")
	release.store.SetConfigured()
	if err := release.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{Severity: diagnostics.SeverityError, Message: "e1"},
		{Severity: diagnostics.SeverityError, Message: "e2"},
		{Severity: diagnostics.SeverityError, Message: "e3"},
	}
	release.store.FinishBuild(1, time.Second, errs, nil)
	registry.add(release)

	// asan: unconfigured -> UNCONFIGURED
	asan := makeTestInstance("asan", "build-asan")
	registry.add(asan)

	srv := &mcpServer{registry: registry}
	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result[0].(mcp.TextResourceContents)

	// Expected format: "asan: UNCONFIGURED | debug: OK | release: FAIL(3 errors)"
	// (alphabetical order: asan, debug, release)
	expected := "asan: UNCONFIGURED | debug: OK | release: FAIL(3 errors)"
	if tc.Text != expected {
		t.Fatalf("expected %q, got %q", expected, tc.Text)
	}
}

// TestBuildHealthMultiConfigDirtyState verifies that a dirty config renders
// as "DIRTY" in the aggregate format.
func TestBuildHealthMultiConfigDirtyState(t *testing.T) {
	registry := newConfigRegistry("debug")

	// debug: dirty -> DIRTY
	debug := makeTestInstance("debug", "build-debug")
	debug.store.SetConfigured()
	debug.store.SetDirty()
	registry.add(debug)

	// release: configured -> READY
	release := makeTestInstance("release", "build-release")
	release.store.SetConfigured()
	registry.add(release)

	srv := &mcpServer{registry: registry}
	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result[0].(mcp.TextResourceContents)

	expected := "debug: DIRTY | release: READY"
	if tc.Text != expected {
		t.Fatalf("expected %q, got %q", expected, tc.Text)
	}
}

// --- get_warnings tests ---

func TestGetWarningsNoFilter(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityWarning, Message: "unused variable", Code: "-Wunused-variable"},
		{File: "util.cpp", Line: 20, Severity: diagnostics.SeverityWarning, Message: "implicit conversion", Code: "-Wimplicit-conversion"},
	}
	store.FinishBuild(0, time.Second, nil, warns)

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetWarnings(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp getWarningsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Count != 2 {
		t.Fatalf("expected count 2, got %d", resp.Count)
	}
	if len(resp.Warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(resp.Warnings))
	}
}

func TestGetWarningsCodeFilter(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityWarning, Message: "unused variable", Code: "-Wunused-variable"},
		{File: "util.cpp", Line: 20, Severity: diagnostics.SeverityWarning, Message: "implicit conversion", Code: "-Wimplicit-conversion"},
	}
	store.FinishBuild(0, time.Second, nil, warns)

	// "-Wunused" should match "-Wunused-variable" via substring.
	req := makeCallToolRequest(map[string]interface{}{"filter": "-Wunused"})
	result, err := srv.handleGetWarnings(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp getWarningsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("expected count 1, got %d", resp.Count)
	}
	if resp.Warnings[0].Code != "-Wunused-variable" {
		t.Fatalf("expected code -Wunused-variable, got %s", resp.Warnings[0].Code)
	}
}

func TestGetWarningsFileFilter(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "src/core/foo.cpp", Line: 10, Severity: diagnostics.SeverityWarning, Message: "unused variable", Code: "-Wunused"},
		{File: "src/gui/bar.cpp", Line: 20, Severity: diagnostics.SeverityWarning, Message: "implicit conversion", Code: "-Wimplicit"},
	}
	store.FinishBuild(0, time.Second, nil, warns)

	// "src/core" should match "src/core/foo.cpp" via file substring.
	req := makeCallToolRequest(map[string]interface{}{"filter": "src/core"})
	result, err := srv.handleGetWarnings(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp getWarningsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("expected count 1, got %d", resp.Count)
	}
	if resp.Warnings[0].File != "src/core/foo.cpp" {
		t.Fatalf("expected file src/core/foo.cpp, got %s", resp.Warnings[0].File)
	}
}

func TestGetWarningsNoMatches(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityWarning, Message: "unused", Code: "-Wunused"},
	}
	store.FinishBuild(0, time.Second, nil, warns)

	req := makeCallToolRequest(map[string]interface{}{"filter": "nonexistent"})
	result, err := srv.handleGetWarnings(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp getWarningsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Count != 0 {
		t.Fatalf("expected count 0, got %d", resp.Count)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d", len(resp.Warnings))
	}
}

func TestGetWarningsEmptyState(t *testing.T) {
	fb := &fakeBuilder{}
	srv, _ := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetWarnings(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp getWarningsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Count != 0 {
		t.Fatalf("expected count 0, got %d", resp.Count)
	}
}

// --- configure tests ---

func TestConfigureSuccess(t *testing.T) {
	fb := &fakeBuilder{
		configureResult: &builder.BuildResult{
			ExitCode: 0,
			Stdout:   "-- Configuring done\n-- Generating done\n",
			Stderr:   "",
		},
	}
	srv, store := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp configureResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.ErrorCount != 0 {
		t.Fatalf("expected error_count 0, got %d", resp.ErrorCount)
	}

	// State should now be configured.
	if store.GetPhase() != state.PhaseConfigured {
		t.Fatalf("expected PhaseConfigured after successful configure, got %d", store.GetPhase())
	}
}

func TestConfigureFailedWithCMakeErrors(t *testing.T) {
	fb := &fakeBuilder{
		configureResult: &builder.BuildResult{
			ExitCode: 1,
			Stdout:   "",
			Stderr:   "CMake Error at CMakeLists.txt:10 (find_package):\n  Could not find package Foo\nCMake Error at CMakeLists.txt:20:\n  Missing required library\n",
		},
	}
	srv, store := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp configureResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
	if resp.ErrorCount != 2 {
		t.Fatalf("expected error_count 2, got %d", resp.ErrorCount)
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(resp.Messages))
	}

	// State should remain unconfigured.
	if store.GetPhase() != state.PhaseUnconfigured {
		t.Fatalf("expected PhaseUnconfigured after failed configure, got %d", store.GetPhase())
	}
}

func TestConfigurePassesArgs(t *testing.T) {
	fb := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	srv, _ := newTestServer(fb)

	req := makeCallToolRequest(map[string]interface{}{
		"cmake_args": []interface{}{"-DCMAKE_BUILD_TYPE=Debug", "-DFOO=bar"},
	})
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if len(fb.lastConfigureArgs) != 2 {
		t.Fatalf("expected 2 configure args, got %v", fb.lastConfigureArgs)
	}
	if fb.lastConfigureArgs[0] != "-DCMAKE_BUILD_TYPE=Debug" {
		t.Fatalf("expected first arg -DCMAKE_BUILD_TYPE=Debug, got %s", fb.lastConfigureArgs[0])
	}
}

// --- clean tests ---

func TestCleanSuccess(t *testing.T) {
	fb := &fakeBuilder{
		cleanResult: &builder.BuildResult{ExitCode: 0},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// First, perform a build so we are in PhaseBuilt.
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	store.FinishBuild(0, time.Second, nil, nil)
	if store.GetPhase() != state.PhaseBuilt {
		t.Fatalf("expected PhaseBuilt, got %d", store.GetPhase())
	}

	req := makeCallToolRequest(nil)
	result, err := srv.handleClean(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp cleanResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if resp.Message != "Clean complete" {
		t.Fatalf("expected message 'Clean complete', got %q", resp.Message)
	}

	// State should be back to configured.
	if store.GetPhase() != state.PhaseConfigured {
		t.Fatalf("expected PhaseConfigured after clean, got %d", store.GetPhase())
	}
}

func TestCleanWhenNotBuilt(t *testing.T) {
	fb := &fakeBuilder{
		cleanResult: &builder.BuildResult{ExitCode: 0},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleClean(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp cleanResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
}

func TestCleanFailure(t *testing.T) {
	fb := &fakeBuilder{
		cleanErr: context.DeadlineExceeded,
	}
	srv, _ := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleClean(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error when clean fails")
	}
}

// --- get_changed_files tests ---

func TestGetChangedFilesHandler(t *testing.T) {
	// This tests the handler wiring with the store's LastSuccessfulBuildTime.
	// The actual change detection is tested in changes/detector_test.go.
	fb := &fakeBuilder{}
	srv, _ := newTestServer(fb)
	// Use a temp dir as source so we can control file layout.
	tmpDir := t.TempDir()
	srv.registry.defaultInstance().cfg.SourceDir = tmpDir
	srv.registry.defaultInstance().cfg.BuildDir = tmpDir + "/build"

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetChangedFiles(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp changedFilesResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	// Method should be either "git" or "mtime".
	if resp.Method != "git" && resp.Method != "mtime" {
		t.Fatalf("expected method git or mtime, got %q", resp.Method)
	}
}

// --- get_build_graph tests ---

func TestGetBuildGraphMissingFile(t *testing.T) {
	fb := &fakeBuilder{}
	srv, _ := newTestServer(fb)
	tmpDir := t.TempDir()
	srv.registry.defaultInstance().cfg.BuildDir = tmpDir + "/nonexistent-build"
	srv.registry.defaultInstance().cfg.SourceDir = tmpDir

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetBuildGraph(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	text := extractText(t, result)
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if raw["available"].(bool) {
		t.Fatal("expected available=false when compile_commands.json is missing")
	}
	reason, ok := raw["reason"].(string)
	if !ok || !strings.Contains(reason, "not found") {
		t.Fatalf("expected reason to mention 'not found', got %q", reason)
	}
}

// --- parseCMakeMessages tests ---

func TestParseCMakeMessagesEmpty(t *testing.T) {
	messages, errorCount := parseCMakeMessages("")
	if errorCount != 0 {
		t.Fatalf("expected 0 errors, got %d", errorCount)
	}
	if len(messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(messages))
	}
}

func TestParseCMakeMessagesWithErrors(t *testing.T) {
	output := "CMake Error at CMakeLists.txt:10:\n  Missing package\nCMake Warning at CMakeLists.txt:5:\n  Deprecated option\nCMake Error at CMakeLists.txt:20:\n  Invalid setting\n"
	messages, errorCount := parseCMakeMessages(output)
	if errorCount != 2 {
		t.Fatalf("expected 2 errors, got %d", errorCount)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
}

// --- suggest_fix tests ---

func TestSuggestFixValidIndex(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Create a temp source file with 25 lines.
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "test.cpp")
	var lines []string
	for i := 1; i <= 25; i++ {
		lines = append(lines, fmt.Sprintf("// line %d", i))
	}
	if err := os.WriteFile(srcFile, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	// Populate store with an error pointing to the file, line 15.
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: srcFile, Line: 15, Column: 3, Severity: diagnostics.SeverityError, Message: "undeclared identifier"},
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(map[string]interface{}{"error_index": float64(0)})
	result, err := srv.handleSuggestFix(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp suggestFixResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.File != srcFile {
		t.Fatalf("expected file %s, got %s", srcFile, resp.File)
	}
	if resp.StartLine != 5 {
		t.Fatalf("expected start_line 5, got %d", resp.StartLine)
	}
	if resp.EndLine != 25 {
		t.Fatalf("expected end_line 25, got %d", resp.EndLine)
	}
	if resp.Diagnostic.Line != 15 {
		t.Fatalf("expected diagnostic line 15, got %d", resp.Diagnostic.Line)
	}
	if resp.Diagnostic.Message != "undeclared identifier" {
		t.Fatalf("expected diagnostic message 'undeclared identifier', got %s", resp.Diagnostic.Message)
	}
	// Source should contain line 15.
	if !strings.Contains(resp.Source, "// line 15") {
		t.Fatalf("expected source to contain '// line 15', got %q", resp.Source)
	}
}

func TestSuggestFixOutOfBounds(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Populate store with 1 error.
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityError, Message: "error"},
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(map[string]interface{}{"error_index": float64(5)})
	result, err := srv.handleSuggestFix(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for out of bounds index")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "out of range") {
		t.Fatalf("expected 'out of range' in error message, got %q", text)
	}
}

func TestSuggestFixFileNotFound(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Populate store with an error pointing to a nonexistent file.
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "/nonexistent/path/to/file.cpp", Line: 10, Severity: diagnostics.SeverityError, Message: "error"},
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(map[string]interface{}{"error_index": float64(0)})
	result, err := srv.handleSuggestFix(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for file not found")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "cannot read source file") {
		t.Fatalf("expected 'cannot read source file' in error message, got %q", text)
	}
}

func TestSuggestFixNearStartOfFile(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Create a temp source file with 25 lines.
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "start.cpp")
	var lines []string
	for i := 1; i <= 25; i++ {
		lines = append(lines, fmt.Sprintf("// line %d", i))
	}
	if err := os.WriteFile(srcFile, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	// Error on line 2 — start_line should clamp to 1.
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: srcFile, Line: 2, Severity: diagnostics.SeverityError, Message: "error near start"},
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(map[string]interface{}{"error_index": float64(0)})
	result, err := srv.handleSuggestFix(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp suggestFixResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.StartLine != 1 {
		t.Fatalf("expected start_line to clamp to 1, got %d", resp.StartLine)
	}
	if resp.EndLine != 12 {
		t.Fatalf("expected end_line 12, got %d", resp.EndLine)
	}
}

func TestSuggestFixNearEndOfFile(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	// Create a temp source file with 15 lines.
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "end.cpp")
	var lines []string
	for i := 1; i <= 15; i++ {
		lines = append(lines, fmt.Sprintf("// line %d", i))
	}
	if err := os.WriteFile(srcFile, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	// Error on last line (15) — end_line should clamp to 15.
	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: srcFile, Line: 15, Severity: diagnostics.SeverityError, Message: "error near end"},
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(map[string]interface{}{"error_index": float64(0)})
	result, err := srv.handleSuggestFix(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp suggestFixResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.StartLine != 5 {
		t.Fatalf("expected start_line 5, got %d", resp.StartLine)
	}
	if resp.EndLine != 15 {
		t.Fatalf("expected end_line to clamp to 15, got %d", resp.EndLine)
	}
	// Source should contain the last line.
	if !strings.Contains(resp.Source, "// line 15") {
		t.Fatalf("expected source to contain '// line 15', got %q", resp.Source)
	}
}

// --- parseFilesCompiled tests ---

func TestParseFilesCompiledNinja(t *testing.T) {
	stderr := "[1/5] Building CXX object main.cpp.o\n[2/5] Building CXX object util.cpp.o\n[3/5] Building CXX object lib.cpp.o\n[4/5] Linking CXX executable app\n[5/5] Finished\n"
	got := parseFilesCompiled(stderr)
	if got != 5 {
		t.Fatalf("expected 5 files compiled, got %d", got)
	}
}

func TestParseFilesCompiledEmpty(t *testing.T) {
	got := parseFilesCompiled("")
	if got != 0 {
		t.Fatalf("expected 0 files compiled, got %d", got)
	}
}

func TestParseFilesCompiledMake(t *testing.T) {
	stderr := "gcc -c -o main.o main.cpp\ng++ -c -o util.o util.cpp\nclang -c -o lib.o lib.cpp\nlinking app\n"
	got := parseFilesCompiled(stderr)
	if got != 3 {
		t.Fatalf("expected 3 files compiled, got %d", got)
	}
}

func TestParseFilesCompiledNinjaCacheHit(t *testing.T) {
	// All targets cached — no progress lines.
	stderr := "ninja: no work to do.\n"
	got := parseFilesCompiled(stderr)
	if got != 0 {
		t.Fatalf("expected 0 files compiled for cache hit, got %d", got)
	}
}

// --- list_configs tests ---

func TestListConfigsDefault(t *testing.T) {
	srv, _ := newTestServer(&fakeBuilder{})

	req := makeCallToolRequest(nil)
	result, err := srv.handleListConfigs(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp listConfigsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.DefaultConfig != "default" {
		t.Fatalf("expected default_config 'default', got %q", resp.DefaultConfig)
	}
	if len(resp.Configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(resp.Configs))
	}
	if resp.Configs[0].Name != "default" {
		t.Fatalf("expected config name 'default', got %q", resp.Configs[0].Name)
	}
	if resp.Configs[0].BuildDir != "build" {
		t.Fatalf("expected build_dir 'build', got %q", resp.Configs[0].BuildDir)
	}
	if resp.Configs[0].Status != "unconfigured" {
		t.Fatalf("expected status 'unconfigured', got %q", resp.Configs[0].Status)
	}
}

func TestBuildWithExplicitConfigParam(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{ExitCode: 0, Duration: time.Second},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(map[string]interface{}{"config": "default"})
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp buildResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Fatalf("expected exit_code 0, got %d", resp.ExitCode)
	}
}

func TestBuildWithNonexistentConfig(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{ExitCode: 0, Duration: time.Second},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(map[string]interface{}{"config": "nonexistent"})
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for nonexistent config")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "unknown configuration") {
		t.Fatalf("expected 'unknown configuration' in error, got %q", text)
	}
	if !strings.Contains(text, "default") {
		t.Fatalf("expected available config 'default' in error, got %q", text)
	}
}

// newMultiTestServer creates an mcpServer with multiple named fakeBuilder
// instances. Each entry in configs maps a config name to a fakeBuilder.
// The defaultName selects which config is the default.
func newMultiTestServer(configs map[string]*fakeBuilder, defaultName string) *mcpServer {
	registry := newConfigRegistry(defaultName)
	for name, fb := range configs {
		inst := &configInstance{
			name:    name,
			cfg:     &config.Config{BuildDir: "build/" + name},
			builder: fb,
			store:   state.NewStore(),
		}
		registry.add(inst)
	}
	return &mcpServer{registry: registry}
}

func TestMultiConfigListConfigs(t *testing.T) {
	srv := newMultiTestServer(map[string]*fakeBuilder{
		"debug":   {},
		"release": {},
	}, "debug")

	req := makeCallToolRequest(nil)
	result, err := srv.handleListConfigs(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp listConfigsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.DefaultConfig != "debug" {
		t.Fatalf("expected default_config %q, got %q", "debug", resp.DefaultConfig)
	}
	if len(resp.Configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(resp.Configs))
	}

	// Configs should be sorted alphabetically.
	if resp.Configs[0].Name != "debug" {
		t.Fatalf("expected first config name %q, got %q", "debug", resp.Configs[0].Name)
	}
	if resp.Configs[1].Name != "release" {
		t.Fatalf("expected second config name %q, got %q", "release", resp.Configs[1].Name)
	}

	// Verify build_dir values.
	if resp.Configs[0].BuildDir != "build/debug" {
		t.Fatalf("expected build_dir %q, got %q", "build/debug", resp.Configs[0].BuildDir)
	}
	if resp.Configs[1].BuildDir != "build/release" {
		t.Fatalf("expected build_dir %q, got %q", "build/release", resp.Configs[1].BuildDir)
	}

	// Both should be unconfigured initially.
	for _, cs := range resp.Configs {
		if cs.Status != "unconfigured" {
			t.Fatalf("expected status %q for %q, got %q", "unconfigured", cs.Name, cs.Status)
		}
	}
}

func TestMultiConfigConfigureOneDoesNotAffectOther(t *testing.T) {
	srv := newMultiTestServer(map[string]*fakeBuilder{
		"debug": {
			configureResult: &builder.BuildResult{ExitCode: 0},
		},
		"release": {
			configureResult: &builder.BuildResult{ExitCode: 0},
		},
	}, "debug")

	// Configure only the release config.
	req := makeCallToolRequest(map[string]interface{}{"config": "release"})
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	// Check list_configs: release should be configured, debug should still be unconfigured.
	listReq := makeCallToolRequest(nil)
	listResult, err := srv.handleListConfigs(context.Background(), listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp listConfigsResponse
	text := extractText(t, listResult)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	for _, cs := range resp.Configs {
		switch cs.Name {
		case "debug":
			if cs.Status != "unconfigured" {
				t.Fatalf("expected debug status %q, got %q", "unconfigured", cs.Status)
			}
		case "release":
			if cs.Status != "configured" {
				t.Fatalf("expected release status %q, got %q", "configured", cs.Status)
			}
		default:
			t.Fatalf("unexpected config name %q", cs.Name)
		}
	}
}

func TestMultiConfigDefaultRouting(t *testing.T) {
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	srv := newMultiTestServer(map[string]*fakeBuilder{
		"debug":   debugFB,
		"release": releaseFB,
	}, "debug")

	// Call configure with no config param — should route to default (debug).
	req := makeCallToolRequest(nil)
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if releaseFB.lastConfigureArgs != nil {
		t.Fatal("release builder Configure() was called when routing to default (debug)")
	}

	// Verify debug is configured, release is not.
	listReq := makeCallToolRequest(nil)
	listResult, err := srv.handleListConfigs(context.Background(), listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp listConfigsResponse
	text := extractText(t, listResult)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	for _, cs := range resp.Configs {
		switch cs.Name {
		case "debug":
			if cs.Status != "configured" {
				t.Fatalf("expected debug status %q after default route, got %q", "configured", cs.Status)
			}
		case "release":
			if cs.Status != "unconfigured" {
				t.Fatalf("expected release status %q, got %q", "unconfigured", cs.Status)
			}
		default:
			t.Fatalf("unexpected config name %q", cs.Name)
		}
	}
}

func TestMultiConfigPresetFieldIntegration(t *testing.T) {
	// Load a multi-config JSON file where one config has a preset field,
	// verify Config.Preset is populated, list_configs returns both configs,
	// and configure dispatches to the correct builder.
	dir := t.TempDir()
	cfgJSON := `{
		"source_dir": ".",
		"generator": "ninja",
		"configs": {
			"debug": {
				"build_dir": "build/debug",
				"preset": "debug"
			},
			"release": {
				"build_dir": "build/release"
			}
		},
		"default_config": "debug"
	}`
	cfgPath := filepath.Join(dir, ".cpp-build-mcp.json")
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	configs, defaultName, err := config.LoadMulti(dir)
	if err != nil {
		t.Fatalf("LoadMulti() returned error: %v", err)
	}

	// Verify Config.Preset is populated on the debug config.
	if configs["debug"].Preset != "debug" {
		t.Errorf("debug.Preset: got %q, want %q", configs["debug"].Preset, "debug")
	}
	// Release should have no preset (empty string).
	if configs["release"].Preset != "" {
		t.Errorf("release.Preset: got %q, want empty", configs["release"].Preset)
	}
	if defaultName != "debug" {
		t.Errorf("defaultName: got %q, want %q", defaultName, "debug")
	}

	// Create an mcpServer with fakeBuilder instances using configs from LoadMulti.
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	registry := newConfigRegistry(defaultName)
	for name, cfg := range configs {
		var fb *fakeBuilder
		if name == "debug" {
			fb = debugFB
		} else {
			fb = releaseFB
		}
		registry.add(&configInstance{
			name:    name,
			cfg:     cfg,
			builder: fb,
			store:   state.NewStore(),
		})
	}
	srv := &mcpServer{registry: registry}

	// Call list_configs — both configs should appear.
	listReq := makeCallToolRequest(nil)
	listResult, err := srv.handleListConfigs(context.Background(), listReq)
	if err != nil {
		t.Fatalf("handleListConfigs unexpected error: %v", err)
	}
	if listResult.IsError {
		t.Fatalf("handleListConfigs tool error: %s", extractText(t, listResult))
	}

	var resp listConfigsResponse
	text := extractText(t, listResult)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal list_configs response: %v", err)
	}
	if len(resp.Configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(resp.Configs))
	}
	if resp.DefaultConfig != "debug" {
		t.Fatalf("expected default_config %q, got %q", "debug", resp.DefaultConfig)
	}

	// Verify config names (sorted).
	if resp.Configs[0].Name != "debug" || resp.Configs[1].Name != "release" {
		t.Fatalf("expected config names [debug, release], got [%s, %s]",
			resp.Configs[0].Name, resp.Configs[1].Name)
	}

	// Call configure with config: "debug" — verify dispatch via store state.
	configReq := makeCallToolRequest(map[string]interface{}{"config": "debug"})
	configResult, err := srv.handleConfigure(context.Background(), configReq)
	if err != nil {
		t.Fatalf("handleConfigure unexpected error: %v", err)
	}
	if configResult.IsError {
		t.Fatalf("handleConfigure tool error: %s", extractText(t, configResult))
	}

	// Verify release builder was NOT called.
	if releaseFB.lastConfigureArgs != nil {
		t.Fatal("release builder Configure() was called when routing to debug")
	}

	// Re-check list_configs: debug should now be configured, release should
	// remain unconfigured — proving configure dispatched to the debug builder.
	listReq2 := makeCallToolRequest(nil)
	listResult2, err := srv.handleListConfigs(context.Background(), listReq2)
	if err != nil {
		t.Fatalf("handleListConfigs unexpected error: %v", err)
	}

	var resp2 listConfigsResponse
	text2 := extractText(t, listResult2)
	if err := json.Unmarshal([]byte(text2), &resp2); err != nil {
		t.Fatalf("failed to unmarshal list_configs response: %v", err)
	}

	for _, cs := range resp2.Configs {
		switch cs.Name {
		case "debug":
			if cs.Status != "configured" {
				t.Fatalf("expected debug status %q after configure, got %q", "configured", cs.Status)
			}
		case "release":
			if cs.Status != "unconfigured" {
				t.Fatalf("expected release status %q (not dispatched to), got %q", "unconfigured", cs.Status)
			}
		default:
			t.Fatalf("unexpected config name %q", cs.Name)
		}
	}
}

func TestMultiConfigPresetWithBuildDirRouting(t *testing.T) {
	// Verify that a config entry with both preset and build_dir routes correctly.
	// The preset+build_dir coexistence on Config is tested in config_test.go;
	// this test verifies that the routing layer works with such a config.
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	registry := newConfigRegistry("debug")
	registry.add(&configInstance{
		name: "debug",
		cfg: &config.Config{
			BuildDir: "build/debug",
			Preset:   "debug",
		},
		builder: debugFB,
		store:   state.NewStore(),
	})
	registry.add(&configInstance{
		name: "release",
		cfg: &config.Config{
			BuildDir: "build/release",
			Preset:   "release",
		},
		builder: releaseFB,
		store:   state.NewStore(),
	})
	srv := &mcpServer{registry: registry}

	// Configure release by name — should dispatch to releaseFB only.
	req := makeCallToolRequest(map[string]interface{}{"config": "release"})
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	// Verify list_configs shows the build_dir values and that only release
	// is configured (proving dispatch went to the release builder).
	listReq := makeCallToolRequest(nil)
	listResult, err := srv.handleListConfigs(context.Background(), listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp listConfigsResponse
	text := extractText(t, listResult)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	for _, cs := range resp.Configs {
		switch cs.Name {
		case "debug":
			if cs.BuildDir != "build/debug" {
				t.Errorf("debug.BuildDir: got %q, want %q", cs.BuildDir, "build/debug")
			}
			if cs.Status != "unconfigured" {
				t.Errorf("debug.Status: got %q, want %q", cs.Status, "unconfigured")
			}
		case "release":
			if cs.BuildDir != "build/release" {
				t.Errorf("release.BuildDir: got %q, want %q", cs.BuildDir, "build/release")
			}
			if cs.Status != "configured" {
				t.Errorf("release.Status: got %q, want %q", cs.Status, "configured")
			}
		default:
			t.Errorf("unexpected config name %q", cs.Name)
		}
	}
}

func TestSingleConfigPresetRouting(t *testing.T) {
	// Verify single-config mode with preset field routes to the default builder.
	// Config parsing for single-config preset is tested in config_test.go;
	// this test verifies the routing layer works correctly with such a config.
	fb := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	registry := newConfigRegistry("default")
	registry.add(&configInstance{
		name: "default",
		cfg: &config.Config{
			BuildDir: "out",
			Preset:   "mypreset",
		},
		builder: fb,
		store:   state.NewStore(),
	})
	srv := &mcpServer{registry: registry}

	// Call configure with no config param — should route to the default.
	req := makeCallToolRequest(nil)
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	// Verify list_configs shows one config with the correct build_dir and
	// status "configured" (proving configure was dispatched to the builder).
	listReq := makeCallToolRequest(nil)
	listResult, err := srv.handleListConfigs(context.Background(), listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp listConfigsResponse
	text := extractText(t, listResult)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(resp.Configs))
	}
	if resp.Configs[0].Name != "default" {
		t.Errorf("config name: got %q, want %q", resp.Configs[0].Name, "default")
	}
	if resp.Configs[0].BuildDir != "out" {
		t.Errorf("config build_dir: got %q, want %q", resp.Configs[0].BuildDir, "out")
	}
	if resp.DefaultConfig != "default" {
		t.Errorf("default_config: got %q, want %q", resp.DefaultConfig, "default")
	}
	if resp.Configs[0].Status != "configured" {
		t.Errorf("config status: got %q, want %q (proving configure was dispatched)", resp.Configs[0].Status, "configured")
	}
}

// --- Preset-derived E2E tests ---

// TestPresetDerivedListConfigs verifies that starting a server with only a
// CMakePresets.json (no .cpp-build-mcp.json) produces the correct
// preset-derived configs via list_configs: correct names, build_dirs, and
// "unconfigured" status.
func TestPresetDerivedListConfigs(t *testing.T) {
	dir := t.TempDir()

	presetsJSON := `{
		"version": 3,
		"configurePresets": [
			{
				"name": "debug",
				"binaryDir": "${sourceDir}/build/debug",
				"generator": "Ninja"
			},
			{
				"name": "release",
				"binaryDir": "${sourceDir}/build/release",
				"generator": "Ninja"
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "CMakePresets.json"), []byte(presetsJSON), 0o644); err != nil {
		t.Fatalf("failed to write CMakePresets.json: %v", err)
	}

	configs, defaultName, err := config.LoadMulti(dir)
	if err != nil {
		t.Fatalf("LoadMulti() returned error: %v", err)
	}

	// Build an mcpServer from the preset-derived configs.
	builders := make(map[string]*fakeBuilder, len(configs))
	registry := newConfigRegistry(defaultName)
	for name, cfg := range configs {
		fb := &fakeBuilder{
			configureResult: &builder.BuildResult{ExitCode: 0},
		}
		builders[name] = fb
		registry.add(&configInstance{
			name:    name,
			cfg:     cfg,
			builder: fb,
			store:   state.NewStore(),
		})
	}
	srv := &mcpServer{registry: registry}

	// Call list_configs.
	req := makeCallToolRequest(nil)
	result, err := srv.handleListConfigs(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp listConfigsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Should have two configs.
	if len(resp.Configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(resp.Configs))
	}

	// Default should be the alphabetically first preset name.
	if resp.DefaultConfig != "debug" {
		t.Fatalf("expected default_config %q, got %q", "debug", resp.DefaultConfig)
	}

	// Configs should be sorted alphabetically: debug, release.
	if resp.Configs[0].Name != "debug" {
		t.Fatalf("expected first config name %q, got %q", "debug", resp.Configs[0].Name)
	}
	if resp.Configs[1].Name != "release" {
		t.Fatalf("expected second config name %q, got %q", "release", resp.Configs[1].Name)
	}

	// Verify build_dir values come from the preset binaryDir (expanded).
	expectedDebugDir := filepath.Join(dir, "build/debug")
	expectedReleaseDir := filepath.Join(dir, "build/release")
	if resp.Configs[0].BuildDir != expectedDebugDir {
		t.Fatalf("expected debug build_dir %q, got %q", expectedDebugDir, resp.Configs[0].BuildDir)
	}
	if resp.Configs[1].BuildDir != expectedReleaseDir {
		t.Fatalf("expected release build_dir %q, got %q", expectedReleaseDir, resp.Configs[1].BuildDir)
	}

	// Both should be unconfigured.
	for _, cs := range resp.Configs {
		if cs.Status != "unconfigured" {
			t.Fatalf("expected status %q for %q, got %q", "unconfigured", cs.Name, cs.Status)
		}
	}

	// Verify that the Config.Preset field is populated for each config.
	if configs["debug"].Preset != "debug" {
		t.Errorf("debug.Preset: got %q, want %q", configs["debug"].Preset, "debug")
	}
	if configs["release"].Preset != "release" {
		t.Errorf("release.Preset: got %q, want %q", configs["release"].Preset, "release")
	}
}

// TestPresetDerivedConfigureDispatch verifies that calling configure with a
// preset-derived config name dispatches to the correct fakeBuilder and that
// Config.Preset is "debug".
func TestPresetDerivedConfigureDispatch(t *testing.T) {
	dir := t.TempDir()

	presetsJSON := `{
		"version": 3,
		"configurePresets": [
			{
				"name": "debug",
				"binaryDir": "${sourceDir}/build/debug",
				"generator": "Ninja"
			},
			{
				"name": "release",
				"binaryDir": "${sourceDir}/build/release",
				"generator": "Ninja"
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "CMakePresets.json"), []byte(presetsJSON), 0o644); err != nil {
		t.Fatalf("failed to write CMakePresets.json: %v", err)
	}

	configs, defaultName, err := config.LoadMulti(dir)
	if err != nil {
		t.Fatalf("LoadMulti() returned error: %v", err)
	}

	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	registry := newConfigRegistry(defaultName)
	for name, cfg := range configs {
		var fb *fakeBuilder
		if name == "debug" {
			fb = debugFB
		} else {
			fb = releaseFB
		}
		registry.add(&configInstance{
			name:    name,
			cfg:     cfg,
			builder: fb,
			store:   state.NewStore(),
		})
	}
	srv := &mcpServer{registry: registry}

	// Verify Config.Preset is "debug" before calling configure.
	if configs["debug"].Preset != "debug" {
		t.Fatalf("expected Config.Preset %q, got %q", "debug", configs["debug"].Preset)
	}

	// Call configure(config: "debug").
	req := makeCallToolRequest(map[string]interface{}{"config": "debug"})
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp configureResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected configure success=true")
	}

	// Verify the debug fakeBuilder was called (Configure dispatched to it).
	if !debugFB.configureCalled {
		t.Fatal("debug builder Configure() was not called")
	}

	// Verify release builder was NOT called.
	if releaseFB.configureCalled {
		t.Fatal("release builder Configure() was called when dispatching to debug")
	}

	// Verify list_configs shows debug as configured, release as unconfigured.
	listReq := makeCallToolRequest(nil)
	listResult, err := srv.handleListConfigs(context.Background(), listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var listResp listConfigsResponse
	listText := extractText(t, listResult)
	if err := json.Unmarshal([]byte(listText), &listResp); err != nil {
		t.Fatalf("failed to unmarshal list_configs response: %v", err)
	}

	for _, cs := range listResp.Configs {
		switch cs.Name {
		case "debug":
			if cs.Status != "configured" {
				t.Fatalf("expected debug status %q after configure, got %q", "configured", cs.Status)
			}
		case "release":
			if cs.Status != "unconfigured" {
				t.Fatalf("expected release status %q, got %q", "unconfigured", cs.Status)
			}
		default:
			t.Fatalf("unexpected config name %q", cs.Name)
		}
	}
}

// TestPresetDerivedHybridBuildTimeout verifies that when both CMakePresets.json
// and .cpp-build-mcp.json exist, preset-derived configs inherit the top-level
// build_timeout override from the config file.
func TestPresetDerivedHybridBuildTimeout(t *testing.T) {
	dir := t.TempDir()

	presetsJSON := `{
		"version": 3,
		"configurePresets": [
			{
				"name": "debug",
				"binaryDir": "${sourceDir}/build/debug",
				"generator": "Ninja"
			},
			{
				"name": "release",
				"binaryDir": "${sourceDir}/build/release",
				"generator": "Ninja"
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "CMakePresets.json"), []byte(presetsJSON), 0o644); err != nil {
		t.Fatalf("failed to write CMakePresets.json: %v", err)
	}

	configJSON := `{
		"build_timeout": "10m"
	}`
	if err := os.WriteFile(filepath.Join(dir, ".cpp-build-mcp.json"), []byte(configJSON), 0o644); err != nil {
		t.Fatalf("failed to write .cpp-build-mcp.json: %v", err)
	}

	configs, defaultName, err := config.LoadMulti(dir)
	if err != nil {
		t.Fatalf("LoadMulti() returned error: %v", err)
	}

	// Verify we got two preset-derived configs.
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}

	// Verify default config is alphabetically first.
	if defaultName != "debug" {
		t.Fatalf("expected default_config %q, got %q", "debug", defaultName)
	}

	// Verify both configs have build_timeout of 10 minutes (from .cpp-build-mcp.json override).
	expectedTimeout := 10 * time.Minute
	for name, cfg := range configs {
		if cfg.BuildTimeout != expectedTimeout {
			t.Errorf("config %q: expected BuildTimeout %v, got %v", name, expectedTimeout, cfg.BuildTimeout)
		}
	}

	// Verify preset-derived fields were NOT overridden by the config file
	// (build_dir, generator, preset are preserved from presets).
	expectedDebugDir := filepath.Join(dir, "build/debug")
	expectedReleaseDir := filepath.Join(dir, "build/release")
	if configs["debug"].BuildDir != expectedDebugDir {
		t.Errorf("debug.BuildDir: got %q, want %q", configs["debug"].BuildDir, expectedDebugDir)
	}
	if configs["release"].BuildDir != expectedReleaseDir {
		t.Errorf("release.BuildDir: got %q, want %q", configs["release"].BuildDir, expectedReleaseDir)
	}
	if configs["debug"].Preset != "debug" {
		t.Errorf("debug.Preset: got %q, want %q", configs["debug"].Preset, "debug")
	}
	if configs["release"].Preset != "release" {
		t.Errorf("release.Preset: got %q, want %q", configs["release"].Preset, "release")
	}
	if configs["debug"].Generator != "ninja" {
		t.Errorf("debug.Generator: got %q, want %q", configs["debug"].Generator, "ninja")
	}
	if configs["release"].Generator != "ninja" {
		t.Errorf("release.Generator: got %q, want %q", configs["release"].Generator, "ninja")
	}

	// Also verify via the server layer: build an mcpServer and check list_configs.
	registry := newConfigRegistry(defaultName)
	for name, cfg := range configs {
		fb := &fakeBuilder{
			configureResult: &builder.BuildResult{ExitCode: 0},
		}
		registry.add(&configInstance{
			name:    name,
			cfg:     cfg,
			builder: fb,
			store:   state.NewStore(),
		})
	}
	srv := &mcpServer{registry: registry}

	req := makeCallToolRequest(nil)
	result, err := srv.handleListConfigs(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp listConfigsResponse
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(resp.Configs))
	}
	if resp.Configs[0].BuildDir != expectedDebugDir {
		t.Fatalf("list_configs debug build_dir: got %q, want %q", resp.Configs[0].BuildDir, expectedDebugDir)
	}
	if resp.Configs[1].BuildDir != expectedReleaseDir {
		t.Fatalf("list_configs release build_dir: got %q, want %q", resp.Configs[1].BuildDir, expectedReleaseDir)
	}
}

// --- config field in response tests ---

// TestBuildResponseContainsConfigField verifies the build response includes the
// resolved config name.
func TestBuildResponseContainsConfigField(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{ExitCode: 0, Duration: time.Second},
	}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	cfgVal, ok := raw["config"]
	if !ok {
		t.Fatal("expected 'config' field in build response JSON")
	}
	if cfgVal.(string) != "default" {
		t.Fatalf("expected config 'default', got %q", cfgVal)
	}
}

// TestGetErrorsResponseContainsConfigField verifies get_errors includes config.
func TestGetErrorsResponseContainsConfigField(t *testing.T) {
	fb := &fakeBuilder{}
	srv, _ := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetErrors(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	cfgVal, ok := raw["config"]
	if !ok {
		t.Fatal("expected 'config' field in get_errors response JSON")
	}
	if cfgVal.(string) != "default" {
		t.Fatalf("expected config 'default', got %q", cfgVal)
	}
}

// TestGetWarningsResponseContainsConfigField verifies get_warnings includes config.
func TestGetWarningsResponseContainsConfigField(t *testing.T) {
	fb := &fakeBuilder{}
	srv, _ := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetWarnings(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	cfgVal, ok := raw["config"]
	if !ok {
		t.Fatal("expected 'config' field in get_warnings response JSON")
	}
	if cfgVal.(string) != "default" {
		t.Fatalf("expected config 'default', got %q", cfgVal)
	}
}

// TestConfigureResponseContainsConfigField verifies configure includes config.
func TestConfigureResponseContainsConfigField(t *testing.T) {
	fb := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	srv, _ := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	cfgVal, ok := raw["config"]
	if !ok {
		t.Fatal("expected 'config' field in configure response JSON")
	}
	if cfgVal.(string) != "default" {
		t.Fatalf("expected config 'default', got %q", cfgVal)
	}
}

// TestCleanResponseContainsConfigField verifies clean includes config.
func TestCleanResponseContainsConfigField(t *testing.T) {
	fb := &fakeBuilder{
		cleanResult: &builder.BuildResult{ExitCode: 0},
	}
	srv, _ := newTestServer(fb)

	req := makeCallToolRequest(nil)
	result, err := srv.handleClean(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	cfgVal, ok := raw["config"]
	if !ok {
		t.Fatal("expected 'config' field in clean response JSON")
	}
	if cfgVal.(string) != "default" {
		t.Fatalf("expected config 'default', got %q", cfgVal)
	}
}

// TestGetChangedFilesResponseContainsConfigField verifies get_changed_files
// includes config.
func TestGetChangedFilesResponseContainsConfigField(t *testing.T) {
	fb := &fakeBuilder{}
	srv, _ := newTestServer(fb)
	tmpDir := t.TempDir()
	srv.registry.defaultInstance().cfg.SourceDir = tmpDir
	srv.registry.defaultInstance().cfg.BuildDir = tmpDir + "/build"

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetChangedFiles(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	cfgVal, ok := raw["config"]
	if !ok {
		t.Fatal("expected 'config' field in get_changed_files response JSON")
	}
	if cfgVal.(string) != "default" {
		t.Fatalf("expected config 'default', got %q", cfgVal)
	}
}

// TestGetBuildGraphResponseContainsConfigField verifies get_build_graph includes
// config.
func TestGetBuildGraphResponseContainsConfigField(t *testing.T) {
	fb := &fakeBuilder{}
	srv, _ := newTestServer(fb)
	tmpDir := t.TempDir()
	srv.registry.defaultInstance().cfg.BuildDir = tmpDir
	srv.registry.defaultInstance().cfg.SourceDir = tmpDir

	req := makeCallToolRequest(nil)
	result, err := srv.handleGetBuildGraph(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	cfgVal, ok := raw["config"]
	if !ok {
		t.Fatal("expected 'config' field in get_build_graph response JSON")
	}
	if cfgVal.(string) != "default" {
		t.Fatalf("expected config 'default', got %q", cfgVal)
	}
}

// TestSuggestFixResponseContainsConfigField verifies suggest_fix includes config.
func TestSuggestFixResponseContainsConfigField(t *testing.T) {
	fb := &fakeBuilder{}
	srv, store := newTestServer(fb)
	store.SetConfigured()

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "test.cpp")
	if err := os.WriteFile(srcFile, []byte("int main() {}\n"), 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	if err := store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: srcFile, Line: 1, Severity: diagnostics.SeverityError, Message: "test error"},
	}
	store.FinishBuild(1, time.Second, errs, nil)

	req := makeCallToolRequest(map[string]interface{}{"error_index": float64(0)})
	result, err := srv.handleSuggestFix(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	cfgVal, ok := raw["config"]
	if !ok {
		t.Fatal("expected 'config' field in suggest_fix response JSON")
	}
	if cfgVal.(string) != "default" {
		t.Fatalf("expected config 'default', got %q", cfgVal)
	}
}

// TestMultiConfigConfigureResponseRoutesConfigField verifies that when multiple
// configs exist, the configure response includes the correct config name.
func TestMultiConfigConfigureResponseRoutesConfigField(t *testing.T) {
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	registry := newConfigRegistry("debug")
	debugInst := &configInstance{
		name:    "debug",
		cfg:     &config.Config{BuildDir: "build/debug"},
		builder: debugFB,
		store:   state.NewStore(),
	}
	releaseInst := &configInstance{
		name:    "release",
		cfg:     &config.Config{BuildDir: "build/release"},
		builder: releaseFB,
		store:   state.NewStore(),
	}
	registry.add(debugInst)
	registry.add(releaseInst)
	srv := &mcpServer{registry: registry}

	// Configure release and verify the response says "release".
	req := makeCallToolRequest(map[string]interface{}{"config": "release"})
	result, err := srv.handleConfigure(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var raw map[string]interface{}
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if raw["config"].(string) != "release" {
		t.Fatalf("expected config 'release', got %q", raw["config"])
	}

	// Configure debug (default, no config param) and verify it says "debug".
	req2 := makeCallToolRequest(nil)
	result2, err := srv.handleConfigure(context.Background(), req2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result2))
	}

	var raw2 map[string]interface{}
	text2 := extractText(t, result2)
	if err := json.Unmarshal([]byte(text2), &raw2); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if raw2["config"].(string) != "debug" {
		t.Fatalf("expected config 'debug', got %q", raw2["config"])
	}
}

// TestBuildGraphTwoConfigsDifferentBuildDirs verifies that get_build_graph reads
// compile_commands.json from the per-config build_dir, not a hardcoded path.
func TestBuildGraphTwoConfigsDifferentBuildDirs(t *testing.T) {
	// Create two temp dirs with different compile_commands.json.
	debugDir := t.TempDir()
	releaseDir := t.TempDir()

	debugCC := `[{"directory": "/tmp", "command": "g++ -c debug.cpp", "file": "debug.cpp"}]`
	releaseCC := `[{"directory": "/tmp", "command": "g++ -c release1.cpp", "file": "release1.cpp"},
	               {"directory": "/tmp", "command": "g++ -c release2.cpp", "file": "release2.cpp"}]`

	if err := os.WriteFile(filepath.Join(debugDir, "compile_commands.json"), []byte(debugCC), 0644); err != nil {
		t.Fatalf("failed to write debug compile_commands.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseDir, "compile_commands.json"), []byte(releaseCC), 0644); err != nil {
		t.Fatalf("failed to write release compile_commands.json: %v", err)
	}

	registry := newConfigRegistry("debug")
	registry.add(&configInstance{
		name:    "debug",
		cfg:     &config.Config{BuildDir: debugDir, SourceDir: debugDir},
		builder: &fakeBuilder{},
		store:   state.NewStore(),
	})
	registry.add(&configInstance{
		name:    "release",
		cfg:     &config.Config{BuildDir: releaseDir, SourceDir: releaseDir},
		builder: &fakeBuilder{},
		store:   state.NewStore(),
	})
	srv := &mcpServer{registry: registry}

	// Query build graph for debug config.
	debugReq := makeCallToolRequest(map[string]interface{}{"config": "debug"})
	debugResult, err := srv.handleGetBuildGraph(context.Background(), debugReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if debugResult.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, debugResult))
	}

	var debugRaw map[string]interface{}
	debugText := extractText(t, debugResult)
	if err := json.Unmarshal([]byte(debugText), &debugRaw); err != nil {
		t.Fatalf("failed to unmarshal debug response: %v", err)
	}
	if debugRaw["config"].(string) != "debug" {
		t.Fatalf("expected config 'debug', got %q", debugRaw["config"])
	}
	if !debugRaw["available"].(bool) {
		t.Fatal("expected debug build graph to be available")
	}
	if int(debugRaw["file_count"].(float64)) != 1 {
		t.Fatalf("expected debug file_count 1, got %v", debugRaw["file_count"])
	}

	// Query build graph for release config.
	releaseReq := makeCallToolRequest(map[string]interface{}{"config": "release"})
	releaseResult, err := srv.handleGetBuildGraph(context.Background(), releaseReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if releaseResult.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, releaseResult))
	}

	var releaseRaw map[string]interface{}
	releaseText := extractText(t, releaseResult)
	if err := json.Unmarshal([]byte(releaseText), &releaseRaw); err != nil {
		t.Fatalf("failed to unmarshal release response: %v", err)
	}
	if releaseRaw["config"].(string) != "release" {
		t.Fatalf("expected config 'release', got %q", releaseRaw["config"])
	}
	if !releaseRaw["available"].(bool) {
		t.Fatal("expected release build graph to be available")
	}
	if int(releaseRaw["file_count"].(float64)) != 2 {
		t.Fatalf("expected release file_count 2, got %v", releaseRaw["file_count"])
	}
}

// extractText extracts the text content from a CallToolResult.
func extractText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item in result")
	}
	// Content items are typed; we need to marshal and re-parse to get text.
	data, err := json.Marshal(result.Content[0])
	if err != nil {
		t.Fatalf("failed to marshal content: %v", err)
	}
	var item struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatalf("failed to unmarshal content: %v", err)
	}
	return item.Text
}
