package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/danweinerdev/cpp-build-mcp/builder"
	"github.com/danweinerdev/cpp-build-mcp/changes"
	"github.com/danweinerdev/cpp-build-mcp/config"
	"github.com/danweinerdev/cpp-build-mcp/diagnostics"
	"github.com/danweinerdev/cpp-build-mcp/graph"
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

	s.AddTool(
		mcp.NewTool("get_warnings",
			mcp.WithDescription("Get the current list of build warnings from the last build."),
			mcp.WithString("filter", mcp.Description("Optional case-insensitive substring filter applied to diagnostic code or file path.")),
		),
		srv.handleGetWarnings,
	)

	s.AddTool(
		mcp.NewTool("configure",
			mcp.WithDescription("Run CMake configure step to generate the build system."),
			mcp.WithArray("cmake_args", mcp.WithStringItems(), mcp.Description("Additional CMake arguments to pass to the configure step.")),
		),
		srv.handleConfigure,
	)

	s.AddTool(
		mcp.NewTool("clean",
			mcp.WithDescription("Clean build artifacts."),
			mcp.WithArray("targets", mcp.WithStringItems(), mcp.Description("Specific targets to clean. If empty, cleans all.")),
		),
		srv.handleClean,
	)

	s.AddTool(
		mcp.NewTool("get_changed_files",
			mcp.WithDescription("Detect source files that have changed since the last successful build."),
		),
		srv.handleGetChangedFiles,
	)

	s.AddTool(
		mcp.NewTool("get_build_graph",
			mcp.WithDescription("Get a summary of the build graph from compile_commands.json."),
		),
		srv.handleGetBuildGraph,
	)

	s.AddResource(
		mcp.NewResource("build://health", "Build Health",
			mcp.WithResourceDescription("One-line summary of build system state"),
			mcp.WithMIMEType("text/plain"),
		),
		srv.handleBuildHealth,
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

// getWarningsResponse is the JSON structure returned by the get_warnings tool.
type getWarningsResponse struct {
	Warnings []diagnosticEntry `json:"warnings"`
	Count    int               `json:"count"`
}

// configureResponse is the JSON structure returned by the configure tool.
type configureResponse struct {
	Success    bool     `json:"success"`
	ErrorCount int      `json:"error_count"`
	Messages   []string `json:"messages"`
}

// cleanResponse is the JSON structure returned by the clean tool.
type cleanResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// changedFilesResponse is the JSON structure returned by the get_changed_files tool.
type changedFilesResponse struct {
	Files  []string `json:"files"`
	Count  int      `json:"count"`
	Method string   `json:"method"`
}

// buildGraphResponse is the JSON structure returned by the get_build_graph tool.
// It directly uses graph.GraphSummary for marshaling.

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

func (srv *mcpServer) handleBuildHealth(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "build://health",
			MIMEType: "text/plain",
			Text:     srv.store.Health(),
		},
	}, nil
}

func (srv *mcpServer) handleGetWarnings(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	warns := srv.store.Warnings()

	filter := req.GetString("filter", "")

	var entries []diagnosticEntry
	for _, d := range warns {
		if filter != "" {
			lowerFilter := strings.ToLower(filter)
			matchCode := strings.Contains(strings.ToLower(d.Code), lowerFilter)
			matchFile := strings.Contains(strings.ToLower(d.File), lowerFilter)
			if !matchCode && !matchFile {
				continue
			}
		}
		entries = append(entries, diagnosticEntry{
			File:     d.File,
			Line:     d.Line,
			Column:   d.Column,
			Severity: string(d.Severity),
			Message:  d.Message,
			Code:     d.Code,
		})
	}

	if entries == nil {
		entries = []diagnosticEntry{}
	}

	resp := getWarningsResponse{
		Warnings: entries,
		Count:    len(entries),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleConfigure(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract optional cmake_args parameter.
	var args []string
	if rawArgs, ok := req.GetArguments()["cmake_args"]; ok {
		if arr, ok := rawArgs.([]interface{}); ok {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					args = append(args, s)
				}
			}
		}
	}

	result, err := srv.builder.Configure(ctx, args)
	if err != nil {
		return mcp.NewToolResultError("configure failed to start: " + err.Error()), nil
	}

	// Parse CMake output for error/warning messages.
	combined := result.Stdout + "\n" + result.Stderr
	messages, errorCount := parseCMakeMessages(combined)

	success := result.ExitCode == 0
	if success {
		srv.store.SetConfigured()
	}

	resp := configureResponse{
		Success:    success,
		ErrorCount: errorCount,
		Messages:   messages,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// parseCMakeMessages splits CMake output into message groups starting with
// "CMake Error" or "CMake Warning" prefixes. It returns the messages and the
// count of error messages.
func parseCMakeMessages(output string) ([]string, int) {
	lines := strings.Split(output, "\n")
	var messages []string
	var current strings.Builder
	inMessage := false
	errorCount := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "CMake Error") || strings.HasPrefix(line, "CMake Warning") {
			// Save any previous message.
			if inMessage && current.Len() > 0 {
				messages = append(messages, strings.TrimSpace(current.String()))
			}
			current.Reset()
			current.WriteString(line)
			inMessage = true
			if strings.HasPrefix(line, "CMake Error") {
				errorCount++
			}
		} else if inMessage {
			current.WriteString("\n")
			current.WriteString(line)
		}
	}
	// Save the last message.
	if inMessage && current.Len() > 0 {
		messages = append(messages, strings.TrimSpace(current.String()))
	}

	if messages == nil {
		messages = []string{}
	}

	return messages, errorCount
}

func (srv *mcpServer) handleClean(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	result, err := srv.builder.Clean(ctx, targets)
	if err != nil {
		return mcp.NewToolResultError("clean failed: " + err.Error()), nil
	}

	if result.ExitCode != 0 {
		return mcp.NewToolResultError("clean failed with exit code " + strings.TrimSpace(result.Stderr)), nil
	}

	srv.store.SetClean()

	resp := cleanResponse{
		Success: true,
		Message: "Clean complete",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleGetChangedFiles(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	since := srv.store.LastSuccessfulBuildTime()

	files, method, err := changes.DetectChanges(srv.cfg.SourceDir, srv.cfg.BuildDir, since)
	if err != nil {
		return mcp.NewToolResultError("failed to detect changes: " + err.Error()), nil
	}

	if files == nil {
		files = []string{}
	}

	resp := changedFilesResponse{
		Files:  files,
		Count:  len(files),
		Method: method,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleGetBuildGraph(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	summary, err := graph.ReadSummary(srv.cfg.BuildDir, srv.cfg.SourceDir)
	if err != nil {
		return mcp.NewToolResultError("failed to read build graph: " + err.Error()), nil
	}

	data, err := json.Marshal(summary)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}
