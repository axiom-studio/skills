package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconDbt = "database"
)

// ============================================================================
// CONFIG STRUCTURES
// ============================================================================

// DbtConfig holds common dbt configuration
type DbtConfig struct {
	ProjectDir     string `json:"projectDir" description:"Path to the dbt project directory"`
	ProfilesDir    string `json:"profilesDir" description:"Path to the dbt profiles directory (default: ~/.dbt)"`
	Profile        string `json:"profile" description:"dbt profile name to use"`
	Target         string `json:"target" description:"dbt target environment (e.g., dev, prod)"`
	Select         string `json:"select" description:"Model selection syntax (e.g., my_model, tag:nightly)"`
	Exclude        string `json:"exclude" description:"Models to exclude from the operation"`
	FullRefresh    bool   `json:"fullRefresh" description:"Run models with full refresh (drop and recreate)"`
	FailFast       bool   `json:"failFast" description:"Stop execution on first failure"`
	WarnError      bool   `json:"warnError" description:"Treat warnings as errors"`
	Threads        int    `json:"threads" description:"Number of threads to use (default: from profile)"`
	SingleThreaded bool   `json:"singleThreaded" description:"Run in single-threaded mode"`
}

// DbtTestConfig extends DbtConfig with test-specific options
type DbtTestConfig struct {
	DbtConfig
	Severity        string `json:"severity" description:"Minimum severity level (warn, error)"`
	StoreFailures   bool   `json:"storeFailures" description:"Store test failures as tables"`
	IndirectSelection string `json:"indirectSelection" description:"Indirect selection mode (eager, cautious, buildable)"`
}

// DbtDocsConfig for documentation generation
type DbtDocsConfig struct {
	DbtConfig
	TargetPath string `json:"targetPath" description:"Custom target path for generated docs"`
}

// DbtSeedConfig for seed operations
type DbtSeedConfig struct {
	DbtConfig
	FullRefresh bool `json:"fullRefresh" description:"Truncate and reload seed data"`
	Show        bool `json:"show" description:"Show a sample of the loaded data"`
}

// DbtSnapshotConfig for snapshot operations
type DbtSnapshotConfig struct {
	DbtConfig
	Select string `json:"select" description:"Snapshot selection syntax"`
}

// DbtListConfig for list operations
type DbtListConfig struct {
	DbtConfig
	OutputType   string `json:"outputType" description:"Output format (name, json, text)"`
	OutputKeys   string `json:"outputKeys" description:"Comma-separated list of keys to output (for json format)"`
	ResourceType string `json:"resourceType" description:"Resource type to list (model, test, seed, snapshot, source, analysis)"`
}

// DbtDebugConfig for debug operations
type DbtDebugConfig struct {
	DbtConfig
	ConfigDir bool `json:"configDir" description:"Print the directory containing dbt.yml files"`
}

// DbtCompileConfig for compile operations
type DbtCompileConfig struct {
	DbtConfig
	NoCompile bool `json:"noCompile" description:"Skip compilation step"`
}

// ============================================================================
// DBT EXECUTOR BASE
// ============================================================================

// DbtExecutor provides common dbt functionality
type DbtExecutor struct {
	nodeType string
}

func (e *DbtExecutor) Type() string {
	return e.nodeType
}

// runDbtCommand executes a dbt CLI command
func (e *DbtExecutor) runDbtCommand(ctx context.Context, config map[string]interface{}, subcommand string, args []string) (*DbtResult, error) {
	dbtCfg := parseDbtConfig(config)

	// Validate project directory
	if dbtCfg.ProjectDir == "" {
		return nil, fmt.Errorf("projectDir is required")
	}

	// Check if project directory exists
	if _, err := os.Stat(dbtCfg.ProjectDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("project directory does not exist: %s", dbtCfg.ProjectDir)
	}

	// Build dbt command
	cmdArgs := []string{subcommand}

	// Add profile if specified
	if dbtCfg.Profile != "" {
		cmdArgs = append(cmdArgs, "--profile", dbtCfg.Profile)
	}

	// Add target if specified
	if dbtCfg.Target != "" {
		cmdArgs = append(cmdArgs, "--target", dbtCfg.Target)
	}

	// Add profiles-dir if specified
	if dbtCfg.ProfilesDir != "" {
		cmdArgs = append(cmdArgs, "--profiles-dir", dbtCfg.ProfilesDir)
	}

	// Add project-dir
	cmdArgs = append(cmdArgs, "--project-dir", dbtCfg.ProjectDir)

	// Add select if specified
	if dbtCfg.Select != "" {
		cmdArgs = append(cmdArgs, "--select", dbtCfg.Select)
	}

	// Add exclude if specified
	if dbtCfg.Exclude != "" {
		cmdArgs = append(cmdArgs, "--exclude", dbtCfg.Exclude)
	}

	// Add full-refresh if specified
	if dbtCfg.FullRefresh {
		cmdArgs = append(cmdArgs, "--full-refresh")
	}

	// Add fail-fast if specified
	if dbtCfg.FailFast {
		cmdArgs = append(cmdArgs, "--fail-fast")
	}

	// Add warn-error if specified
	if dbtCfg.WarnError {
		cmdArgs = append(cmdArgs, "--warn-error")
	}

	// Add threads if specified
	if dbtCfg.Threads > 0 {
		cmdArgs = append(cmdArgs, "--threads", fmt.Sprintf("%d", dbtCfg.Threads))
	}

	// Add single-threaded if specified
	if dbtCfg.SingleThreaded {
		cmdArgs = append(cmdArgs, "--single-threaded")
	}

	// Add additional args
	cmdArgs = append(cmdArgs, args...)

	// Create command
	cmd := exec.CommandContext(ctx, "dbt", cmdArgs...)
	cmd.Dir = dbtCfg.ProjectDir

	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Record start time
	startTime := time.Now()

	// Execute command
	err := cmd.Run()
	duration := time.Since(startTime)

	result := &DbtResult{
		Success:  err == nil,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration.Milliseconds(),
		Command:  fmt.Sprintf("dbt %s", strings.Join(cmdArgs, " ")),
	}

	if err != nil {
		// Check for exit error to get exit code
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		result.Error = err.Error()
	}

	return result, nil
}

// parseDbtConfig extracts dbt configuration from config map
func parseDbtConfig(config map[string]interface{}) DbtConfig {
	return DbtConfig{
		ProjectDir:     getString(config, "projectDir"),
		ProfilesDir:    getString(config, "profilesDir"),
		Profile:        getString(config, "profile"),
		Target:         getString(config, "target"),
		Select:         getString(config, "select"),
		Exclude:        getString(config, "exclude"),
		FullRefresh:    getBool(config, "fullRefresh", false),
		FailFast:       getBool(config, "failFast", false),
		WarnError:      getBool(config, "warnError", false),
		Threads:        getInt(config, "threads", 0),
		SingleThreaded: getBool(config, "singleThreaded", false),
	}
}

// DbtResult holds the result of a dbt command execution
type DbtResult struct {
	Success  bool   `json:"success"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Duration int64  `json:"durationMs"`
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
	Command  string `json:"command"`
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getString safely gets a string from a map
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getInt safely gets an int from a map
func getInt(config map[string]interface{}, key string, def int) int {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return def
}

// getBool safely gets a bool from a map
func getBool(config map[string]interface{}, key string, def bool) bool {
	if v, ok := config[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// getMap safely gets a map from a map
func getMap(config map[string]interface{}, key string) map[string]interface{} {
	if v, ok := config[key]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			return m
		}
	}
	return nil
}

// parseDbtOutput attempts to parse dbt's JSON output (from manifest or run results)
func parseDbtOutput(output string) map[string]interface{} {
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err == nil {
		return result
	}
	return nil
}

// extractRunResults extracts summary information from dbt run results
func extractRunResults(stdout string) map[string]interface{} {
	results := make(map[string]interface{})

	// Try to parse JSON output first
	if jsonResult := parseDbtOutput(stdout); jsonResult != nil {
		return jsonResult
	}

	// Parse text output for summary
	lines := strings.Split(stdout, "\n")
	var executedModels []string
	var passedTests []string
	var failedTests []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Check for model execution patterns
		if strings.Contains(line, "OK created") || strings.Contains(line, "PASS") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				for _, part := range parts {
					if strings.HasPrefix(part, "model.") || strings.HasPrefix(part, "test.") {
						if strings.Contains(line, "PASS") {
							passedTests = append(passedTests, part)
						} else {
							executedModels = append(executedModels, part)
						}
					}
				}
			}
		}

		// Check for failures
		if strings.Contains(line, "FAIL") || strings.Contains(line, "ERROR") {
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.HasPrefix(part, "model.") || strings.HasPrefix(part, "test.") {
					failedTests = append(failedTests, part)
				}
			}
		}
	}

	results["executedModels"] = executedModels
	results["passedTests"] = passedTests
	results["failedTests"] = failedTests
	results["totalExecuted"] = len(executedModels)
	results["totalPassed"] = len(passedTests)
	results["totalFailed"] = len(failedTests)

	return results
}

// ============================================================================
// DBT-RUN EXECUTOR
// ============================================================================

// DbtRunExecutor handles dbt-run node type
type DbtRunExecutor struct {
	DbtExecutor
}

func NewDbtRunExecutor() *DbtRunExecutor {
	return &DbtRunExecutor{
		DbtExecutor: DbtExecutor{nodeType: "dbt-run"},
	}
}

func (e *DbtRunExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	result, err := e.runDbtCommand(ctx, config, "run", []string{})
	if err != nil && result == nil {
		return nil, fmt.Errorf("failed to execute dbt run: %w", err)
	}

	output := map[string]interface{}{
		"success":  result.Success,
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"duration": result.Duration,
		"exitCode": result.ExitCode,
		"command":  result.Command,
	}

	// Extract run results if successful
	if result.Success {
		runResults := extractRunResults(result.Stdout)
		output["runResults"] = runResults
	}

	if !result.Success {
		output["error"] = result.Error
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// DBT-TEST EXECUTOR
// ============================================================================

// DbtTestExecutor handles dbt-test node type
type DbtTestExecutor struct {
	DbtExecutor
}

func NewDbtTestExecutor() *DbtTestExecutor {
	return &DbtTestExecutor{
		DbtExecutor: DbtExecutor{nodeType: "dbt-test"},
	}
}

func (e *DbtTestExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	args := []string{}

	// Add test-specific options
	if severity := getString(config, "severity"); severity != "" {
		args = append(args, "--severity", severity)
	}

	if storeFailures := getBool(config, "storeFailures", false); storeFailures {
		args = append(args, "--store-failures")
	}

	if indirectSelection := getString(config, "indirectSelection"); indirectSelection != "" {
		args = append(args, "--indirect-selection", indirectSelection)
	}

	result, err := e.runDbtCommand(ctx, config, "test", args)
	if err != nil && result == nil {
		return nil, fmt.Errorf("failed to execute dbt test: %w", err)
	}

	output := map[string]interface{}{
		"success":  result.Success,
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"duration": result.Duration,
		"exitCode": result.ExitCode,
		"command":  result.Command,
	}

	// Extract test results
	if result.Success {
		testResults := extractRunResults(result.Stdout)
		output["testResults"] = testResults
	}

	if !result.Success {
		output["error"] = result.Error
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// DBT-COMPILE EXECUTOR
// ============================================================================

// DbtCompileExecutor handles dbt-compile node type
type DbtCompileExecutor struct {
	DbtExecutor
}

func NewDbtCompileExecutor() *DbtCompileExecutor {
	return &DbtCompileExecutor{
		DbtExecutor: DbtExecutor{nodeType: "dbt-compile"},
	}
}

func (e *DbtCompileExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	args := []string{}

	// Add compile-specific options
	if noCompile := getBool(config, "noCompile", false); noCompile {
		args = append(args, "--no-compile")
	}

	result, err := e.runDbtCommand(ctx, config, "compile", args)
	if err != nil && result == nil {
		return nil, fmt.Errorf("failed to execute dbt compile: %w", err)
	}

	output := map[string]interface{}{
		"success":  result.Success,
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"duration": result.Duration,
		"exitCode": result.ExitCode,
		"command":  result.Command,
	}

	// Check if manifest was generated
	projectDir := getString(config, "projectDir")
	manifestPath := filepath.Join(projectDir, "target", "manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		output["manifestGenerated"] = true
		output["manifestPath"] = manifestPath

		// Try to read and parse manifest
		if manifestData, err := os.ReadFile(manifestPath); err == nil {
			var manifest map[string]interface{}
			if err := json.Unmarshal(manifestData, &manifest); err == nil {
				// Extract useful manifest info
				if nodes, ok := manifest["nodes"].(map[string]interface{}); ok {
					output["modelCount"] = len(nodes)
				}
				if metadata, ok := manifest["metadata"].(map[string]interface{}); ok {
					if version, ok := metadata["dbt_version"].(string); ok {
						output["dbtVersion"] = version
					}
				}
			}
		}
	}

	if !result.Success {
		output["error"] = result.Error
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// DBT-DOCS-GENERATE EXECUTOR
// ============================================================================

// DbtDocsGenerateExecutor handles dbt-docs-generate node type
type DbtDocsGenerateExecutor struct {
	DbtExecutor
}

func NewDbtDocsGenerateExecutor() *DbtDocsGenerateExecutor {
	return &DbtDocsGenerateExecutor{
		DbtExecutor: DbtExecutor{nodeType: "dbt-docs-generate"},
	}
}

func (e *DbtDocsGenerateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	result, err := e.runDbtCommand(ctx, config, "docs", []string{"generate"})
	if err != nil && result == nil {
		return nil, fmt.Errorf("failed to execute dbt docs generate: %w", err)
	}

	output := map[string]interface{}{
		"success":  result.Success,
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"duration": result.Duration,
		"exitCode": result.ExitCode,
		"command":  result.Command,
	}

	// Check if catalog was generated
	projectDir := getString(config, "projectDir")
	catalogPath := filepath.Join(projectDir, "target", "catalog.json")
	if _, err := os.Stat(catalogPath); err == nil {
		output["catalogGenerated"] = true
		output["catalogPath"] = catalogPath

		// Try to read catalog info
		if catalogData, err := os.ReadFile(catalogPath); err == nil {
			var catalog map[string]interface{}
			if err := json.Unmarshal(catalogData, &catalog); err == nil {
				if sources, ok := catalog["sources"].(map[string]interface{}); ok {
					output["sourceCount"] = len(sources)
				}
				if nodes, ok := catalog["nodes"].(map[string]interface{}); ok {
					output["nodeCount"] = len(nodes)
				}
			}
		}
	}

	if !result.Success {
		output["error"] = result.Error
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// DBT-SEED EXECUTOR
// ============================================================================

// DbtSeedExecutor handles dbt-seed node type
type DbtSeedExecutor struct {
	DbtExecutor
}

func NewDbtSeedExecutor() *DbtSeedExecutor {
	return &DbtSeedExecutor{
		DbtExecutor: DbtExecutor{nodeType: "dbt-seed"},
	}
}

func (e *DbtSeedExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	args := []string{}

	// Add seed-specific options
	if fullRefresh := getBool(config, "fullRefresh", false); fullRefresh {
		args = append(args, "--full-refresh")
	}

	if show := getBool(config, "show", false); show {
		args = append(args, "--show")
	}

	result, err := e.runDbtCommand(ctx, config, "seed", args)
	if err != nil && result == nil {
		return nil, fmt.Errorf("failed to execute dbt seed: %w", err)
	}

	output := map[string]interface{}{
		"success":  result.Success,
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"duration": result.Duration,
		"exitCode": result.ExitCode,
		"command":  result.Command,
	}

	// Extract seed results
	if result.Success {
		seedResults := extractRunResults(result.Stdout)
		output["seedResults"] = seedResults
	}

	if !result.Success {
		output["error"] = result.Error
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// DBT-SNAPSHOT EXECUTOR
// ============================================================================

// DbtSnapshotExecutor handles dbt-snapshot node type
type DbtSnapshotExecutor struct {
	DbtExecutor
}

func NewDbtSnapshotExecutor() *DbtSnapshotExecutor {
	return &DbtSnapshotExecutor{
		DbtExecutor: DbtExecutor{nodeType: "dbt-snapshot"},
	}
}

func (e *DbtSnapshotExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	result, err := e.runDbtCommand(ctx, config, "snapshot", []string{})
	if err != nil && result == nil {
		return nil, fmt.Errorf("failed to execute dbt snapshot: %w", err)
	}

	output := map[string]interface{}{
		"success":  result.Success,
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"duration": result.Duration,
		"exitCode": result.ExitCode,
		"command":  result.Command,
	}

	// Extract snapshot results
	if result.Success {
		snapshotResults := extractRunResults(result.Stdout)
		output["snapshotResults"] = snapshotResults
	}

	if !result.Success {
		output["error"] = result.Error
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// ============================================================================
// DBT-DEBUG EXECUTOR
// ============================================================================

// DbtDebugExecutor handles dbt-debug node type
type DbtDebugExecutor struct {
	DbtExecutor
}

func NewDbtDebugExecutor() *DbtDebugExecutor {
	return &DbtDebugExecutor{
		DbtExecutor: DbtExecutor{nodeType: "dbt-debug"},
	}
}

func (e *DbtDebugExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	args := []string{}

	// Add debug-specific options
	if configDir := getBool(config, "configDir", false); configDir {
		args = append(args, "--config-dir")
	}

	result, err := e.runDbtCommand(ctx, config, "debug", args)
	if err != nil && result == nil {
		return nil, fmt.Errorf("failed to execute dbt debug: %w", err)
	}

	output := map[string]interface{}{
		"success":  result.Success,
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"duration": result.Duration,
		"exitCode": result.ExitCode,
		"command":  result.Command,
	}

	// Parse debug output for connection status
	stdout := result.Stdout
	output["connectionOk"] = strings.Contains(stdout, "All checks passed!")
	output["dbtVersion"] = extractDbtVersion(stdout)

	// Extract profile info from debug output
	if strings.Contains(stdout, "profiles.yml") {
		output["profilesFound"] = true
	}

	if !result.Success {
		output["error"] = result.Error
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// extractDbtVersion extracts version from debug output
func extractDbtVersion(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Core:") {
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "Core:" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
		}
	}
	return ""
}

// ============================================================================
// DBT-LIST EXECUTOR
// ============================================================================

// DbtListExecutor handles dbt-list node type
type DbtListExecutor struct {
	DbtExecutor
}

func NewDbtListExecutor() *DbtListExecutor {
	return &DbtListExecutor{
		DbtExecutor: DbtExecutor{nodeType: "dbt-list"},
	}
}

func (e *DbtListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	args := []string{}

	// Add list-specific options
	if outputType := getString(config, "outputType"); outputType != "" {
		args = append(args, "--output", outputType)
	}

	if outputKeys := getString(config, "outputKeys"); outputKeys != "" {
		args = append(args, "--output-keys", outputKeys)
	}

	if resourceType := getString(config, "resourceType"); resourceType != "" {
		args = append(args, "--resource-type", resourceType)
	}

	result, err := e.runDbtCommand(ctx, config, "list", args)
	if err != nil && result == nil {
		return nil, fmt.Errorf("failed to execute dbt list: %w", err)
	}

	output := map[string]interface{}{
		"success":  result.Success,
		"stdout":   result.Stdout,
		"stderr":   result.Stderr,
		"duration": result.Duration,
		"exitCode": result.ExitCode,
		"command":  result.Command,
	}

	// Parse list output
	if result.Success {
		items := parseListOutput(result.Stdout)
		output["items"] = items
		output["count"] = len(items)
	}

	if !result.Success {
		output["error"] = result.Error
	}

	return &executor.StepResult{
		Output: output,
	}, nil
}

// parseListOutput parses the output of dbt list command
func parseListOutput(output string) []string {
	var items []string

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "Compiling") && !strings.Contains(line, "Found ") {
			items = append(items, line)
		}
	}

	return items
}

// ============================================================================
// SCHEMAS
// ============================================================================

// DbtRunSchema is the UI schema for dbt-run
var DbtRunSchema = resolver.NewSchemaBuilder("dbt-run").
	WithName("dbt Run").
	WithCategory("action").
	WithIcon(iconDbt).
	WithDescription("Execute dbt models to build tables/views in your data warehouse").
	AddSection("dbt Connection").
		AddExpressionField("projectDir", "Project Directory",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/dbt/project"),
			resolver.WithHint("Path to the dbt project directory containing dbt_project.yml"),
		).
		AddExpressionField("profilesDir", "Profiles Directory",
			resolver.WithPlaceholder("~/.dbt"),
			resolver.WithHint("Path to directory containing profiles.yml (default: ~/.dbt)"),
		).
		AddExpressionField("profile", "Profile Name",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("dbt profile name to use"),
		).
		AddExpressionField("target", "Target",
			resolver.WithPlaceholder("dev"),
			resolver.WithHint("Target environment (e.g., dev, prod, staging)"),
		).
		EndSection().
	AddSection("Selection").
		AddExpressionField("select", "Select",
			resolver.WithPlaceholder("my_model, tag:nightly, +model_name"),
			resolver.WithHint("Model selection syntax. Use + prefix for parents, + suffix for children"),
		).
		AddExpressionField("exclude", "Exclude",
			resolver.WithPlaceholder("deprecated_model, tag:skip"),
			resolver.WithHint("Models to exclude from the operation"),
		).
		EndSection().
	AddSection("Execution Options").
		AddToggleField("fullRefresh", "Full Refresh",
			resolver.WithDefault(false),
			resolver.WithHint("Drop and recreate models (use with caution)"),
		).
		AddToggleField("failFast", "Fail Fast",
			resolver.WithDefault(false),
			resolver.WithHint("Stop execution on first failure"),
		).
		AddToggleField("warnError", "Warn as Error",
			resolver.WithDefault(false),
			resolver.WithHint("Treat warnings as errors"),
		).
		AddNumberField("threads", "Threads",
			resolver.WithDefault(0),
			resolver.WithHint("Number of threads (0 = use profile default)"),
		).
		AddToggleField("singleThreaded", "Single Threaded",
			resolver.WithDefault(false),
			resolver.WithHint("Run in single-threaded mode"),
		).
		EndSection().
	Build()

// DbtTestSchema is the UI schema for dbt-test
var DbtTestSchema = resolver.NewSchemaBuilder("dbt-test").
	WithName("dbt Test").
	WithCategory("action").
	WithIcon(iconDbt).
	WithDescription("Run dbt tests to validate data quality and constraints").
	AddSection("dbt Connection").
		AddExpressionField("projectDir", "Project Directory",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/dbt/project"),
			resolver.WithHint("Path to the dbt project directory containing dbt_project.yml"),
		).
		AddExpressionField("profilesDir", "Profiles Directory",
			resolver.WithPlaceholder("~/.dbt"),
			resolver.WithHint("Path to directory containing profiles.yml (default: ~/.dbt)"),
		).
		AddExpressionField("profile", "Profile Name",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("dbt profile name to use"),
		).
		AddExpressionField("target", "Target",
			resolver.WithPlaceholder("dev"),
			resolver.WithHint("Target environment (e.g., dev, prod, staging)"),
		).
		EndSection().
	AddSection("Selection").
		AddExpressionField("select", "Select",
			resolver.WithPlaceholder("my_model, tag:nightly"),
			resolver.WithHint("Model selection syntax"),
		).
		AddExpressionField("exclude", "Exclude",
			resolver.WithPlaceholder("deprecated_model"),
			resolver.WithHint("Models to exclude from the operation"),
		).
		EndSection().
	AddSection("Test Options").
		AddSelectField("severity", "Minimum Severity",
			[]resolver.SelectOption{
				{Label: "Warn", Value: "warn"},
				{Label: "Error", Value: "error"},
			},
			resolver.WithDefault("warn"),
			resolver.WithHint("Minimum severity level to report"),
		).
		AddToggleField("storeFailures", "Store Failures",
			resolver.WithDefault(false),
			resolver.WithHint("Store test failures as tables for analysis"),
		).
		AddSelectField("indirectSelection", "Indirect Selection",
			[]resolver.SelectOption{
				{Label: "Eager", Value: "eager"},
				{Label: "Cautious", Value: "cautious"},
				{Label: "Buildable", Value: "buildable"},
			},
			resolver.WithDefault("eager"),
			resolver.WithHint("Mode for selecting tests indirectly related to selected models"),
		).
		EndSection().
	AddSection("Execution Options").
		AddToggleField("fullRefresh", "Full Refresh",
			resolver.WithDefault(false),
			resolver.WithHint("Drop and recreate models (use with caution)"),
		).
		AddToggleField("failFast", "Fail Fast",
			resolver.WithDefault(false),
			resolver.WithHint("Stop execution on first failure"),
		).
		AddToggleField("warnError", "Warn as Error",
			resolver.WithDefault(false),
			resolver.WithHint("Treat warnings as errors"),
		).
		AddNumberField("threads", "Threads",
			resolver.WithDefault(0),
			resolver.WithHint("Number of threads (0 = use profile default)"),
		).
		AddToggleField("singleThreaded", "Single Threaded",
			resolver.WithDefault(false),
			resolver.WithHint("Run in single-threaded mode"),
		).
		EndSection().
	Build()

// DbtCompileSchema is the UI schema for dbt-compile
var DbtCompileSchema = resolver.NewSchemaBuilder("dbt-compile").
	WithName("dbt Compile").
	WithCategory("action").
	WithIcon(iconDbt).
	WithDescription("Generate compiled SQL from dbt models without executing").
	AddSection("dbt Connection").
		AddExpressionField("projectDir", "Project Directory",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/dbt/project"),
			resolver.WithHint("Path to the dbt project directory containing dbt_project.yml"),
		).
		AddExpressionField("profilesDir", "Profiles Directory",
			resolver.WithPlaceholder("~/.dbt"),
			resolver.WithHint("Path to directory containing profiles.yml (default: ~/.dbt)"),
		).
		AddExpressionField("profile", "Profile Name",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("dbt profile name to use"),
		).
		AddExpressionField("target", "Target",
			resolver.WithPlaceholder("dev"),
			resolver.WithHint("Target environment (e.g., dev, prod, staging)"),
		).
		EndSection().
	AddSection("Selection").
		AddExpressionField("select", "Select",
			resolver.WithPlaceholder("my_model, tag:nightly"),
			resolver.WithHint("Model selection syntax"),
		).
		AddExpressionField("exclude", "Exclude",
			resolver.WithPlaceholder("deprecated_model"),
			resolver.WithHint("Models to exclude from the operation"),
		).
		EndSection().
	AddSection("Compile Options").
		AddToggleField("noCompile", "Skip Compile",
			resolver.WithDefault(false),
			resolver.WithHint("Skip the compilation step"),
		).
		EndSection().
	Build()

// DbtDocsGenerateSchema is the UI schema for dbt-docs-generate
var DbtDocsGenerateSchema = resolver.NewSchemaBuilder("dbt-docs-generate").
	WithName("dbt Generate Docs").
	WithCategory("action").
	WithIcon(iconDbt).
	WithDescription("Generate documentation for your dbt project").
	AddSection("dbt Connection").
		AddExpressionField("projectDir", "Project Directory",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/dbt/project"),
			resolver.WithHint("Path to the dbt project directory containing dbt_project.yml"),
		).
		AddExpressionField("profilesDir", "Profiles Directory",
			resolver.WithPlaceholder("~/.dbt"),
			resolver.WithHint("Path to directory containing profiles.yml (default: ~/.dbt)"),
		).
		AddExpressionField("profile", "Profile Name",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("dbt profile name to use"),
		).
		AddExpressionField("target", "Target",
			resolver.WithPlaceholder("dev"),
			resolver.WithHint("Target environment (e.g., dev, prod, staging)"),
		).
		EndSection().
	Build()

// DbtSeedSchema is the UI schema for dbt-seed
var DbtSeedSchema = resolver.NewSchemaBuilder("dbt-seed").
	WithName("dbt Seed").
	WithCategory("action").
	WithIcon(iconDbt).
	WithDescription("Load seed data from CSV files into your data warehouse").
	AddSection("dbt Connection").
		AddExpressionField("projectDir", "Project Directory",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/dbt/project"),
			resolver.WithHint("Path to the dbt project directory containing dbt_project.yml"),
		).
		AddExpressionField("profilesDir", "Profiles Directory",
			resolver.WithPlaceholder("~/.dbt"),
			resolver.WithHint("Path to directory containing profiles.yml (default: ~/.dbt)"),
		).
		AddExpressionField("profile", "Profile Name",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("dbt profile name to use"),
		).
		AddExpressionField("target", "Target",
			resolver.WithPlaceholder("dev"),
			resolver.WithHint("Target environment (e.g., dev, prod, staging)"),
		).
		EndSection().
	AddSection("Selection").
		AddExpressionField("select", "Select",
			resolver.WithPlaceholder("my_seed, tag:initial"),
			resolver.WithHint("Seed selection syntax"),
		).
		AddExpressionField("exclude", "Exclude",
			resolver.WithPlaceholder("deprecated_seed"),
			resolver.WithHint("Seeds to exclude from the operation"),
		).
		EndSection().
	AddSection("Seed Options").
		AddToggleField("fullRefresh", "Full Refresh",
			resolver.WithDefault(false),
			resolver.WithHint("Truncate and reload all seed data"),
		).
		AddToggleField("show", "Show Data",
			resolver.WithDefault(false),
			resolver.WithHint("Show a sample of the loaded data"),
		).
		EndSection().
	AddSection("Execution Options").
		AddToggleField("failFast", "Fail Fast",
			resolver.WithDefault(false),
			resolver.WithHint("Stop execution on first failure"),
		).
		AddToggleField("warnError", "Warn as Error",
			resolver.WithDefault(false),
			resolver.WithHint("Treat warnings as errors"),
		).
		AddNumberField("threads", "Threads",
			resolver.WithDefault(0),
			resolver.WithHint("Number of threads (0 = use profile default)"),
		).
		AddToggleField("singleThreaded", "Single Threaded",
			resolver.WithDefault(false),
			resolver.WithHint("Run in single-threaded mode"),
		).
		EndSection().
	Build()

// DbtSnapshotSchema is the UI schema for dbt-snapshot
var DbtSnapshotSchema = resolver.NewSchemaBuilder("dbt-snapshot").
	WithName("dbt Snapshot").
	WithCategory("action").
	WithIcon(iconDbt).
	WithDescription("Execute dbt snapshots to track historical changes in source data").
	AddSection("dbt Connection").
		AddExpressionField("projectDir", "Project Directory",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/dbt/project"),
			resolver.WithHint("Path to the dbt project directory containing dbt_project.yml"),
		).
		AddExpressionField("profilesDir", "Profiles Directory",
			resolver.WithPlaceholder("~/.dbt"),
			resolver.WithHint("Path to directory containing profiles.yml (default: ~/.dbt)"),
		).
		AddExpressionField("profile", "Profile Name",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("dbt profile name to use"),
		).
		AddExpressionField("target", "Target",
			resolver.WithPlaceholder("dev"),
			resolver.WithHint("Target environment (e.g., dev, prod, staging)"),
		).
		EndSection().
	AddSection("Selection").
		AddExpressionField("select", "Select Snapshots",
			resolver.WithPlaceholder("my_snapshot, tag:hourly"),
			resolver.WithHint("Snapshot selection syntax"),
		).
		AddExpressionField("exclude", "Exclude",
			resolver.WithPlaceholder("deprecated_snapshot"),
			resolver.WithHint("Snapshots to exclude from the operation"),
		).
		EndSection().
	AddSection("Execution Options").
		AddToggleField("fullRefresh", "Full Refresh",
			resolver.WithDefault(false),
			resolver.WithHint("Drop and recreate snapshots (use with caution)"),
		).
		AddToggleField("failFast", "Fail Fast",
			resolver.WithDefault(false),
			resolver.WithHint("Stop execution on first failure"),
		).
		AddToggleField("warnError", "Warn as Error",
			resolver.WithDefault(false),
			resolver.WithHint("Treat warnings as errors"),
		).
		AddNumberField("threads", "Threads",
			resolver.WithDefault(0),
			resolver.WithHint("Number of threads (0 = use profile default)"),
		).
		AddToggleField("singleThreaded", "Single Threaded",
			resolver.WithDefault(false),
			resolver.WithHint("Run in single-threaded mode"),
		).
		EndSection().
	Build()

// DbtDebugSchema is the UI schema for dbt-debug
var DbtDebugSchema = resolver.NewSchemaBuilder("dbt-debug").
	WithName("dbt Debug").
	WithCategory("diagnostic").
	WithIcon(iconDbt).
	WithDescription("Test the dbt connection and configuration").
	AddSection("dbt Connection").
		AddExpressionField("projectDir", "Project Directory",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/dbt/project"),
			resolver.WithHint("Path to the dbt project directory containing dbt_project.yml"),
		).
		AddExpressionField("profilesDir", "Profiles Directory",
			resolver.WithPlaceholder("~/.dbt"),
			resolver.WithHint("Path to directory containing profiles.yml (default: ~/.dbt)"),
		).
		AddExpressionField("profile", "Profile Name",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("dbt profile name to use"),
		).
		AddExpressionField("target", "Target",
			resolver.WithPlaceholder("dev"),
			resolver.WithHint("Target environment (e.g., dev, prod, staging)"),
		).
		EndSection().
	AddSection("Debug Options").
		AddToggleField("configDir", "Show Config Dir",
			resolver.WithDefault(false),
			resolver.WithHint("Print the directory containing dbt.yml files"),
		).
		EndSection().
	Build()

// DbtListSchema is the UI schema for dbt-list
var DbtListSchema = resolver.NewSchemaBuilder("dbt-list").
	WithName("dbt List").
	WithCategory("action").
	WithIcon(iconDbt).
	WithDescription("List dbt resources (models, tests, seeds, snapshots, sources)").
	AddSection("dbt Connection").
		AddExpressionField("projectDir", "Project Directory",
			resolver.WithRequired(),
			resolver.WithPlaceholder("/path/to/dbt/project"),
			resolver.WithHint("Path to the dbt project directory containing dbt_project.yml"),
		).
		AddExpressionField("profilesDir", "Profiles Directory",
			resolver.WithPlaceholder("~/.dbt"),
			resolver.WithHint("Path to directory containing profiles.yml (default: ~/.dbt)"),
		).
		AddExpressionField("profile", "Profile Name",
			resolver.WithPlaceholder("default"),
			resolver.WithHint("dbt profile name to use"),
		).
		AddExpressionField("target", "Target",
			resolver.WithPlaceholder("dev"),
			resolver.WithHint("Target environment (e.g., dev, prod, staging)"),
		).
		EndSection().
	AddSection("Selection").
		AddExpressionField("select", "Select",
			resolver.WithPlaceholder("my_model, tag:nightly, +model_name"),
			resolver.WithHint("Resource selection syntax"),
		).
		AddExpressionField("exclude", "Exclude",
			resolver.WithPlaceholder("deprecated_model"),
			resolver.WithHint("Resources to exclude from the listing"),
		).
		EndSection().
	AddSection("List Options").
		AddSelectField("outputType", "Output Type",
			[]resolver.SelectOption{
				{Label: "Name", Value: "name"},
				{Label: "JSON", Value: "json"},
				{Label: "Text", Value: "text"},
			},
			resolver.WithDefault("name"),
			resolver.WithHint("Output format for listed resources"),
		).
		AddExpressionField("outputKeys", "Output Keys",
			resolver.WithPlaceholder("name,resource_type"),
			resolver.WithHint("Comma-separated list of keys to output (for JSON format)"),
		).
		AddSelectField("resourceType", "Resource Type",
			[]resolver.SelectOption{
				{Label: "Model", Value: "model"},
				{Label: "Test", Value: "test"},
				{Label: "Seed", Value: "seed"},
				{Label: "Snapshot", Value: "snapshot"},
				{Label: "Source", Value: "source"},
				{Label: "Analysis", Value: "analysis"},
			},
			resolver.WithHint("Filter by resource type"),
		).
		EndSection().
	Build()

// ============================================================================
// MAIN FUNCTION
// ============================================================================

func main() {
	// Get port from env or use default
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50103"
	}

	// Create skill server
	server := grpc.NewSkillServer("skill-dbt", "1.0.0")

	// Register all dbt executors with schemas
	server.RegisterExecutorWithSchema("dbt-run", NewDbtRunExecutor(), DbtRunSchema)
	server.RegisterExecutorWithSchema("dbt-test", NewDbtTestExecutor(), DbtTestSchema)
	server.RegisterExecutorWithSchema("dbt-compile", NewDbtCompileExecutor(), DbtCompileSchema)
	server.RegisterExecutorWithSchema("dbt-docs-generate", NewDbtDocsGenerateExecutor(), DbtDocsGenerateSchema)
	server.RegisterExecutorWithSchema("dbt-seed", NewDbtSeedExecutor(), DbtSeedSchema)
	server.RegisterExecutorWithSchema("dbt-snapshot", NewDbtSnapshotExecutor(), DbtSnapshotSchema)
	server.RegisterExecutorWithSchema("dbt-debug", NewDbtDebugExecutor(), DbtDebugSchema)
	server.RegisterExecutorWithSchema("dbt-list", NewDbtListExecutor(), DbtListSchema)

	fmt.Printf("Starting skill-dbt gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}
