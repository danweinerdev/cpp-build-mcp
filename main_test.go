package main

import (
	"context"
	"encoding/json"
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

	// Captured arguments from the last Build call.
	lastTargets []string
	lastJobs    int
}

func (f *fakeBuilder) Configure(_ context.Context, _ []string) (*builder.BuildResult, error) {
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

func (f *fakeBuilder) Clean(_ context.Context, _ []string) (*builder.BuildResult, error) {
	return &builder.BuildResult{}, nil
}

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
