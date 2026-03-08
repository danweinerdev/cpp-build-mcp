package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"regexp"
	"strconv"
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
	registry *configRegistry
}

// resolveConfig extracts the optional "config" parameter from a tool request
// and returns the corresponding configInstance. If no config parameter is
// provided, the default instance is returned.
func (srv *mcpServer) resolveConfig(req mcp.CallToolRequest) (*configInstance, error) {
	name := req.GetString("config", "")
	if name == "" {
		return srv.registry.defaultInstance(), nil
	}
	return srv.registry.get(name)
}

func main() {
	configs, defaultName, err := config.LoadMulti(".")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	registry := newConfigRegistry(defaultName)
	for name, cfg := range configs {
		b, err := builder.NewBuilder(cfg)
		if err != nil {
			log.Fatalf("failed to create builder for config %q: %v", name, err)
		}

		inst := &configInstance{
			name:    name,
			cfg:     cfg,
			builder: b,
			store:   state.NewStore(),
		}
		registry.add(inst)

		// Run toolchain detection eagerly at startup so the lazy call in
		// handleBuild finds a concrete toolchain and skips filesystem detection.
		inst.cfg.Toolchain = resolveToolchain(inst)
	}

	srv := &mcpServer{
		registry: registry,
	}

	s := server.NewMCPServer("cpp-build-mcp", "0.1.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
	)

	s.AddTool(
		mcp.NewTool("build",
			mcp.WithDescription("Build the C/C++ project using CMake."),
			mcp.WithString("config", mcp.Description("Configuration name (omit for default)")),
			mcp.WithArray("targets", mcp.WithStringItems(), mcp.Description("Build targets to compile. If empty, builds the default target.")),
			mcp.WithNumber("jobs", mcp.Description("Number of parallel build jobs. 0 uses the build system default.")),
		),
		srv.handleBuild,
	)

	s.AddTool(
		mcp.NewTool("get_errors",
			mcp.WithDescription("Get the current list of build errors from the last build."),
			mcp.WithString("config", mcp.Description("Configuration name (omit for default)")),
		),
		srv.handleGetErrors,
	)

	s.AddTool(
		mcp.NewTool("get_warnings",
			mcp.WithDescription("Get the current list of build warnings from the last build."),
			mcp.WithString("config", mcp.Description("Configuration name (omit for default)")),
			mcp.WithString("filter", mcp.Description("Optional case-insensitive substring filter applied to diagnostic code or file path.")),
		),
		srv.handleGetWarnings,
	)

	s.AddTool(
		mcp.NewTool("configure",
			mcp.WithDescription("Run CMake configure step to generate the build system."),
			mcp.WithString("config", mcp.Description("Configuration name (omit for default)")),
			mcp.WithArray("cmake_args", mcp.WithStringItems(), mcp.Description("Additional CMake arguments to pass to the configure step.")),
		),
		srv.handleConfigure,
	)

	s.AddTool(
		mcp.NewTool("clean",
			mcp.WithDescription("Clean build artifacts."),
			mcp.WithString("config", mcp.Description("Configuration name (omit for default)")),
			mcp.WithArray("targets", mcp.WithStringItems(), mcp.Description("Specific targets to clean. If empty, cleans all.")),
		),
		srv.handleClean,
	)

	s.AddTool(
		mcp.NewTool("get_changed_files",
			mcp.WithDescription("Detect source files that have changed since the last successful build."),
			mcp.WithString("config", mcp.Description("Configuration name (omit for default)")),
		),
		srv.handleGetChangedFiles,
	)

	s.AddTool(
		mcp.NewTool("get_build_graph",
			mcp.WithDescription("Get a summary of the build graph from compile_commands.json."),
			mcp.WithString("config", mcp.Description("Configuration name (omit for default)")),
		),
		srv.handleGetBuildGraph,
	)

	s.AddTool(
		mcp.NewTool("suggest_fix",
			mcp.WithDescription("Get source context around a build error for fixing."),
			mcp.WithString("config", mcp.Description("Configuration name (omit for default)")),
			mcp.WithNumber("error_index", mcp.Description("Zero-based index into the error list from get_errors.")),
		),
		srv.handleSuggestFix,
	)

	s.AddTool(
		mcp.NewTool("list_configs",
			mcp.WithDescription("List all available build configurations and their current status."),
		),
		srv.handleListConfigs,
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

// listConfigsResponse is the JSON structure returned by the list_configs tool.
type listConfigsResponse struct {
	Configs       []ConfigSummary `json:"configs"`
	DefaultConfig string          `json:"default_config"`
}

// buildResponse is the JSON structure returned by the build tool.
type buildResponse struct {
	Config        string `json:"config"`
	ExitCode      int    `json:"exit_code"`
	ErrorCount    int    `json:"error_count"`
	WarningCount  int    `json:"warning_count"`
	DurationMs    int64  `json:"duration_ms"`
	FilesCompiled int    `json:"files_compiled"`
}

// getErrorsResponse is the JSON structure returned by the get_errors tool.
type getErrorsResponse struct {
	Config string            `json:"config"`
	Errors []diagnosticEntry `json:"errors"`
}

// getWarningsResponse is the JSON structure returned by the get_warnings tool.
type getWarningsResponse struct {
	Config   string            `json:"config"`
	Warnings []diagnosticEntry `json:"warnings"`
	Count    int               `json:"count"`
}

// configureResponse is the JSON structure returned by the configure tool.
type configureResponse struct {
	Config     string   `json:"config"`
	Success    bool     `json:"success"`
	ErrorCount int      `json:"error_count"`
	Messages   []string `json:"messages"`
}

// cleanResponse is the JSON structure returned by the clean tool.
type cleanResponse struct {
	Config  string `json:"config"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// changedFilesResponse is the JSON structure returned by the get_changed_files tool.
type changedFilesResponse struct {
	Config string   `json:"config"`
	Files  []string `json:"files"`
	Count  int      `json:"count"`
	Method string   `json:"method"`
}

// buildGraphResponse is the JSON structure returned by the get_build_graph tool.
// It wraps graph.GraphSummary so the config name can be included without
// modifying the graph package.
type buildGraphResponse struct {
	Config string `json:"config"`
	*graph.GraphSummary
}

// suggestFixResponse is the JSON structure returned by the suggest_fix tool.
type suggestFixResponse struct {
	Config     string          `json:"config"`
	File       string          `json:"file"`
	StartLine  int             `json:"start_line"`
	EndLine    int             `json:"end_line"`
	Source     string          `json:"source"`
	Diagnostic diagnosticEntry `json:"diagnostic"`
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

// resolveToolchain returns the effective toolchain string. If the configured
// toolchain is "auto" or empty, it runs auto-detection against
// compile_commands.json and the system compiler. When the detected toolchain
// is "gcc-legacy" (GCC < 10 without JSON diagnostics), diagnostic flag
// injection is disabled to prevent passing unsupported flags.
func resolveToolchain(inst *configInstance) string {
	tc := inst.cfg.Toolchain
	if tc == "auto" || tc == "" {
		tc = builder.DetectToolchain(inst.cfg.BuildDir)
		if tc == "gcc-legacy" {
			inst.cfg.InjectDiagnosticFlags = false
			slog.Warn("detected GCC < 10, disabling diagnostic flag injection")
		}
	}
	return tc
}

func (srv *mcpServer) handleListConfigs(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp := listConfigsResponse{
		Configs:       srv.registry.list(),
		DefaultConfig: srv.registry.dflt,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleBuild(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	inst, err := srv.resolveConfig(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Check if build can start (validates configured state and no build in progress).
	if err := inst.store.StartBuild(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// If the state is dirty, set the builder's dirty flag so it cleans first.
	wasDirty := inst.store.IsDirty()
	if wasDirty {
		inst.builder.SetDirty(true)
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
	result, buildErr := inst.builder.Build(ctx, targets, jobs)
	if buildErr != nil {
		// Process spawn error — finalize state and return tool error.
		inst.store.FinishBuild(-1, 0, nil, nil)
		return mcp.NewToolResultError("build failed to start: " + buildErr.Error()), nil
	}

	// Parse diagnostics from build output.
	tc := resolveToolchain(inst)
	diags, _ := diagnostics.Parse(tc, result.Stdout, result.Stderr)

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

	// If the build was killed (timeout/cancel), mark state as dirty.
	if result.Killed {
		inst.store.SetDirty()
	}

	// Update state with build results.
	inst.store.FinishBuild(result.ExitCode, result.Duration, errs, warns)

	// Clear dirty if the build succeeded, was dirty before, and was not killed.
	if wasDirty && result.ExitCode == 0 && !result.Killed {
		inst.store.ClearDirty()
	}

	// Build the response.
	resp := buildResponse{
		Config:        inst.name,
		ExitCode:      result.ExitCode,
		ErrorCount:    len(errs),
		WarningCount:  len(warns),
		DurationMs:    result.Duration.Milliseconds(),
		FilesCompiled: parseFilesCompiled(result.Stderr),
	}

	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return mcp.NewToolResultError("failed to marshal response: " + marshalErr.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleGetErrors(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	inst, err := srv.resolveConfig(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	errs := inst.store.Errors()

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

	resp := getErrorsResponse{Config: inst.name, Errors: entries}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleBuildHealth(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	var text string

	if srv.registry.len() == 1 {
		// Single config: return the existing verbose format for backward
		// compatibility (e.g., "OK: 0 errors, 2 warnings, last build 30s ago").
		inst := srv.registry.defaultInstance()
		text = inst.store.Health()
	} else {
		// Multiple configs: return pipe-separated aggregate format sorted by
		// name (e.g., "debug: OK | release: FAIL(3 errors) | asan: UNCONFIGURED").
		instances := srv.registry.all()
		parts := make([]string, len(instances))
		for i, inst := range instances {
			parts[i] = inst.name + ": " + aggregateHealthToken(inst.store)
		}
		text = strings.Join(parts, " | ")
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "build://health",
			MIMEType: "text/plain",
			Text:     text,
		},
	}, nil
}

func (srv *mcpServer) handleGetWarnings(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	inst, err := srv.resolveConfig(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	warns := inst.store.Warnings()

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
		Config:   inst.name,
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
	inst, err := srv.resolveConfig(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

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

	result, configErr := inst.builder.Configure(ctx, args)
	if configErr != nil {
		return mcp.NewToolResultError("configure failed to start: " + configErr.Error()), nil
	}

	// Parse CMake output for error/warning messages.
	combined := result.Stdout + "\n" + result.Stderr
	messages, errorCount := parseCMakeMessages(combined)

	success := result.ExitCode == 0
	if success {
		inst.store.SetConfigured()
	}

	resp := configureResponse{
		Config:     inst.name,
		Success:    success,
		ErrorCount: errorCount,
		Messages:   messages,
	}

	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return mcp.NewToolResultError("failed to marshal response: " + marshalErr.Error()), nil
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
	inst, err := srv.resolveConfig(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	result, cleanErr := inst.builder.Clean(ctx, targets)
	if cleanErr != nil {
		return mcp.NewToolResultError("clean failed: " + cleanErr.Error()), nil
	}

	if result.ExitCode != 0 {
		return mcp.NewToolResultError("clean failed with exit code " + strings.TrimSpace(result.Stderr)), nil
	}

	inst.store.SetClean()

	resp := cleanResponse{
		Config:  inst.name,
		Success: true,
		Message: "Clean complete",
	}

	data, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return mcp.NewToolResultError("failed to marshal response: " + marshalErr.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleGetChangedFiles(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	inst, err := srv.resolveConfig(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	since := inst.store.LastSuccessfulBuildTime()

	files, method, err := changes.DetectChanges(inst.cfg.SourceDir, inst.cfg.BuildDir, since)
	if err != nil {
		return mcp.NewToolResultError("failed to detect changes: " + err.Error()), nil
	}

	if files == nil {
		files = []string{}
	}

	resp := changedFilesResponse{
		Config: inst.name,
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

func (srv *mcpServer) handleGetBuildGraph(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	inst, err := srv.resolveConfig(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	summary, err := graph.ReadSummary(inst.cfg.BuildDir, inst.cfg.SourceDir)
	if err != nil {
		return mcp.NewToolResultError("failed to read build graph: " + err.Error()), nil
	}

	resp := buildGraphResponse{
		Config:       inst.name,
		GraphSummary: summary,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (srv *mcpServer) handleSuggestFix(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	inst, err := srv.resolveConfig(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	idx := req.GetInt("error_index", -1)
	if idx < 0 {
		return mcp.NewToolResultError("error_index is required and must be >= 0"), nil
	}

	errs := inst.store.Errors()
	if idx >= len(errs) {
		return mcp.NewToolResultError(fmt.Sprintf("error_index %d out of range (have %d errors)", idx, len(errs))), nil
	}

	diag := errs[idx]

	content, err := os.ReadFile(diag.File)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("cannot read source file %q: %s", diag.File, err.Error())), nil
	}

	lines := strings.Split(string(content), "\n")
	totalLines := len(lines)

	// Handle file-level diagnostics where the compiler reports no specific line.
	diagLine := diag.Line
	if diagLine <= 0 {
		diagLine = 1
	}

	// Diagnostic lines are 1-based. Compute the context window [startLine, endLine] (1-based, inclusive).
	startLine := diagLine - 10
	if startLine < 1 {
		startLine = 1
	}
	endLine := diagLine + 10
	if endLine > totalLines {
		endLine = totalLines
	}

	// Extract the snippet (convert to 0-based indexing for slicing).
	snippet := strings.Join(lines[startLine-1:endLine], "\n")

	resp := suggestFixResponse{
		Config:    inst.name,
		File:      diag.File,
		StartLine: startLine,
		EndLine:   endLine,
		Source:    snippet,
		Diagnostic: diagnosticEntry{
			File:     diag.File,
			Line:     diag.Line,
			Column:   diag.Column,
			Severity: string(diag.Severity),
			Message:  diag.Message,
			Code:     diag.Code,
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError("failed to marshal response: " + err.Error()), nil
	}

	return mcp.NewToolResultText(string(data)), nil
}

// parseFilesCompiled extracts the number of files compiled from build output.
// For Ninja builds, it parses [N/M] progress lines and returns the highest N.
// For Make builds (when no Ninja progress is found), it counts lines that look
// like compiler invocations.
func parseFilesCompiled(stderr string) int {
	// Look for Ninja progress pattern [N/M] at start of line.
	ninjaRe := regexp.MustCompile(`^\[(\d+)/\d+\]`)
	highest := 0
	for _, line := range strings.Split(stderr, "\n") {
		if m := ninjaRe.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n > highest {
				highest = n
			}
		}
	}
	if highest > 0 {
		return highest
	}

	// Fallback for Make: count compiler invocation lines.
	compilerRe := regexp.MustCompile(`^\s*(gcc|g\+\+|clang|clang\+\+|cl\.exe|cc|c\+\+)\s`)
	count := 0
	for _, line := range strings.Split(stderr, "\n") {
		if compilerRe.MatchString(line) {
			count++
		}
	}
	return count
}
