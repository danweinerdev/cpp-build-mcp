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
	srv := &mcpServer{
		builder: fb,
		store:   store,
		cfg:     cfg,
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

	// Read response line.
	line, err := e.fromServer.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw: %s", err, line)
	}

	return resp
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

	srv := &mcpServer{
		builder: blockingBuilder,
		store:   store,
		cfg:     cfg,
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
