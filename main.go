package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/danweinerdev/cpp-build-mcp/builder"
	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/diagnostics"
	"github.com/danweinerdev/cpp-build-mcp/state"
)

// mcpServer holds the dependencies shared across MCP tool handlers.
type mcpServer struct {
	builder builder.Builder
	store   *state.Store
	cfg     *config.Config
}

func main() {
	cfg, err := config.Load(".")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	b, err := builder.NewBuilder(cfg)
	if err != nil {
		log.Fatalf("failed to create builder: %v", err)
	}

	store := state.NewStore()

	srv := &mcpServer{
		builder: b,
		store:   store,
		cfg:     cfg,
	}

	s := server.NewMCPServer("cpp-build-mcp", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
	)

	s.AddTool(
		mcp.NewTool("build",
			mcp.WithDescription("Build the C/C++ project using CMake."),
			mcp.WithArray("targets", mcp.WithStringItems(), mcp.Description("Build targets to compile. If empty, builds the default target.")),
			mcp.WithNumber("jobs", mcp.Description("Number of parallel build jobs. 0 uses the build system default.")),
		),
		srv.handleBuild,
	)

	s.AddTool(
		mcp.NewTool("get_errors",
			mcp.WithDescription("Get the current list of build errors from the last build."),
		),
		srv.handleGetErrors,
	)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// buildResponse is the JSON structure returned by the build tool.
type buildResponse struct {
	ExitCode      int   `json:"exit_code"`
	ErrorCount    int   `json:"error_count"`
	WarningCount  int   `json:"warning_count"`
	DurationMs    int64 `json:"duration_ms"`
	FilesCompiled int   `json:"files_compiled"`
}

// getErrorsResponse is the JSON structure returned by the get_errors tool.
type getErrorsResponse struct {
	Errors []diagnosticEntry `json:"errors"`
}

// diagnosticEntry represents a single diagnostic in the get_errors response.
// Fields with omitempty are excluded when empty, matching the Diagnostic struct
// convention.
type diagnosticEntry struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity,omitempty"`
	Message  string `json:"message,omitempty"`
	Code     string `json:"code,omitempty"`
}

func (srv *mcpServer) handleBuild(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Check if build can start (validates configured state and no build in progress).
	if err := srv.store.StartBuild(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// If the state is dirty, set the builder's dirty flag so it cleans first.
	wasDirty := srv.store.IsDirty()
	if wasDirty {
		if cb, ok := srv.builder.(*builder.CMakeBuilder); ok {
			cb.SetDirty(true)
		}
	}

	// Extract optional targets parameter.
	var targets []string
	if rawTargets, ok := req.GetArguments()["targets"]; ok {
		if arr, ok := rawTargets.([]interface{}); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					targets = append(targets, s)
				}
			}
		}
	}

	// Extract optional jobs parameter.
	jobs := req.GetInt("jobs", 0)

	// Run the build.
	result, err := srv.builder.Build(ctx, targets, jobs)
	if err != nil {
		// Process spawn error — finalize state and return tool error.
		srv.store.FinishBuild(-1, 0, nil, nil)
		return mcp.NewToolResultError("build failed to start: " + err.Error()), nil
	}

	// Parse diagnostics from build output.
	diags, _ := diagnostics.Parse(srv.cfg.Toolchain, result.Stdout, result.Stderr)

	// Split diagnostics into errors and warnings.
	var errs, warns []diagnostics.Diagnostic
	for _, d := range diags {
		switch d.Severity {
		case diagnostics.SeverityError:
			errs = append(errs, d)
		case diagnostics.SeverityWarning:
			warns = append(warns, d)
		default:
			// Notes and other severities are not tracked separately.
		}
	}

	// Update state with build results.
	srv.store.FinishBuild(result.ExitCode, result.Duration, errs, warns)

	// Clear dirty if the build succeeded and was dirty.
	if wasDirty && result.ExitCode == 0 {
		srv.store.ClearDirty()
	}

	// Build the response.
	resp := buildResponse{
		ExitCode:      result.ExitCode,
		ErrorCount:    len(errs),
		WarningCount:  len(warns),
		DurationMs:    result.Duration.Milliseconds(),
		FilesCompiled: 0,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleGetErrors(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	errs := srv.store.Errors()

	// Cap at 20 diagnostics.
	if len(errs) > 20 {
		errs = errs[:20]
	}

	entries := make([]diagnosticEntry, len(errs))
	for i, d := range errs {
		entries[i] = diagnosticEntry{
			File:     d.File,
			Line:     d.Line,
			Column:   d.Column,
			Severity: string(d.Severity),
			Message:  d.Message,
			Code:     d.Code,
		}
	}

	resp := getErrorsResponse{Errors: entries}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}
