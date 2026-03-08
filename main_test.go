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
}

func (f *fakeBuilder) Configure(_ context.Context, args []string) (*builder.BuildResult, error) {
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

func (f *fakeBuilder) SetDirty(dirty bool) {}

// newTestServer creates an mcpServer with a fakeBuilder and fresh state store.
func newTestServer(fb *fakeBuilder) *mcpServer {
	cfg := &config.Config{
		BuildDir:  "build",
		SourceDir: ".",
		Toolchain: "auto",
		Generator: "ninja",
	}
	return &mcpServer{
		builder: fb,
		store:   state.NewStore(),
		cfg:     cfg,
	}
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
	srv := newTestServer(fb)
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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	// Start a build to mark as in-progress.
	if err := srv.store.StartBuild(); err != nil {
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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error result when build process fails to spawn")
	}

	// State should not be left with build-in-progress.
	if srv.store.IsBuilding() {
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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	// Phase should be PhaseBuilt after build completes.
	if srv.store.GetPhase() != state.PhaseBuilt {
		t.Fatalf("expected PhaseBuilt, got %d", srv.store.GetPhase())
	}
	// Build should no longer be in progress.
	if srv.store.IsBuilding() {
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
	srv := newTestServer(fb)
	srv.store.SetConfigured()
	srv.store.SetDirty()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if srv.store.IsDirty() {
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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if !srv.store.IsDirty() {
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
	srv := newTestServer(fb)
	srv.store.SetConfigured()
	srv.store.SetDirty()

	req := makeCallToolRequest(nil)
	result, err := srv.handleBuild(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	if !srv.store.IsDirty() {
		t.Fatal("expected dirty flag to remain set after failed build")
	}
}

func TestGetErrorsNoErrors(t *testing.T) {
	fb := &fakeBuilder{}
	srv := newTestServer(fb)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	// Simulate a build that produced errors.
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Column: 5, Severity: diagnostics.SeverityError, Message: "undeclared identifier 'x'", Code: "undeclared_var"},
		{File: "util.cpp", Line: 20, Severity: diagnostics.SeverityError, Message: "missing semicolon"},
	}
	srv.store.FinishBuild(1, time.Second, errs, nil)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

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

	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv.store.FinishBuild(1, time.Second, errs, nil)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	// Create an error with minimal fields — column=0, code="" should be omitted.
	errs := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 1, Severity: diagnostics.SeverityError, Message: "fail"},
	}

	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv.store.FinishBuild(1, time.Second, errs, nil)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

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
	srv := newTestServer(&fakeBuilder{})
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
	srv := newTestServer(&fakeBuilder{})
	srv.store.SetConfigured()
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
	srv := newTestServer(&fakeBuilder{})
	srv.store.SetConfigured()
	srv.store.SetDirty()
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
	srv := newTestServer(&fakeBuilder{})
	srv.store.SetConfigured()
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv.store.FinishBuild(0, time.Second, nil, nil)

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
	srv := newTestServer(&fakeBuilder{})
	srv.store.SetConfigured()
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{{Severity: diagnostics.SeverityError, Message: "fail"}}
	srv.store.FinishBuild(1, time.Second, errs, nil)

	result, err := srv.handleBuildHealth(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tc := result[0].(mcp.TextResourceContents)
	if !strings.Contains(tc.Text, "FAIL") {
		t.Fatalf("expected FAIL in health text, got %q", tc.Text)
	}
}

// --- get_warnings tests ---

func TestGetWarningsNoFilter(t *testing.T) {
	fb := &fakeBuilder{}
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityWarning, Message: "unused variable", Code: "-Wunused-variable"},
		{File: "util.cpp", Line: 20, Severity: diagnostics.SeverityWarning, Message: "implicit conversion", Code: "-Wimplicit-conversion"},
	}
	srv.store.FinishBuild(0, time.Second, nil, warns)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityWarning, Message: "unused variable", Code: "-Wunused-variable"},
		{File: "util.cpp", Line: 20, Severity: diagnostics.SeverityWarning, Message: "implicit conversion", Code: "-Wimplicit-conversion"},
	}
	srv.store.FinishBuild(0, time.Second, nil, warns)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "src/core/foo.cpp", Line: 10, Severity: diagnostics.SeverityWarning, Message: "unused variable", Code: "-Wunused"},
		{File: "src/gui/bar.cpp", Line: 20, Severity: diagnostics.SeverityWarning, Message: "implicit conversion", Code: "-Wimplicit"},
	}
	srv.store.FinishBuild(0, time.Second, nil, warns)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	warns := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityWarning, Message: "unused", Code: "-Wunused"},
	}
	srv.store.FinishBuild(0, time.Second, nil, warns)

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
	srv := newTestServer(fb)

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
	srv := newTestServer(fb)

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
	if srv.store.GetPhase() != state.PhaseConfigured {
		t.Fatalf("expected PhaseConfigured after successful configure, got %d", srv.store.GetPhase())
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
	srv := newTestServer(fb)

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
	if srv.store.GetPhase() != state.PhaseUnconfigured {
		t.Fatalf("expected PhaseUnconfigured after failed configure, got %d", srv.store.GetPhase())
	}
}

func TestConfigurePassesArgs(t *testing.T) {
	fb := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	srv := newTestServer(fb)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	// First, perform a build so we are in PhaseBuilt.
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	srv.store.FinishBuild(0, time.Second, nil, nil)
	if srv.store.GetPhase() != state.PhaseBuilt {
		t.Fatalf("expected PhaseBuilt, got %d", srv.store.GetPhase())
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
	if srv.store.GetPhase() != state.PhaseConfigured {
		t.Fatalf("expected PhaseConfigured after clean, got %d", srv.store.GetPhase())
	}
}

func TestCleanWhenNotBuilt(t *testing.T) {
	fb := &fakeBuilder{
		cleanResult: &builder.BuildResult{ExitCode: 0},
	}
	srv := newTestServer(fb)
	srv.store.SetConfigured()

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
	srv := newTestServer(fb)

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
	srv := newTestServer(fb)
	// Use a temp dir as source so we can control file layout.
	tmpDir := t.TempDir()
	srv.cfg.SourceDir = tmpDir
	srv.cfg.BuildDir = tmpDir + "/build"

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
	srv := newTestServer(fb)
	tmpDir := t.TempDir()
	srv.cfg.BuildDir = tmpDir + "/nonexistent-build"
	srv.cfg.SourceDir = tmpDir

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

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
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: srcFile, Line: 15, Column: 3, Severity: diagnostics.SeverityError, Message: "undeclared identifier"},
	}
	srv.store.FinishBuild(1, time.Second, errs, nil)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	// Populate store with 1 error.
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "main.cpp", Line: 10, Severity: diagnostics.SeverityError, Message: "error"},
	}
	srv.store.FinishBuild(1, time.Second, errs, nil)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

	// Populate store with an error pointing to a nonexistent file.
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "/nonexistent/path/to/file.cpp", Line: 10, Severity: diagnostics.SeverityError, Message: "error"},
	}
	srv.store.FinishBuild(1, time.Second, errs, nil)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

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
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: srcFile, Line: 2, Severity: diagnostics.SeverityError, Message: "error near start"},
	}
	srv.store.FinishBuild(1, time.Second, errs, nil)

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
	srv := newTestServer(fb)
	srv.store.SetConfigured()

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
	if err := srv.store.StartBuild(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: srcFile, Line: 15, Severity: diagnostics.SeverityError, Message: "error near end"},
	}
	srv.store.FinishBuild(1, time.Second, errs, nil)

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
