package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
)

// HelmSkill implements Helm operations using the official Helm SDK
type HelmSkill struct {
	grpc.SkillServer
	settings *cli.EnvSettings
}

// NewHelmSkill creates a new Helm skill instance
func NewHelmSkill() *HelmSkill {
	return &HelmSkill{
		settings: cli.New(),
	}
}

// getActionConfig creates a Helm action configuration with the specified namespace and kubeconfig
func (s *HelmSkill) getActionConfig(namespace, kubeconfig string) (*action.Configuration, error) {
	actionConfig := new(action.Configuration)

	// Set kubeconfig if provided
	if kubeconfig != "" {
		os.Setenv("KUBECONFIG", kubeconfig)
	}

	// Use specified namespace or default
	if namespace == "" {
		namespace = "default"
	}

	// Initialize the action configuration using settings RESTClientGetter
	err := actionConfig.Init(s.settings.RESTClientGetter(), namespace, os.Getenv("HELM_DRIVER"), func(format string, v ...interface{}) {
		fmt.Printf("Helm debug: "+format+"\n", v...)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Helm action config: %w", err)
	}

	return actionConfig, nil
}

// getReposFile returns the path to the repositories.yaml file
func (s *HelmSkill) getReposFile() string {
	return filepath.Join(s.settings.RepositoryConfig)
}

// loadRepoConfig loads the repository configuration
func (s *HelmSkill) loadRepoConfig() (*repo.File, error) {
	f, err := repo.LoadFile(s.getReposFile())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if f == nil {
		f = repo.NewFile()
	}
	return f, nil
}

// saveRepoConfig saves the repository configuration
func (s *HelmSkill) saveRepoConfig(f *repo.File) error {
	return f.WriteFile(s.getReposFile(), 0644)
}

// getGetters returns the getter providers
func (s *HelmSkill) getGetters() getter.Providers {
	return getter.All(s.settings)
}

// getConfigString extracts a string value from config
func getConfigString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case []byte:
			return string(val)
		case fmt.Stringer:
			return val.String()
		default:
			return fmt.Sprintf("%v", val)
		}
	}
	return ""
}

// getConfigBool extracts a bool value from config
func getConfigBool(config map[string]interface{}, key string) bool {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case bool:
			return val
		case string:
			return val == "true" || val == "1" || val == "yes"
		case []byte:
			s := string(val)
			return s == "true" || s == "1" || s == "yes"
		case int, int32, int64:
			return fmt.Sprintf("%v", val) == "1"
		default:
			return false
		}
	}
	return false
}

// getConfigInt extracts an int value from config
func getConfigInt(config map[string]interface{}, key string) int {
	if v, ok := config[key]; ok {
		switch val := v.(type) {
		case int:
			return val
		case int32:
			return int(val)
		case int64:
			return int(val)
		case float64:
			return int(val)
		case string:
			var i int
			fmt.Sscanf(val, "%d", &i)
			return i
		case []byte:
			var i int
			fmt.Sscanf(string(val), "%d", &i)
			return i
		default:
			return 0
		}
	}
	return 0
}

// ============================================================================
// HELM INSTALL EXECUTOR
// ============================================================================

// HelmInstallExecutor handles helm-install node type
type HelmInstallExecutor struct {
	skill *HelmSkill
}

func (e *HelmInstallExecutor) Type() string {
	return "helm-install"
}

func (e *HelmInstallExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	chartRef := getConfigString(config, "chart")
	releaseName := getConfigString(config, "releaseName")
	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	version := getConfigString(config, "version")
	timeout := getConfigString(config, "timeout")
	wait := getConfigBool(config, "wait")
	waitForJobs := getConfigBool(config, "waitForJobs")
	createNamespace := getConfigBool(config, "createNamespace")
	dryRun := getConfigBool(config, "dryRun")
	atomic := getConfigBool(config, "atomic")
	skipCRDs := getConfigBool(config, "skipCRDs")
	valuesYAML := getConfigString(config, "values")

	if chartRef == "" {
		return &executor.StepResult{Error: fmt.Errorf("chart reference is required")}, nil
	}
	if releaseName == "" {
		return &executor.StepResult{Error: fmt.Errorf("releaseName is required")}, nil
	}

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewInstall(actionConfig)
	client.ReleaseName = releaseName
	client.Namespace = namespace
	client.Version = version
	client.Timeout = parseDuration(timeout, 5*time.Minute)
	client.Wait = wait
	client.WaitForJobs = waitForJobs
	client.CreateNamespace = createNamespace
	client.DryRun = dryRun
	client.Atomic = atomic
	client.SkipCRDs = skipCRDs

	// Parse values
	vals, err := parseValues(valuesYAML)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to parse values: %w", err)}, nil
	}

	// Load the chart
	chartRequested, name, err := e.skill.getChart(chartRef, version)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to load chart: %w", err)}, nil
	}

	if client.ReleaseName == "" {
		client.ReleaseName = name
	}

	// Run the install
	rel, err := client.RunWithContext(ctx, chartRequested, vals)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("install failed: %w", err)}, nil
	}

	output := map[string]interface{}{
		"name":       rel.Name,
		"namespace":  rel.Namespace,
		"version":    fmt.Sprintf("%d", rel.Version),
		"status":     rel.Info.Status.String(),
		"chart":      rel.Chart.Metadata.Name,
		"appVersion": rel.Chart.Metadata.AppVersion,
	}

	if dryRun {
		output["dryRun"] = true
		output["manifest"] = rel.Manifest
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// HELM UPGRADE EXECUTOR
// ============================================================================

// HelmUpgradeExecutor handles helm-upgrade node type
type HelmUpgradeExecutor struct {
	skill *HelmSkill
}

func (e *HelmUpgradeExecutor) Type() string {
	return "helm-upgrade"
}

func (e *HelmUpgradeExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	chartRef := getConfigString(config, "chart")
	releaseName := getConfigString(config, "releaseName")
	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	version := getConfigString(config, "version")
	timeout := getConfigString(config, "timeout")
	wait := getConfigBool(config, "wait")
	waitForJobs := getConfigBool(config, "waitForJobs")
	dryRun := getConfigBool(config, "dryRun")
	atomic := getConfigBool(config, "atomic")
	reuseValues := getConfigBool(config, "reuseValues")
	resetValues := getConfigBool(config, "resetValues")
	force := getConfigBool(config, "force")
	valuesYAML := getConfigString(config, "values")

	if chartRef == "" {
		return &executor.StepResult{Error: fmt.Errorf("chart reference is required")}, nil
	}
	if releaseName == "" {
		return &executor.StepResult{Error: fmt.Errorf("releaseName is required")}, nil
	}

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewUpgrade(actionConfig)
	client.Namespace = namespace
	client.Version = version
	client.Timeout = parseDuration(timeout, 5*time.Minute)
	client.Wait = wait
	client.WaitForJobs = waitForJobs
	client.DryRun = dryRun
	client.Atomic = atomic
	client.ReuseValues = reuseValues
	client.ResetValues = resetValues
	client.Force = force
	client.MaxHistory = 10

	// Parse values
	vals, err := parseValues(valuesYAML)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to parse values: %w", err)}, nil
	}

	// Load the chart
	chartRequested, _, err := e.skill.getChart(chartRef, version)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to load chart: %w", err)}, nil
	}

	// Run the upgrade
	rel, err := client.RunWithContext(ctx, releaseName, chartRequested, vals)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("upgrade failed: %w", err)}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name":       rel.Name,
			"namespace":  rel.Namespace,
			"version":    fmt.Sprintf("%d", rel.Version),
			"status":     rel.Info.Status.String(),
			"chart":      rel.Chart.Metadata.Name,
			"appVersion": rel.Chart.Metadata.AppVersion,
		},
	}, nil
}

// ============================================================================
// HELM UNINSTALL EXECUTOR
// ============================================================================

// HelmUninstallExecutor handles helm-uninstall node type
type HelmUninstallExecutor struct {
	skill *HelmSkill
}

func (e *HelmUninstallExecutor) Type() string {
	return "helm-uninstall"
}

func (e *HelmUninstallExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	releaseName := getConfigString(config, "releaseName")
	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	keepHistory := getConfigBool(config, "keepHistory")
	timeout := getConfigString(config, "timeout")

	if releaseName == "" {
		return &executor.StepResult{Error: fmt.Errorf("releaseName is required")}, nil
	}

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewUninstall(actionConfig)
	client.KeepHistory = keepHistory
	client.Timeout = parseDuration(timeout, 5*time.Minute)

	// Run the uninstall
	result, err := client.Run(releaseName)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("uninstall failed: %w", err)}, nil
	}

	output := map[string]interface{}{
		"name":   releaseName,
		"status": "uninstalled",
	}

	if result != nil && result.Info != "" {
		output["info"] = result.Info
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// HELM ROLLBACK EXECUTOR
// ============================================================================

// HelmRollbackExecutor handles helm-rollback node type
type HelmRollbackExecutor struct {
	skill *HelmSkill
}

func (e *HelmRollbackExecutor) Type() string {
	return "helm-rollback"
}

func (e *HelmRollbackExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	releaseName := getConfigString(config, "releaseName")
	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	revision := getConfigInt(config, "revision")
	timeout := getConfigString(config, "timeout")
	wait := getConfigBool(config, "wait")
	waitForJobs := getConfigBool(config, "waitForJobs")
	force := getConfigBool(config, "force")
	cleanupOnFail := getConfigBool(config, "cleanupOnFail")

	if releaseName == "" {
		return &executor.StepResult{Error: fmt.Errorf("releaseName is required")}, nil
	}

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewRollback(actionConfig)
	client.Version = revision
	client.Timeout = parseDuration(timeout, 5*time.Minute)
	client.Wait = wait
	client.WaitForJobs = waitForJobs
	client.Force = force
	client.CleanupOnFail = cleanupOnFail

	// Run the rollback
	err = client.Run(releaseName)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("rollback failed: %w", err)}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name":     releaseName,
			"revision": revision,
			"status":   "rolled back",
		},
	}, nil
}

// ============================================================================
// HELM LIST EXECUTOR
// ============================================================================

// HelmListExecutor handles helm-list node type
type HelmListExecutor struct {
	skill *HelmSkill
}

func (e *HelmListExecutor) Type() string {
	return "helm-list"
}

func (e *HelmListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	all := getConfigBool(config, "all")
	deployed := getConfigBool(config, "deployed")
	failed := getConfigBool(config, "failed")
	pending := getConfigBool(config, "pending")
	uninstalled := getConfigBool(config, "uninstalled")
	uninstalling := getConfigBool(config, "uninstalling")
	filter := getConfigString(config, "filter")

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewList(actionConfig)

	// Set state filter
	if all {
		client.StateMask = action.ListAll
	} else {
		var mask action.ListStates
		if deployed {
			mask |= action.ListDeployed
		}
		if failed {
			mask |= action.ListFailed
		}
		if pending {
			mask |= action.ListPendingInstall | action.ListPendingUpgrade | action.ListPendingRollback
		}
		if uninstalled {
			mask |= action.ListUninstalled
		}
		if uninstalling {
			mask |= action.ListUninstalling
		}
		if mask == 0 {
			mask = action.ListDeployed
		}
		client.StateMask = mask
	}

	// Set filter
	if filter != "" {
		client.Filter = filter
	}

	// Run the list
	releases, err := client.Run()
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("list failed: %w", err)}, nil
	}

	// Format output
	var items []map[string]interface{}
	for _, rel := range releases {
		items = append(items, map[string]interface{}{
			"name":       rel.Name,
			"namespace":  rel.Namespace,
			"revision":   rel.Version,
			"status":     rel.Info.Status.String(),
			"chart":      fmt.Sprintf("%s-%s", rel.Chart.Metadata.Name, rel.Chart.Metadata.Version),
			"appVersion": rel.Chart.Metadata.AppVersion,
			"deployed":   rel.Info.LastDeployed.Format(time.RFC3339),
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"count":    len(releases),
			"releases": items,
		},
	}, nil
}

// ============================================================================
// HELM STATUS EXECUTOR
// ============================================================================

// HelmStatusExecutor handles helm-status node type
type HelmStatusExecutor struct {
	skill *HelmSkill
}

func (e *HelmStatusExecutor) Type() string {
	return "helm-status"
}

func (e *HelmStatusExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	releaseName := getConfigString(config, "releaseName")
	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	revision := getConfigInt(config, "revision")
	outputFormat := getConfigString(config, "output")

	if releaseName == "" {
		return &executor.StepResult{Error: fmt.Errorf("releaseName is required")}, nil
	}

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewStatus(actionConfig)
	client.Version = revision

	// Run the status
	rel, err := client.Run(releaseName)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("status failed: %w", err)}, nil
	}

	// Build output
	output := map[string]interface{}{
		"name":         rel.Name,
		"namespace":    rel.Namespace,
		"revision":     rel.Version,
		"status":       rel.Info.Status.String(),
		"chart":        rel.Chart.Metadata.Name,
		"chartVersion": rel.Chart.Metadata.Version,
		"appVersion":   rel.Chart.Metadata.AppVersion,
		"deployed":     rel.Info.LastDeployed.Format(time.RFC3339),
		"description":  rel.Info.Description,
	}

	if outputFormat == "json" || outputFormat == "" {
		output["release"] = rel
	}

	if outputFormat == "manifest" || outputFormat == "all" {
		output["manifest"] = rel.Manifest
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// HELM HISTORY EXECUTOR
// ============================================================================

// HelmHistoryExecutor handles helm-history node type
type HelmHistoryExecutor struct {
	skill *HelmSkill
}

func (e *HelmHistoryExecutor) Type() string {
	return "helm-history"
}

func (e *HelmHistoryExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	releaseName := getConfigString(config, "releaseName")
	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	max := getConfigInt(config, "max")

	if releaseName == "" {
		return &executor.StepResult{Error: fmt.Errorf("releaseName is required")}, nil
	}

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewHistory(actionConfig)
	if max > 0 {
		client.Max = max
	} else {
		client.Max = 10
	}

	// Run the history
	hist, err := client.Run(releaseName)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("history failed: %w", err)}, nil
	}

	// Sort by revision descending
	sort.Slice(hist, func(i, j int) bool {
		return hist[i].Version > hist[j].Version
	})

	// Format output
	var items []map[string]interface{}
	for _, rel := range hist {
		items = append(items, map[string]interface{}{
			"revision":    rel.Version,
			"status":      rel.Info.Status.String(),
			"chart":       fmt.Sprintf("%s-%s", rel.Chart.Metadata.Name, rel.Chart.Metadata.Version),
			"appVersion":  rel.Chart.Metadata.AppVersion,
			"deployed":    rel.Info.LastDeployed.Format(time.RFC3339),
			"description": rel.Info.Description,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"count":   len(hist),
			"history": items,
		},
	}, nil
}

// ============================================================================
// HELM REPO ADD EXECUTOR
// ============================================================================

// HelmRepoAddExecutor handles helm-repo-add node type
type HelmRepoAddExecutor struct {
	skill *HelmSkill
}

func (e *HelmRepoAddExecutor) Type() string {
	return "helm-repo-add"
}

func (e *HelmRepoAddExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	name := getConfigString(config, "name")
	url := getConfigString(config, "url")
	username := getConfigString(config, "username")
	password := getConfigString(config, "password")
	caFile := getConfigString(config, "caFile")
	certFile := getConfigString(config, "certFile")
	keyFile := getConfigString(config, "keyFile")
	insecureSkipTLSverify := getConfigBool(config, "insecureSkipTLSverify")
	forceUpdate := getConfigBool(config, "forceUpdate")

	if name == "" {
		return &executor.StepResult{Error: fmt.Errorf("repo name is required")}, nil
	}
	if url == "" {
		return &executor.StepResult{Error: fmt.Errorf("repo URL is required")}, nil
	}

	// Load existing repos
	f, err := e.skill.loadRepoConfig()
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to load repo config: %w", err)}, nil
	}

	// Check if repo already exists
	if f.Has(name) && !forceUpdate {
		return &executor.StepResult{
			Error: fmt.Errorf("repository name (%s) already exists, please use a different name or pass --force-update", name),
		}, nil
	}

	// Create repository entry
	entry := &repo.Entry{
		Name:                  name,
		URL:                   url,
		Username:              username,
		Password:              password,
		CertFile:              certFile,
		KeyFile:               keyFile,
		CAFile:                caFile,
		InsecureSkipTLSverify: insecureSkipTLSverify,
	}

	// Get the chart repository
	r, err := repo.NewChartRepository(entry, e.skill.getGetters())
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to create repository: %w", err)}, nil
	}

	// Download index file to verify
	if _, err := r.DownloadIndexFile(); err != nil {
		return &executor.StepResult{
			Error: fmt.Errorf("looks like %q is not a valid chart repository or cannot be reached: %w", url, err),
		}, nil
	}

	// Add or update the repository
	if f.Has(name) {
		f.Update(entry)
	} else {
		f.Add(entry)
	}

	// Save the configuration
	if err := e.skill.saveRepoConfig(f); err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to save repo config: %w", err)}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"name":   name,
			"url":    url,
			"status": "added",
		},
	}, nil
}

// ============================================================================
// HELM REPO LIST EXECUTOR
// ============================================================================

// HelmRepoListExecutor handles helm-repo-list node type
type HelmRepoListExecutor struct {
	skill *HelmSkill
}

func (e *HelmRepoListExecutor) Type() string {
	return "helm-repo-list"
}

func (e *HelmRepoListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	// Load repos
	f, err := e.skill.loadRepoConfig()
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to load repo config: %w", err)}, nil
	}

	// Format output
	var items []map[string]interface{}
	for _, r := range f.Repositories {
		items = append(items, map[string]interface{}{
			"name": r.Name,
			"url":  r.URL,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"count": len(f.Repositories),
			"repos": items,
		},
	}, nil
}

// ============================================================================
// HELM REPO UPDATE EXECUTOR
// ============================================================================

// HelmRepoUpdateExecutor handles helm-repo-update node type
type HelmRepoUpdateExecutor struct {
	skill *HelmSkill
}

func (e *HelmRepoUpdateExecutor) Type() string {
	return "helm-repo-update"
}

func (e *HelmRepoUpdateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)
	repoNames := getConfigString(config, "repos") // Comma-separated list of repos to update

	// Load repos
	f, err := e.skill.loadRepoConfig()
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to load repo config: %w", err)}, nil
	}

	if len(f.Repositories) == 0 {
		return &executor.StepResult{
			Output: map[string]interface{}{
				"status":  "no repositories configured",
				"updated": 0,
			},
		}, nil
	}

	// Parse specific repos to update
	var reposToUpdate []string
	if repoNames != "" {
		reposToUpdate = strings.Split(repoNames, ",")
	}

	var updated []string
	var failed []string

	for _, r := range f.Repositories {
		// Skip if specific repos requested and this one isn't in the list
		if len(reposToUpdate) > 0 {
			found := false
			for _, name := range reposToUpdate {
				if strings.TrimSpace(name) == r.Name {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Get the chart repository
		chartRepo, err := repo.NewChartRepository(r, e.skill.getGetters())
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", r.Name, err))
			continue
		}

		// Download index file
		if _, err := chartRepo.DownloadIndexFile(); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", r.Name, err))
			continue
		}

		updated = append(updated, r.Name)
	}

	// Save the updated configuration
	if err := e.skill.saveRepoConfig(f); err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to save repo config: %w", err)}, nil
	}

	output := map[string]interface{}{
		"updated": len(updated),
		"status":  "success",
	}

	if len(updated) > 0 {
		output["repositories"] = strings.Join(updated, ", ")
	}
	if len(failed) > 0 {
		output["failed"] = strings.Join(failed, "; ")
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// HELM SEARCH EXECUTOR
// ============================================================================

// HelmSearchExecutor handles helm-search node type
type HelmSearchExecutor struct {
	skill *HelmSkill
}

func (e *HelmSearchExecutor) Type() string {
	return "helm-search"
}

func (e *HelmSearchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	keyword := getConfigString(config, "keyword")
	repoName := getConfigString(config, "repo")
	version := getConfigString(config, "version")
	regex := getConfigBool(config, "regex")
	versions := getConfigBool(config, "versions")

	if keyword == "" {
		return &executor.StepResult{Error: fmt.Errorf("keyword is required")}, nil
	}

	// Load repos
	f, err := e.skill.loadRepoConfig()
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to load repo config: %w", err)}, nil
	}

	// Build search index
	var searchRepos []*repo.ChartRepository
	if repoName != "" {
		if f.Has(repoName) {
			for _, r := range f.Repositories {
				if r.Name == repoName {
					chartRepo, err := repo.NewChartRepository(r, e.skill.getGetters())
					if err != nil {
						return &executor.StepResult{Error: fmt.Errorf("failed to create repository: %w", err)}, nil
					}
					searchRepos = append(searchRepos, chartRepo)
					break
				}
			}
		} else {
			return &executor.StepResult{Error: fmt.Errorf("repository %q not found", repoName)}, nil
		}
	} else {
		for _, r := range f.Repositories {
			chartRepo, err := repo.NewChartRepository(r, e.skill.getGetters())
			if err != nil {
				continue
			}
			searchRepos = append(searchRepos, chartRepo)
		}
	}

	// Search through index files
	var results []map[string]interface{}
	for _, chartRepo := range searchRepos {
		idx := chartRepo.IndexFile
		if idx == nil {
			// Download index file
			idxPath, err := chartRepo.DownloadIndexFile()
			if err != nil {
				continue
			}
			idx, err = repo.LoadIndexFile(idxPath)
			if err != nil {
				continue
			}
		}

		// Search entries
		for name, entries := range idx.Entries {
			// Check if name matches keyword
			matched := false
			if regex {
				if matched, _ = regexp.MatchString(keyword, name); matched {
					matched = true
				}
			} else {
				matched = strings.Contains(name, keyword)
			}

			if !matched {
				continue
			}

			// Filter by version if specified
			for _, entry := range entries {
				if version != "" && entry.Version != version {
					continue
				}

				result := map[string]interface{}{
					"name":    fmt.Sprintf("%s/%s", chartRepo.Config.Name, name),
					"version": entry.Version,
					"app":     entry.AppVersion,
				}

				if versions {
					// Include all versions
					results = append(results, result)
				} else {
					// Only include latest version (first in list)
					if len(results) == 0 || results[len(results)-1]["name"] != result["name"] {
						results = append(results, result)
					}
				}
			}
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"count":   len(results),
			"results": results,
		},
	}, nil
}

// ============================================================================
// HELM TEMPLATE EXECUTOR
// ============================================================================

// HelmTemplateExecutor handles helm-template node type
type HelmTemplateExecutor struct {
	skill *HelmSkill
}

func (e *HelmTemplateExecutor) Type() string {
	return "helm-template"
}

func (e *HelmTemplateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	chartRef := getConfigString(config, "chart")
	releaseName := getConfigString(config, "releaseName")
	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	version := getConfigString(config, "version")
	valuesYAML := getConfigString(config, "values")
	showCRDs := getConfigBool(config, "showCRDs")
	skipCRDs := getConfigBool(config, "skipCRDs")

	if chartRef == "" {
		return &executor.StepResult{Error: fmt.Errorf("chart reference is required")}, nil
	}
	if releaseName == "" {
		releaseName = "release-name"
	}

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewInstall(actionConfig)
	client.ReleaseName = releaseName
	client.Namespace = namespace
	client.Version = version
	client.DryRun = true
	client.ClientOnly = true
	client.SkipCRDs = skipCRDs

	// Parse values
	vals, err := parseValues(valuesYAML)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to parse values: %w", err)}, nil
	}

	// Load the chart
	chartRequested, name, err := e.skill.getChart(chartRef, version)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to load chart: %w", err)}, nil
	}

	if client.ReleaseName == "" {
		client.ReleaseName = name
	}

	// Run the template (dry-run install)
	rel, err := client.RunWithContext(ctx, chartRequested, vals)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("template failed: %w", err)}, nil
	}

	output := map[string]interface{}{
		"manifest": rel.Manifest,
		"name":     rel.Name,
	}

	if showCRDs && len(chartRequested.CRDs()) > 0 {
		var crds []string
		for _, crd := range chartRequested.CRDs() {
			crds = append(crds, string(crd.Data))
		}
		output["crds"] = strings.Join(crds, "---\n")
	}

	return &executor.StepResult{Output: output}, nil
}

// ============================================================================
// HELM VALUES EXECUTOR
// ============================================================================

// HelmValuesExecutor handles helm-values node type
type HelmValuesExecutor struct {
	skill *HelmSkill
}

func (e *HelmValuesExecutor) Type() string {
	return "helm-values"
}

func (e *HelmValuesExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	releaseName := getConfigString(config, "releaseName")
	namespace := getConfigString(config, "namespace")
	kubeconfig := getConfigString(config, "kubeconfig")
	revision := getConfigInt(config, "revision")

	if releaseName == "" {
		return &executor.StepResult{Error: fmt.Errorf("releaseName is required")}, nil
	}

	actionConfig, err := e.skill.getActionConfig(namespace, kubeconfig)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("failed to initialize Helm: %w", err)}, nil
	}

	client := action.NewGetValues(actionConfig)
	client.Version = revision

	// Run get values
	vals, err := client.Run(releaseName)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("get values failed: %w", err)}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"values": vals,
			"name":   releaseName,
		},
	}, nil
}

// ============================================================================
// HELM PULL EXECUTOR
// ============================================================================

// HelmPullExecutor handles helm-pull node type
type HelmPullExecutor struct {
	skill *HelmSkill
}

func (e *HelmPullExecutor) Type() string {
	return "helm-pull"
}

func (e *HelmPullExecutor) Execute(ctx context.Context, step *executor.StepDefinition, res executor.TemplateResolver) (*executor.StepResult, error) {
	config := res.ResolveMap(step.Config)

	chartRef := getConfigString(config, "chart")
	version := getConfigString(config, "version")
	destination := getConfigString(config, "destination")
	untar := getConfigBool(config, "untar")
	untardir := getConfigString(config, "untardir")
	verify := getConfigBool(config, "verify")

	if chartRef == "" {
		return &executor.StepResult{Error: fmt.Errorf("chart reference is required")}, nil
	}

	// Create pull client
	client := action.NewPull()
	client.Version = version
	client.Settings = e.skill.settings

	if destination != "" {
		client.DestDir = destination
	}

	client.Untar = untar
	if untardir != "" {
		client.UntarDir = untardir
	}

	if verify {
		client.Verify = true
	}

	// Run the pull
	outputPath, err := client.Run(chartRef)
	if err != nil {
		return &executor.StepResult{Error: fmt.Errorf("pull failed: %w", err)}, nil
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"path":    outputPath,
			"chart":   chartRef,
			"version": version,
			"status":  "pulled",
		},
	}, nil
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

// getChart loads a chart from the given reference
func (s *HelmSkill) getChart(chartRef, version string) (*chart.Chart, string, error) {
	name, chartRequested, err := s.getChartWithReq(chartRef, version)
	if err != nil {
		return nil, "", err
	}
	return chartRequested, name, nil
}

// getChartWithReq loads a chart and returns the name
func (s *HelmSkill) getChartWithReq(chartRef, version string) (string, *chart.Chart, error) {
	// Check if it's a local chart
	if _, err := os.Stat(chartRef); err == nil {
		chartRequested, err := loader.Load(chartRef)
		if err != nil {
			return "", nil, fmt.Errorf("failed to load local chart: %w", err)
		}
		return chartRequested.Name(), chartRequested, nil
	}

	// It's a remote chart - need to download it
	name := chartRef
	if idx := strings.Index(chartRef, "/"); idx != -1 {
		name = chartRef[idx+1:]
	}

	// Create a temporary directory for the chart
	tempDir, err := os.MkdirTemp("", "helm-chart-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	client := action.NewPull()
	client.Version = version
	client.Settings = s.settings
	client.DestDir = tempDir
	client.Untar = true

	_, err = client.Run(chartRef)
	if err != nil {
		return "", nil, fmt.Errorf("failed to download chart: %w", err)
	}

	// Find the chart directory
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read temp dir: %w", err)
	}

	var chartPath string
	for _, entry := range entries {
		if entry.IsDir() {
			chartPath = filepath.Join(tempDir, entry.Name())
			break
		}
	}

	if chartPath == "" {
		return "", nil, fmt.Errorf("chart directory not found")
	}

	chartRequested, err := loader.Load(chartPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to load downloaded chart: %w", err)
	}

	return name, chartRequested, nil
}

// parseValues parses YAML/JSON values string into a map
func parseValues(valuesYAML string) (map[string]interface{}, error) {
	vals := make(map[string]interface{})

	if valuesYAML == "" {
		return vals, nil
	}

	// Try to parse as JSON first
	if strings.HasPrefix(valuesYAML, "{") || strings.HasPrefix(valuesYAML, "[") {
		err := json.Unmarshal([]byte(valuesYAML), &vals)
		if err == nil {
			return vals, nil
		}
	}

	// Try YAML parsing using json (for simple cases)
	err := json.Unmarshal([]byte(valuesYAML), &vals)
	if err != nil {
		// Last resort: treat as simple key=value pairs
		lines := strings.Split(valuesYAML, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				vals[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	return vals, nil
}

// parseDuration parses a duration string with a default fallback
func parseDuration(s string, defaultDur time.Duration) time.Duration {
	if s == "" {
		return defaultDur
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		// Try parsing as integer seconds
		var seconds int
		if _, err := fmt.Sscanf(s, "%d", &seconds); err == nil {
			return time.Duration(seconds) * time.Second
		}
		return defaultDur
	}
	return d
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50087"
	}

	server := grpc.NewSkillServer("skill-helm", "1.0.0")

	skill := NewHelmSkill()

	// Install operations
	server.RegisterExecutor("helm-install", &HelmInstallExecutor{skill: skill})
	server.RegisterExecutor("helm-upgrade", &HelmUpgradeExecutor{skill: skill})
	server.RegisterExecutor("helm-uninstall", &HelmUninstallExecutor{skill: skill})
	server.RegisterExecutor("helm-rollback", &HelmRollbackExecutor{skill: skill})

	// List and status operations
	server.RegisterExecutor("helm-list", &HelmListExecutor{skill: skill})
	server.RegisterExecutor("helm-status", &HelmStatusExecutor{skill: skill})
	server.RegisterExecutor("helm-history", &HelmHistoryExecutor{skill: skill})

	// Repository operations
	server.RegisterExecutor("helm-repo-add", &HelmRepoAddExecutor{skill: skill})
	server.RegisterExecutor("helm-repo-list", &HelmRepoListExecutor{skill: skill})
	server.RegisterExecutor("helm-repo-update", &HelmRepoUpdateExecutor{skill: skill})

	// Search and template operations
	server.RegisterExecutor("helm-search", &HelmSearchExecutor{skill: skill})
	server.RegisterExecutor("helm-template", &HelmTemplateExecutor{skill: skill})
	server.RegisterExecutor("helm-values", &HelmValuesExecutor{skill: skill})
	server.RegisterExecutor("helm-pull", &HelmPullExecutor{skill: skill})

	fmt.Printf("Starting skill-helm gRPC server on port %s\n", port)
	if err := server.Serve(":" + port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}
