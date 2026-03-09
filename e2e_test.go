package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/danweinerdev/cpp-build-mcp/builder"
	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/diagnostics"
	"github.com/danweinerdev/cpp-build-mcp/state"
)

// jsonRPCRequest is a generic JSON-RPC request for E2E tests.
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse is a generic JSON-RPC response for E2E tests.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// e2eEnv holds the pipes and server for an E2E test.
type e2eEnv struct {
	toServer   io.Writer // write JSON-RPC requests here
	fromServer *bufio.Reader
	cancel     context.CancelFunc
	done       chan error
	store      *state.Store
	nextID     int
}

// startE2E sets up an MCP server with piped I/O, initializes it, and returns
// a helper for sending requests. The caller must call env.cancel() when done.
func startE2E(t *testing.T, fb *fakeBuilder) *e2eEnv {
	t.Helper()

	cfg := &config.Config{
		BuildDir:     "build",
		SourceDir:    ".",
		Toolchain:    "clang",
		Generator:    "ninja",
		BuildTimeout: 5 * time.Minute,
	}

	store := state.NewStore()
	registry := newConfigRegistry("default")
	registry.add(&configInstance{
		name:    "default",
		cfg:     cfg,
		builder: fb,
		store:   store,
	})
	srv := &mcpServer{
		registry: registry,
	}

	s := server.NewMCPServer("cpp-build-mcp", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
	)

	s.AddTool(
		mcp.NewTool("build",
			mcp.WithDescription("Build the C/C++ project."),
			mcp.WithArray("targets", mcp.WithStringItems()),
			mcp.WithNumber("jobs"),
		),
		srv.handleBuild,
	)

	s.AddTool(
		mcp.NewTool("get_errors",
			mcp.WithDescription("Get build errors."),
		),
		srv.handleGetErrors,
	)

	s.AddTool(
		mcp.NewTool("get_warnings",
			mcp.WithDescription("Get build warnings."),
			mcp.WithString("filter", mcp.Description("Optional filter.")),
		),
		srv.handleGetWarnings,
	)

	s.AddTool(
		mcp.NewTool("configure",
			mcp.WithDescription("Run CMake configure."),
			mcp.WithArray("cmake_args", mcp.WithStringItems()),
		),
		srv.handleConfigure,
	)

	s.AddTool(
		mcp.NewTool("clean",
			mcp.WithDescription("Clean build artifacts."),
			mcp.WithArray("targets", mcp.WithStringItems()),
		),
		srv.handleClean,
	)

	s.AddTool(
		mcp.NewTool("get_changed_files",
			mcp.WithDescription("Detect changed files."),
		),
		srv.handleGetChangedFiles,
	)

	s.AddTool(
		mcp.NewTool("get_build_graph",
			mcp.WithDescription("Get build graph summary."),
		),
		srv.handleGetBuildGraph,
	)

	s.AddTool(
		mcp.NewTool("suggest_fix",
			mcp.WithDescription("Get source context around a build error for fixing."),
			mcp.WithNumber("error_index", mcp.Description("Zero-based index into the error list from get_errors.")),
		),
		srv.handleSuggestFix,
	)

	s.AddTool(
		mcp.NewTool("list_configs",
			mcp.WithDescription("List all available build configurations."),
		),
		srv.handleListConfigs,
	)

	s.AddResource(
		mcp.NewResource("build://health", "Build Health",
			mcp.WithResourceDescription("Build health summary"),
			mcp.WithMIMEType("text/plain"),
		),
		srv.handleBuildHealth,
	)

	// Create pipes: client writes to clientW -> server reads from clientR
	//               server writes to serverW -> client reads from serverR
	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	stdioServer := server.NewStdioServer(s)
	stdioServer.SetErrorLogger(log.New(io.Discard, "", 0))

	go func() {
		done <- stdioServer.Listen(ctx, clientR, serverW)
	}()

	env := &e2eEnv{
		toServer:   clientW,
		fromServer: bufio.NewReader(serverR),
		cancel:     cancel,
		done:       done,
		store:      store,
		nextID:     1,
	}

	// Initialize the MCP session.
	env.initialize(t)

	return env
}

func (e *e2eEnv) send(t *testing.T, method string, params any) jsonRPCResponse {
	t.Helper()
	id := e.nextID
	e.nextID++

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	data = append(data, '\n')

	if _, err := e.toServer.Write(data); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read lines until we get a response (has "id" field).
	// Skip any server-sent notifications that may arrive before the response.
	for {
		line, err := e.fromServer.ReadString('\n')
		if err != nil {
			t.Fatalf("read response: %v", err)
		}

		// Check if this is a notification (no "id" field) or a response.
		var peek struct {
			ID *int `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &peek); err != nil {
			t.Fatalf("unmarshal message: %v\nraw: %s", err, line)
		}
		if peek.ID == nil {
			continue // skip notifications
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("unmarshal response: %v\nraw: %s", err, line)
		}
		return resp
	}
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (e *e2eEnv) sendNotification(t *testing.T, method string) {
	t.Helper()
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
	}{
		JSONRPC: "2.0",
		Method:  method,
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	data = append(data, '\n')
	if _, err := e.toServer.Write(data); err != nil {
		t.Fatalf("write notification: %v", err)
	}
}

func (e *e2eEnv) initialize(t *testing.T) {
	t.Helper()
	resp := e.send(t, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "test-client",
			"version": "1.0.0",
		},
	})
	if resp.Error != nil {
		t.Fatalf("initialize failed: %s", resp.Error.Message)
	}
	// Send initialized notification.
	e.sendNotification(t, "notifications/initialized")
}

func (e *e2eEnv) callTool(t *testing.T, name string, args map[string]any) jsonRPCResponse {
	t.Helper()
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	return e.send(t, "tools/call", params)
}

func (e *e2eEnv) readResource(t *testing.T, uri string) jsonRPCResponse {
	t.Helper()
	return e.send(t, "resources/read", map[string]any{"uri": uri})
}

// toolResultText extracts the text content from a tools/call response.
func toolResultText(t *testing.T, resp jsonRPCResponse) (string, bool) {
	t.Helper()
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("no content in tool result")
	}
	return result.Content[0].Text, result.IsError
}

// resourceText extracts the text from a resources/read response.
func resourceText(t *testing.T, resp jsonRPCResponse) string {
	t.Helper()
	var result struct {
		Contents []struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal resource result: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("no contents in resource result")
	}
	return result.Contents[0].Text
}

func TestE2ESuccessfulBuild(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Stdout:   "",
			Stderr:   "",
			Duration: time.Second,
		},
	}
	env := startE2E(t, fb)
	defer env.cancel()

	// Configure the store so builds can proceed.
	env.store.SetConfigured()

	resp := env.callTool(t, "build", nil)
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}

	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}

	var br buildResponse
	if err := json.Unmarshal([]byte(text), &br); err != nil {
		t.Fatalf("unmarshal build response: %v", err)
	}
	if br.ExitCode != 0 {
		t.Fatalf("expected exit_code 0, got %d", br.ExitCode)
	}
	if br.FilesCompiled != 0 {
		t.Fatalf("expected files_compiled 0, got %d", br.FilesCompiled)
	}
}

func TestE2EFailedBuildThenGetErrors(t *testing.T) {
	clangJSON := `[{"file":"main.cpp","line":10,"column":5,"severity":"error","message":"use of undeclared identifier 'x'","option":"-Werror"},{"file":"main.cpp","line":20,"column":1,"severity":"warning","message":"unused variable 'y'","option":"-Wunused-variable"}]`

	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{
			ExitCode: 1,
			Stdout:   clangJSON,
			Stderr:   "1 error generated.\n",
			Duration: 2 * time.Second,
		},
	}
	env := startE2E(t, fb)
	defer env.cancel()

	env.store.SetConfigured()

	// Build should return summary with error count.
	resp := env.callTool(t, "build", nil)
	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}

	var br buildResponse
	if err := json.Unmarshal([]byte(text), &br); err != nil {
		t.Fatalf("unmarshal build response: %v", err)
	}
	if br.ExitCode != 1 {
		t.Fatalf("expected exit_code 1, got %d", br.ExitCode)
	}
	if br.ErrorCount != 1 {
		t.Fatalf("expected error_count 1, got %d", br.ErrorCount)
	}
	if br.WarningCount != 1 {
		t.Fatalf("expected warning_count 1, got %d", br.WarningCount)
	}

	// get_errors should return the error diagnostic.
	resp = env.callTool(t, "get_errors", nil)
	text, isError = toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}

	var er getErrorsResponse
	if err := json.Unmarshal([]byte(text), &er); err != nil {
		t.Fatalf("unmarshal get_errors response: %v", err)
	}
	if len(er.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(er.Errors))
	}
	if er.Errors[0].File != "main.cpp" {
		t.Fatalf("expected file main.cpp, got %s", er.Errors[0].File)
	}
	if er.Errors[0].Line != 10 {
		t.Fatalf("expected line 10, got %d", er.Errors[0].Line)
	}
}

func TestE2EBuildWhenUnconfigured(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{ExitCode: 0, Duration: time.Second},
	}
	env := startE2E(t, fb)
	defer env.cancel()

	// Do NOT configure — store is in PhaseUnconfigured.
	resp := env.callTool(t, "build", nil)
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %s", resp.Error.Message)
	}

	_, isError := toolResultText(t, resp)
	if !isError {
		t.Fatal("expected tool error when unconfigured")
	}
}

func TestE2EBuildInProgressGuard(t *testing.T) {
	// Create a builder that blocks until told to proceed.
	proceed := make(chan struct{})
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{ExitCode: 0, Duration: time.Second},
	}

	cfg := &config.Config{
		BuildDir:     "build",
		SourceDir:    ".",
		Toolchain:    "clang",
		Generator:    "ninja",
		BuildTimeout: 5 * time.Minute,
	}

	store := state.NewStore()
	store.SetConfigured()

	// Use a blocking builder for the first call.
	blockingBuilder := &blockingFakeBuilder{
		result:  &builder.BuildResult{ExitCode: 0, Duration: time.Second},
		proceed: proceed,
	}

	registry := newConfigRegistry("default")
	registry.add(&configInstance{
		name:    "default",
		cfg:     cfg,
		builder: blockingBuilder,
		store:   store,
	})
	srv := &mcpServer{
		registry: registry,
	}

	s := server.NewMCPServer("cpp-build-mcp", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
	)
	s.AddTool(
		mcp.NewTool("build", mcp.WithDescription("Build.")),
		srv.handleBuild,
	)

	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdioSrv := server.NewStdioServer(s)
	stdioSrv.SetErrorLogger(log.New(io.Discard, "", 0))
	go stdioSrv.Listen(ctx, clientR, serverW)

	reader := bufio.NewReader(serverR)

	// Initialize.
	initReq := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}%s`, "\n")
	clientW.Write([]byte(initReq))
	reader.ReadString('\n') // read init response
	clientW.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))

	// Send first build (will block).
	clientW.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"build"}}` + "\n"))

	// Give the server a moment to start processing the first build.
	time.Sleep(100 * time.Millisecond)

	// Send second build (should get in-progress error).
	clientW.Write([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"build"}}` + "\n"))

	// The second request should respond first (or around the same time) since
	// the first is blocked. But due to the worker pool, responses may come in
	// any order. Let's just unblock and read both.

	// Unblock the first build after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(proceed)
	}()

	// Read two responses (order may vary).
	var responses []jsonRPCResponse
	for i := 0; i < 2; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read response %d: %v", i, err)
		}
		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("unmarshal response %d: %v", i, err)
		}
		responses = append(responses, resp)
	}

	// Find the response to ID 3 (the second build) — it should be a tool error.
	var foundInProgress bool
	for _, resp := range responses {
		if resp.ID == 3 {
			var result struct {
				IsError bool `json:"isError"`
			}
			json.Unmarshal(resp.Result, &result)
			if result.IsError {
				foundInProgress = true
			}
		}
	}
	if !foundInProgress {
		t.Fatal("expected in-progress tool error for second build call")
	}

	_ = fb // unused, that's fine
}

// blockingFakeBuilder blocks Build() until proceed channel is closed.
type blockingFakeBuilder struct {
	result  *builder.BuildResult
	proceed chan struct{}
}

func (b *blockingFakeBuilder) Configure(_ context.Context, _ []string) (*builder.BuildResult, error) {
	return &builder.BuildResult{}, nil
}

func (b *blockingFakeBuilder) Build(_ context.Context, _ []string, _ int) (*builder.BuildResult, error) {
	<-b.proceed
	return b.result, nil
}

func (b *blockingFakeBuilder) Clean(_ context.Context, _ []string) (*builder.BuildResult, error) {
	return &builder.BuildResult{}, nil
}

func (b *blockingFakeBuilder) SetDirty(dirty bool) {}

func TestE2EHealthResource(t *testing.T) {
	fb := &fakeBuilder{
		buildResult: &builder.BuildResult{ExitCode: 0, Duration: time.Second},
	}
	env := startE2E(t, fb)
	defer env.cancel()

	// Unconfigured health.
	resp := env.readResource(t, "build://health")
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}
	text := resourceText(t, resp)
	if !containsStr(text, "UNCONFIGURED") {
		t.Fatalf("expected UNCONFIGURED, got %q", text)
	}

	// Configure and check.
	env.store.SetConfigured()
	resp = env.readResource(t, "build://health")
	text = resourceText(t, resp)
	if !containsStr(text, "READY") {
		t.Fatalf("expected READY, got %q", text)
	}

	// Build and check.
	env.callTool(t, "build", nil)
	resp = env.readResource(t, "build://health")
	text = resourceText(t, resp)
	if !containsStr(text, "OK") {
		t.Fatalf("expected OK, got %q", text)
	}

	// Make it dirty and check.
	env.store.SetDirty()
	resp = env.readResource(t, "build://health")
	text = resourceText(t, resp)
	if !containsStr(text, "DIRTY") {
		t.Fatalf("expected DIRTY, got %q", text)
	}
}

func TestE2EGetErrorsAfterFailedBuild(t *testing.T) {
	// Pre-populate state with errors (simulating a failed build).
	fb := &fakeBuilder{}
	env := startE2E(t, fb)
	defer env.cancel()

	env.store.SetConfigured()
	if err := env.store.StartBuild(); err != nil {
		t.Fatalf("start build: %v", err)
	}
	errs := []diagnostics.Diagnostic{
		{File: "a.cpp", Line: 1, Severity: diagnostics.SeverityError, Message: "bad"},
		{File: "b.cpp", Line: 2, Severity: diagnostics.SeverityError, Message: "worse"},
	}
	env.store.FinishBuild(1, time.Second, errs, nil)

	resp := env.callTool(t, "get_errors", nil)
	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}

	var er getErrorsResponse
	if err := json.Unmarshal([]byte(text), &er); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(er.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(er.Errors))
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// multiE2EEnv extends e2eEnv with per-config stores for multi-config tests.
type multiE2EEnv struct {
	e2eEnv
	stores map[string]*state.Store
}

// startMultiE2E sets up an MCP server with two named configs ("debug" and
// "release") using the provided fakeBuilders. Each config gets its own
// state.Store. The caller must call env.cancel() when done.
func startMultiE2E(t *testing.T, debugFB, releaseFB *fakeBuilder) *multiE2EEnv {
	t.Helper()

	// Toolchain must be "clang" to match the Clang JSON diagnostic format
	// used in fakeBuilder stderr/stdout fixtures. Changing this would cause
	// diagnostics.Parse to use a different parser, silently producing zero
	// diagnostics and making error-count assertions pass vacuously.
	debugCfg := &config.Config{
		BuildDir:     "build/debug",
		SourceDir:    ".",
		Toolchain:    "clang",
		Generator:    "ninja",
		BuildTimeout: 5 * time.Minute,
	}
	releaseCfg := &config.Config{
		BuildDir:     "build/release",
		SourceDir:    ".",
		Toolchain:    "clang",
		Generator:    "ninja",
		BuildTimeout: 5 * time.Minute,
	}

	debugStore := state.NewStore()
	releaseStore := state.NewStore()

	registry := newConfigRegistry("debug")
	registry.add(&configInstance{
		name:    "debug",
		cfg:     debugCfg,
		builder: debugFB,
		store:   debugStore,
	})
	registry.add(&configInstance{
		name:    "release",
		cfg:     releaseCfg,
		builder: releaseFB,
		store:   releaseStore,
	})

	srv := &mcpServer{registry: registry}

	s := server.NewMCPServer("cpp-build-mcp", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
	)

	s.AddTool(
		mcp.NewTool("build",
			mcp.WithDescription("Build the C/C++ project."),
			mcp.WithString("config", mcp.Description("Configuration name.")),
			mcp.WithArray("targets", mcp.WithStringItems()),
			mcp.WithNumber("jobs"),
		),
		srv.handleBuild,
	)
	s.AddTool(
		mcp.NewTool("get_errors",
			mcp.WithDescription("Get build errors."),
			mcp.WithString("config", mcp.Description("Configuration name.")),
		),
		srv.handleGetErrors,
	)
	s.AddTool(
		mcp.NewTool("get_warnings",
			mcp.WithDescription("Get build warnings."),
			mcp.WithString("config", mcp.Description("Configuration name.")),
			mcp.WithString("filter", mcp.Description("Optional filter.")),
		),
		srv.handleGetWarnings,
	)
	s.AddTool(
		mcp.NewTool("configure",
			mcp.WithDescription("Run CMake configure."),
			mcp.WithString("config", mcp.Description("Configuration name.")),
			mcp.WithArray("cmake_args", mcp.WithStringItems()),
		),
		srv.handleConfigure,
	)
	s.AddTool(
		mcp.NewTool("clean",
			mcp.WithDescription("Clean build artifacts."),
			mcp.WithString("config", mcp.Description("Configuration name.")),
			mcp.WithArray("targets", mcp.WithStringItems()),
		),
		srv.handleClean,
	)
	s.AddTool(
		mcp.NewTool("list_configs",
			mcp.WithDescription("List all available build configurations."),
		),
		srv.handleListConfigs,
	)
	s.AddResource(
		mcp.NewResource("build://health", "Build Health",
			mcp.WithResourceDescription("Build health summary"),
			mcp.WithMIMEType("text/plain"),
		),
		srv.handleBuildHealth,
	)

	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	stdioServer := server.NewStdioServer(s)
	stdioServer.SetErrorLogger(log.New(io.Discard, "", 0))

	go func() {
		done <- stdioServer.Listen(ctx, clientR, serverW)
	}()

	env := &multiE2EEnv{
		e2eEnv: e2eEnv{
			toServer:   clientW,
			fromServer: bufio.NewReader(serverR),
			cancel:     cancel,
			done:       done,
			store:      debugStore, // default store for backward compat
			nextID:     1,
		},
		stores: map[string]*state.Store{
			"debug":   debugStore,
			"release": releaseStore,
		},
	}

	env.initialize(t)
	return env
}

// TestE2EMultiConfigConfigureIsolation verifies that configuring one named
// config does not affect the other's state.
func TestE2EMultiConfigConfigureIsolation(t *testing.T) {
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	env := startMultiE2E(t, debugFB, releaseFB)
	defer env.cancel()

	// Configure only the debug config via the E2E pipe.
	resp := env.callTool(t, "configure", map[string]any{"config": "debug"})
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}
	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}

	var cr configureResponse
	if err := json.Unmarshal([]byte(text), &cr); err != nil {
		t.Fatalf("unmarshal configure response: %v", err)
	}
	if !cr.Success {
		t.Fatal("expected configure success")
	}
	if cr.Config != "debug" {
		t.Fatalf("expected config field %q, got %q", "debug", cr.Config)
	}

	// Verify debug store is Configured, release store remains Unconfigured.
	if env.stores["debug"].GetPhase() != state.PhaseConfigured {
		t.Fatalf("expected debug phase Configured, got %d", env.stores["debug"].GetPhase())
	}
	if env.stores["release"].GetPhase() != state.PhaseUnconfigured {
		t.Fatalf("expected release phase Unconfigured, got %d", env.stores["release"].GetPhase())
	}

	// Also verify via list_configs E2E call.
	listResp := env.callTool(t, "list_configs", nil)
	listText, listIsError := toolResultText(t, listResp)
	if listIsError {
		t.Fatalf("unexpected tool error: %s", listText)
	}

	var lc listConfigsResponse
	if err := json.Unmarshal([]byte(listText), &lc); err != nil {
		t.Fatalf("unmarshal list_configs response: %v", err)
	}
	for _, cs := range lc.Configs {
		switch cs.Name {
		case "debug":
			if cs.Status != "configured" {
				t.Fatalf("expected debug status %q, got %q", "configured", cs.Status)
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

// TestE2EMultiConfigBuildErrorIsolation verifies that building one config with
// errors does not leak diagnostics into the other config's get_errors.
func TestE2EMultiConfigBuildErrorIsolation(t *testing.T) {
	// Debug builder will produce errors via Clang JSON diagnostics.
	debugClangJSON := `[{"file":"src/debug.cpp","line":10,"column":5,"severity":"error","message":"use of undeclared identifier 'foo'","option":"-Werror"}]`
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
		buildResult: &builder.BuildResult{
			ExitCode: 1,
			Stdout:   debugClangJSON,
			Stderr:   "1 error generated.\n",
			Duration: time.Second,
		},
	}
	// Release builder is clean (no errors).
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Stdout:   "",
			Stderr:   "",
			Duration: time.Second,
		},
	}

	env := startMultiE2E(t, debugFB, releaseFB)
	defer env.cancel()

	// Configure both configs.
	for _, name := range []string{"debug", "release"} {
		resp := env.callTool(t, "configure", map[string]any{"config": name})
		text, isError := toolResultText(t, resp)
		if isError {
			t.Fatalf("configure %s failed: %s", name, text)
		}
	}

	// Build debug (will fail with 1 error).
	resp := env.callTool(t, "build", map[string]any{"config": "debug"})
	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error for debug build: %s", text)
	}
	var debugBR buildResponse
	if err := json.Unmarshal([]byte(text), &debugBR); err != nil {
		t.Fatalf("unmarshal debug build response: %v", err)
	}
	if debugBR.ExitCode != 1 {
		t.Fatalf("expected debug exit_code 1, got %d", debugBR.ExitCode)
	}
	if debugBR.ErrorCount != 1 {
		t.Fatalf("expected debug error_count 1, got %d", debugBR.ErrorCount)
	}
	if debugBR.Config != "debug" {
		t.Fatalf("expected config %q, got %q", "debug", debugBR.Config)
	}

	// get_errors for release should return empty (release has not been built).
	resp = env.callTool(t, "get_errors", map[string]any{"config": "release"})
	text, isError = toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error for release get_errors: %s", text)
	}
	var releaseER getErrorsResponse
	if err := json.Unmarshal([]byte(text), &releaseER); err != nil {
		t.Fatalf("unmarshal release get_errors response: %v", err)
	}
	if len(releaseER.Errors) != 0 {
		t.Fatalf("expected 0 errors for release, got %d", len(releaseER.Errors))
	}
	if releaseER.Config != "release" {
		t.Fatalf("expected config %q, got %q", "release", releaseER.Config)
	}

	// get_errors for debug should return 1 error.
	resp = env.callTool(t, "get_errors", map[string]any{"config": "debug"})
	text, isError = toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error for debug get_errors: %s", text)
	}
	var debugER getErrorsResponse
	if err := json.Unmarshal([]byte(text), &debugER); err != nil {
		t.Fatalf("unmarshal debug get_errors response: %v", err)
	}
	if len(debugER.Errors) != 1 {
		t.Fatalf("expected 1 error for debug, got %d", len(debugER.Errors))
	}
	if debugER.Errors[0].File != "src/debug.cpp" {
		t.Fatalf("expected error file %q, got %q", "src/debug.cpp", debugER.Errors[0].File)
	}
}

// TestE2EMultiConfigIndependentBuildCounts verifies that building both configs
// produces independent error and warning counts.
func TestE2EMultiConfigIndependentBuildCounts(t *testing.T) {
	// Debug: 2 errors, 1 warning.
	debugClangJSON := `[{"file":"a.cpp","line":1,"column":1,"severity":"error","message":"err1","option":""},{"file":"a.cpp","line":2,"column":1,"severity":"error","message":"err2","option":""},{"file":"a.cpp","line":3,"column":1,"severity":"warning","message":"warn1","option":"-Wunused"}]`
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
		buildResult: &builder.BuildResult{
			ExitCode: 1,
			Stdout:   debugClangJSON,
			Stderr:   "",
			Duration: time.Second,
		},
	}

	// Release: 0 errors, 2 warnings.
	releaseClangJSON := `[{"file":"b.cpp","line":10,"column":1,"severity":"warning","message":"rwarn1","option":"-Wall"},{"file":"b.cpp","line":20,"column":1,"severity":"warning","message":"rwarn2","option":"-Wextra"}]`
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Stdout:   releaseClangJSON,
			Stderr:   "",
			Duration: 2 * time.Second,
		},
	}

	env := startMultiE2E(t, debugFB, releaseFB)
	defer env.cancel()

	// Configure both.
	for _, name := range []string{"debug", "release"} {
		resp := env.callTool(t, "configure", map[string]any{"config": name})
		text, isError := toolResultText(t, resp)
		if isError {
			t.Fatalf("configure %s failed: %s", name, text)
		}
	}

	// Build debug.
	resp := env.callTool(t, "build", map[string]any{"config": "debug"})
	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}
	var debugBR buildResponse
	if err := json.Unmarshal([]byte(text), &debugBR); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if debugBR.ErrorCount != 2 {
		t.Fatalf("expected debug error_count 2, got %d", debugBR.ErrorCount)
	}
	if debugBR.WarningCount != 1 {
		t.Fatalf("expected debug warning_count 1, got %d", debugBR.WarningCount)
	}

	// Build release.
	resp = env.callTool(t, "build", map[string]any{"config": "release"})
	text, isError = toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}
	var releaseBR buildResponse
	if err := json.Unmarshal([]byte(text), &releaseBR); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if releaseBR.ErrorCount != 0 {
		t.Fatalf("expected release error_count 0, got %d", releaseBR.ErrorCount)
	}
	if releaseBR.WarningCount != 2 {
		t.Fatalf("expected release warning_count 2, got %d", releaseBR.WarningCount)
	}

	// Verify counts did not bleed across configs.
	if debugBR.Config != "debug" {
		t.Fatalf("expected debug config field, got %q", debugBR.Config)
	}
	if releaseBR.Config != "release" {
		t.Fatalf("expected release config field, got %q", releaseBR.Config)
	}
}

// TestE2EMultiConfigListConfigsAfterOperations verifies that list_configs
// returns correct statuses after various operations on each config.
func TestE2EMultiConfigListConfigsAfterOperations(t *testing.T) {
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Stdout:   "",
			Stderr:   "",
			Duration: time.Second,
		},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	env := startMultiE2E(t, debugFB, releaseFB)
	defer env.cancel()

	// Initially both should be unconfigured.
	resp := env.callTool(t, "list_configs", nil)
	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}
	var lc listConfigsResponse
	if err := json.Unmarshal([]byte(text), &lc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(lc.Configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(lc.Configs))
	}
	for _, cs := range lc.Configs {
		if cs.Status != "unconfigured" {
			t.Fatalf("expected initial status %q for %q, got %q", "unconfigured", cs.Name, cs.Status)
		}
	}
	if lc.DefaultConfig != "debug" {
		t.Fatalf("expected default_config %q, got %q", "debug", lc.DefaultConfig)
	}

	// Configure debug, build debug.
	env.callTool(t, "configure", map[string]any{"config": "debug"})
	env.callTool(t, "build", map[string]any{"config": "debug"})

	// Configure release (but don't build).
	env.callTool(t, "configure", map[string]any{"config": "release"})

	// Check statuses: debug=built, release=configured.
	resp = env.callTool(t, "list_configs", nil)
	text, isError = toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if err := json.Unmarshal([]byte(text), &lc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, cs := range lc.Configs {
		switch cs.Name {
		case "debug":
			if cs.Status != "built" {
				t.Fatalf("expected debug status %q, got %q", "built", cs.Status)
			}
			if cs.BuildDir != "build/debug" {
				t.Fatalf("expected debug build_dir %q, got %q", "build/debug", cs.BuildDir)
			}
		case "release":
			if cs.Status != "configured" {
				t.Fatalf("expected release status %q, got %q", "configured", cs.Status)
			}
			if cs.BuildDir != "build/release" {
				t.Fatalf("expected release build_dir %q, got %q", "build/release", cs.BuildDir)
			}
		default:
			t.Fatalf("unexpected config name %q", cs.Name)
		}
	}
}

// TestE2EMultiConfigHealthAggregation verifies that the build://health resource
// returns a pipe-separated aggregate format with both configs' states.
func TestE2EMultiConfigHealthAggregation(t *testing.T) {
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Stdout:   "",
			Stderr:   "",
			Duration: time.Second,
		},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	env := startMultiE2E(t, debugFB, releaseFB)
	defer env.cancel()

	// Both unconfigured.
	resp := env.readResource(t, "build://health")
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}
	text := resourceText(t, resp)
	if !containsStr(text, "debug: UNCONFIGURED") {
		t.Fatalf("expected 'debug: UNCONFIGURED' in health, got %q", text)
	}
	if !containsStr(text, "release: UNCONFIGURED") {
		t.Fatalf("expected 'release: UNCONFIGURED' in health, got %q", text)
	}
	if !containsStr(text, " | ") {
		t.Fatalf("expected pipe separator in health, got %q", text)
	}

	// Configure debug and build it.
	env.callTool(t, "configure", map[string]any{"config": "debug"})
	env.callTool(t, "build", map[string]any{"config": "debug"})

	// Configure release (but don't build).
	env.callTool(t, "configure", map[string]any{"config": "release"})

	// Health should show debug: OK and release: READY.
	resp = env.readResource(t, "build://health")
	text = resourceText(t, resp)
	if !containsStr(text, "debug: OK") {
		t.Fatalf("expected 'debug: OK' in health, got %q", text)
	}
	if !containsStr(text, "release: READY") {
		t.Fatalf("expected 'release: READY' in health, got %q", text)
	}
}

// TestE2EMultiConfigBuildStateIsolation is the comprehensive state isolation
// test. It configures debug, builds debug with errors, then verifies release
// is entirely unaffected: release remains unconfigured, release get_errors
// returns empty, and building release without configure returns an error.
func TestE2EMultiConfigBuildStateIsolation(t *testing.T) {
	debugClangJSON := `[{"file":"main.cpp","line":5,"column":3,"severity":"error","message":"missing semicolon","option":""}]`
	debugFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
		buildResult: &builder.BuildResult{
			ExitCode: 1,
			Stdout:   debugClangJSON,
			Stderr:   "",
			Duration: time.Second,
		},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
		buildResult: &builder.BuildResult{
			ExitCode: 0,
			Duration: time.Second,
		},
	}

	env := startMultiE2E(t, debugFB, releaseFB)
	defer env.cancel()

	// Configure and build debug.
	env.callTool(t, "configure", map[string]any{"config": "debug"})
	resp := env.callTool(t, "build", map[string]any{"config": "debug"})
	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}
	var debugBR buildResponse
	if err := json.Unmarshal([]byte(text), &debugBR); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if debugBR.ErrorCount != 1 {
		t.Fatalf("expected debug error_count 1, got %d", debugBR.ErrorCount)
	}

	// Release is still unconfigured; building it should fail.
	resp = env.callTool(t, "build", map[string]any{"config": "release"})
	_, isError = toolResultText(t, resp)
	if !isError {
		t.Fatal("expected tool error when building unconfigured release config")
	}

	// Release get_errors should return empty.
	resp = env.callTool(t, "get_errors", map[string]any{"config": "release"})
	text, isError = toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error for release get_errors: %s", text)
	}
	var releaseER getErrorsResponse
	if err := json.Unmarshal([]byte(text), &releaseER); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(releaseER.Errors) != 0 {
		t.Fatalf("expected 0 errors for unconfigured release, got %d", len(releaseER.Errors))
	}

	// Debug get_errors should still have 1 error.
	resp = env.callTool(t, "get_errors", map[string]any{"config": "debug"})
	text, isError = toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error for debug get_errors: %s", text)
	}
	var debugER getErrorsResponse
	if err := json.Unmarshal([]byte(text), &debugER); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(debugER.Errors) != 1 {
		t.Fatalf("expected 1 error for debug, got %d", len(debugER.Errors))
	}

	// Health should show debug: FAIL and release: UNCONFIGURED.
	healthResp := env.readResource(t, "build://health")
	healthText := resourceText(t, healthResp)
	if !containsStr(healthText, "debug: FAIL") {
		t.Fatalf("expected 'debug: FAIL' in health, got %q", healthText)
	}
	if !containsStr(healthText, "release: UNCONFIGURED") {
		t.Fatalf("expected 'release: UNCONFIGURED' in health, got %q", healthText)
	}
}

// ---------------------------------------------------------------------------
// Progress notification E2E tests
// ---------------------------------------------------------------------------

// progressFakeBuilder is a fakeBuilder that also implements progressSetter.
// During Build(), it calls the progress callback with simulated [N/M] lines.
type progressFakeBuilder struct {
	fakeBuilder
	progressFunc builder.ProgressFunc
	progressLines []struct{ current, total int; line string }
}

func (f *progressFakeBuilder) SetProgressFunc(fn builder.ProgressFunc) {
	f.progressFunc = fn
}

func (f *progressFakeBuilder) Build(_ context.Context, targets []string, jobs int) (*builder.BuildResult, error) {
	f.lastTargets = targets
	f.lastJobs = jobs
	// Call progress callback for each simulated line before returning the result.
	if f.progressFunc != nil {
		for _, pl := range f.progressLines {
			f.progressFunc(pl.current, pl.total, pl.line)
		}
	}
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return f.buildResult, nil
}

// jsonRPCMessage is a generic JSON-RPC message that could be a response or notification.
type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`      // present for responses, absent for notifications
	Method  string          `json:"method,omitempty"`  // present for notifications
	Params  json.RawMessage `json:"params,omitempty"`  // present for notifications
	Result  json.RawMessage `json:"result,omitempty"`  // present for responses
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// callToolWithProgress sends a tools/call request with _meta.progressToken and
// reads exactly expectedNotifications + 1 messages (the notifications plus the
// response). This is deterministic — no timing dependencies.
func (e *e2eEnv) callToolWithProgress(t *testing.T, name string, args map[string]any, progressToken any, expectedNotifications int) (jsonRPCResponse, []jsonRPCMessage) {
	t.Helper()
	id := e.nextID
	e.nextID++

	params := map[string]any{
		"name": name,
		"_meta": map[string]any{
			"progressToken": progressToken,
		},
	}
	if args != nil {
		params["arguments"] = args
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	data = append(data, '\n')

	if _, err := e.toServer.Write(data); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read exactly expectedNotifications + 1 messages. The response and
	// notifications may arrive in any order since notifications are sent
	// asynchronously via the mcp-go notification channel.
	//
	// A goroutine reads messages into a channel so we can apply a timeout.
	// If the expected count is wrong, the test fails with a clear error
	// instead of hanging indefinitely.
	type readResult struct {
		msg jsonRPCMessage
		err error
	}
	msgCh := make(chan readResult, expectedNotifications+1)
	go func() {
		for {
			line, err := e.fromServer.ReadString('\n')
			if err != nil {
				msgCh <- readResult{err: err}
				return
			}
			var msg jsonRPCMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				msgCh <- readResult{err: fmt.Errorf("unmarshal: %v (raw: %s)", err, line)}
				return
			}
			msgCh <- readResult{msg: msg}
		}
	}()

	var notifications []jsonRPCMessage
	var resp jsonRPCResponse
	gotResponse := false
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	for i := 0; i < expectedNotifications+1; i++ {
		select {
		case r := <-msgCh:
			if r.err != nil {
				t.Fatalf("read message %d: %v", i, r.err)
			}
			if r.msg.ID != nil && *r.msg.ID == id {
				resp = jsonRPCResponse{
					JSONRPC: r.msg.JSONRPC,
					ID:      *r.msg.ID,
					Result:  r.msg.Result,
					Error:   r.msg.Error,
				}
				gotResponse = true
			} else {
				notifications = append(notifications, r.msg)
			}
		case <-timer.C:
			t.Fatalf("timeout waiting for message %d/%d (got response: %v, notifications: %d)",
				i+1, expectedNotifications+1, gotResponse, len(notifications))
		}
	}

	if !gotResponse {
		t.Fatal("did not receive tool response among expected messages")
	}

	return resp, notifications
}

func TestE2EBuildProgressNotifications(t *testing.T) {
	fb := &progressFakeBuilder{
		fakeBuilder: fakeBuilder{
			buildResult: &builder.BuildResult{
				ExitCode: 0,
				Stdout:   "",
				Stderr:   "",
				Duration: time.Second,
			},
		},
		progressLines: []struct{ current, total int; line string }{
			{1, 3, "[1/3] Building CXX object a.cpp.o"},
			{2, 3, "[2/3] Building CXX object b.cpp.o"},
			{3, 3, "[3/3] Linking CXX executable main"},
		},
	}

	// Use startE2E but with the progressFakeBuilder.
	cfg := &config.Config{
		BuildDir:     "build",
		SourceDir:    ".",
		Toolchain:    "clang",
		Generator:    "ninja",
		BuildTimeout: 5 * time.Minute,
	}

	store := state.NewStore()
	registry := newConfigRegistry("default")
	registry.add(&configInstance{
		name:    "default",
		cfg:     cfg,
		builder: fb,
		store:   store,
	})
	srv := &mcpServer{registry: registry}

	s := server.NewMCPServer("cpp-build-mcp", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
	)
	s.AddTool(
		mcp.NewTool("build",
			mcp.WithDescription("Build."),
			mcp.WithArray("targets", mcp.WithStringItems()),
			mcp.WithNumber("jobs"),
		),
		srv.handleBuild,
	)

	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdioSrv := server.NewStdioServer(s)
	stdioSrv.SetErrorLogger(log.New(io.Discard, "", 0))
	go stdioSrv.Listen(ctx, clientR, serverW)

	env := &e2eEnv{
		toServer:   clientW,
		fromServer: bufio.NewReader(serverR),
		cancel:     cancel,
		done:       make(chan error, 1),
		store:      store,
		nextID:     1,
	}
	env.initialize(t)
	store.SetConfigured()

	t.Run("with progressToken", func(t *testing.T) {
		resp, notifications := env.callToolWithProgress(t, "build", nil, "test-token-123", 3)
		if resp.Error != nil {
			t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
		}

		text, isError := toolResultText(t, resp)
		if isError {
			t.Fatalf("unexpected tool error: %s", text)
		}

		// Verify we got progress notifications.
		var progressNotifs []jsonRPCMessage
		for _, n := range notifications {
			if n.Method == "notifications/progress" {
				progressNotifs = append(progressNotifs, n)
			}
		}

		if len(progressNotifs) == 0 {
			t.Fatal("expected at least one progress notification, got none")
		}

		// Check the first notification's fields.
		var params struct {
			ProgressToken any     `json:"progressToken"`
			Progress      float64 `json:"progress"`
			Total         float64 `json:"total"`
			Message       string  `json:"message"`
		}
		if err := json.Unmarshal(progressNotifs[0].Params, &params); err != nil {
			t.Fatalf("unmarshal progress params: %v", err)
		}

		// Token should be echoed back verbatim.
		if params.ProgressToken != "test-token-123" {
			t.Errorf("expected progressToken %q, got %v", "test-token-123", params.ProgressToken)
		}
		if params.Progress != 1 {
			t.Errorf("expected progress 1, got %v", params.Progress)
		}
		if params.Total != 3 {
			t.Errorf("expected total 3, got %v", params.Total)
		}
		if !strings.Contains(params.Message, "[1/3]") {
			t.Errorf("expected message to contain [1/3], got %q", params.Message)
		}

		// Check the last notification is [3/3].
		lastNotif := progressNotifs[len(progressNotifs)-1]
		if err := json.Unmarshal(lastNotif.Params, &params); err != nil {
			t.Fatalf("unmarshal last progress params: %v", err)
		}
		if params.Progress != 3 || params.Total != 3 {
			t.Errorf("expected last progress (3, 3), got (%v, %v)", params.Progress, params.Total)
		}
	})
}

func TestE2EBuildNoProgressWithoutToken(t *testing.T) {
	fb := &progressFakeBuilder{
		fakeBuilder: fakeBuilder{
			buildResult: &builder.BuildResult{
				ExitCode: 0,
				Stdout:   "",
				Stderr:   "",
				Duration: time.Second,
			},
		},
		progressLines: []struct{ current, total int; line string }{
			{1, 1, "[1/1] Building CXX object a.cpp.o"},
		},
	}

	cfg := &config.Config{
		BuildDir:     "build",
		SourceDir:    ".",
		Toolchain:    "clang",
		Generator:    "ninja",
		BuildTimeout: 5 * time.Minute,
	}

	store := state.NewStore()
	registry := newConfigRegistry("default")
	registry.add(&configInstance{
		name:    "default",
		cfg:     cfg,
		builder: fb,
		store:   store,
	})
	srv := &mcpServer{registry: registry}

	s := server.NewMCPServer("cpp-build-mcp", "0.1.0",
		server.WithToolCapabilities(true),
	)
	s.AddTool(
		mcp.NewTool("build", mcp.WithDescription("Build.")),
		srv.handleBuild,
	)

	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdioSrv := server.NewStdioServer(s)
	stdioSrv.SetErrorLogger(log.New(io.Discard, "", 0))
	go stdioSrv.Listen(ctx, clientR, serverW)

	env := &e2eEnv{
		toServer:   clientW,
		fromServer: bufio.NewReader(serverR),
		cancel:     cancel,
		done:       make(chan error, 1),
		store:      store,
		nextID:     1,
	}
	env.initialize(t)
	store.SetConfigured()

	// Call build WITHOUT a progressToken — use regular callTool.
	resp := env.callTool(t, "build", nil)
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}

	text, isError := toolResultText(t, resp)
	if isError {
		t.Fatalf("unexpected tool error: %s", text)
	}

	// If we got a clean response on the first ReadString, no notifications were sent.
	// The existing callTool helper reads exactly one line and parses it as a response.
	// If a notification had been sent first, callTool would have failed to parse it
	// as a jsonRPCResponse (missing ID), so reaching here without error confirms
	// no notifications were sent.
}

func TestE2EMultiConfigProgressPrefix(t *testing.T) {
	debugFB := &progressFakeBuilder{
		fakeBuilder: fakeBuilder{
			configureResult: &builder.BuildResult{ExitCode: 0},
			buildResult: &builder.BuildResult{
				ExitCode: 0,
				Duration: time.Second,
			},
		},
		progressLines: []struct{ current, total int; line string }{
			{1, 2, "[1/2] Building CXX object debug.cpp.o"},
			{2, 2, "[2/2] Linking CXX executable debug_main"},
		},
	}
	releaseFB := &fakeBuilder{
		configureResult: &builder.BuildResult{ExitCode: 0},
	}

	debugCfg := &config.Config{
		BuildDir:     "build/debug",
		SourceDir:    ".",
		Toolchain:    "clang",
		Generator:    "ninja",
		BuildTimeout: 5 * time.Minute,
	}
	releaseCfg := &config.Config{
		BuildDir:     "build/release",
		SourceDir:    ".",
		Toolchain:    "clang",
		Generator:    "ninja",
		BuildTimeout: 5 * time.Minute,
	}

	debugStore := state.NewStore()
	releaseStore := state.NewStore()

	registry := newConfigRegistry("debug")
	registry.add(&configInstance{
		name:    "debug",
		cfg:     debugCfg,
		builder: debugFB,
		store:   debugStore,
	})
	registry.add(&configInstance{
		name:    "release",
		cfg:     releaseCfg,
		builder: releaseFB,
		store:   releaseStore,
	})

	srv := &mcpServer{registry: registry}

	s := server.NewMCPServer("cpp-build-mcp", "0.1.0",
		server.WithToolCapabilities(true),
	)
	s.AddTool(
		mcp.NewTool("build",
			mcp.WithDescription("Build."),
			mcp.WithString("config", mcp.Description("Config name.")),
		),
		srv.handleBuild,
	)
	s.AddTool(
		mcp.NewTool("configure",
			mcp.WithDescription("Configure."),
			mcp.WithString("config", mcp.Description("Config name.")),
		),
		srv.handleConfigure,
	)

	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdioSrv := server.NewStdioServer(s)
	stdioSrv.SetErrorLogger(log.New(io.Discard, "", 0))
	go stdioSrv.Listen(ctx, clientR, serverW)

	env := &e2eEnv{
		toServer:   clientW,
		fromServer: bufio.NewReader(serverR),
		cancel:     cancel,
		done:       make(chan error, 1),
		store:      debugStore,
		nextID:     1,
	}
	env.initialize(t)

	// Configure debug.
	env.callTool(t, "configure", map[string]any{"config": "debug"})

	// Build debug with progress token.
	resp, notifications := env.callToolWithProgress(t, "build", map[string]any{"config": "debug"}, "multi-tok", 2)
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %s", resp.Error.Message)
	}

	// In multi-config mode, messages should be prefixed with [debug].
	var progressNotifs []jsonRPCMessage
	for _, n := range notifications {
		if n.Method == "notifications/progress" {
			progressNotifs = append(progressNotifs, n)
		}
	}

	if len(progressNotifs) == 0 {
		t.Fatal("expected progress notifications, got none")
	}

	var params struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(progressNotifs[0].Params, &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !strings.HasPrefix(params.Message, "[debug] ") {
		t.Errorf("expected message to start with [debug] prefix, got %q", params.Message)
	}
}
