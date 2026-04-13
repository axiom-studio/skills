package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/k8sclient"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconKubernetes = "kubernetes"
)

var k8sClient *k8sclient.Client

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50051"
	}

	// Initialize K8s client (uses ATLAS_URL env var)
	k8sClient = k8sclient.NewClient("")

	server := grpc.NewSkillServer("skill-k8s", "1.0.0")

	// Register trigger nodes
	server.RegisterExecutorWithSchema("k8s-watch", &K8sWatchExecutor{}, makeK8sWatchSchema())
	server.RegisterExecutorWithSchema("k8s-event", &K8sEventExecutor{}, makeK8sEventSchema())
	server.RegisterExecutorWithSchema("k8s-log-monitor", &K8sLogMonitorExecutor{}, makeK8sLogMonitorSchema())

	// Register action nodes
	server.RegisterExecutorWithSchema("k8s-get", &K8sGetExecutor{}, makeK8sGetSchema())
	server.RegisterExecutorWithSchema("k8s-list", &K8sListExecutor{}, makeK8sListSchema())
	server.RegisterExecutorWithSchema("k8s-logs", &K8sLogsExecutor{}, makeK8sLogsSchema())
	server.RegisterExecutorWithSchema("k8s-events", &K8sEventsExecutor{}, makeK8sEventsSchema())
	server.RegisterExecutorWithSchema("k8s-restart", &K8sRestartExecutor{}, makeK8sRestartSchema())
	server.RegisterExecutorWithSchema("k8s-scale", &K8sScaleExecutor{}, makeK8sScaleSchema())
	server.RegisterExecutorWithSchema("k8s-patch", &K8sPatchExecutor{}, makeK8sPatchSchema())
	server.RegisterExecutorWithSchema("k8s-delete", &K8sDeleteExecutor{}, makeK8sDeleteSchema())

	fmt.Printf("Starting skill-k8s gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// Common resource type options
func resourceTypeOptions() []resolver.SelectOption {
	return []resolver.SelectOption{
		{Label: "Pod", Value: "pod"},
		{Label: "Deployment", Value: "deployment"},
		{Label: "StatefulSet", Value: "statefulset"},
		{Label: "DaemonSet", Value: "daemonset"},
		{Label: "Service", Value: "service"},
		{Label: "ConfigMap", Value: "configmap"},
		{Label: "Secret", Value: "secret"},
		{Label: "Ingress", Value: "ingress"},
		{Label: "PersistentVolumeClaim", Value: "pvc"},
		{Label: "Namespace", Value: "namespace"},
		{Label: "Node", Value: "node"},
		{Label: "Job", Value: "job"},
		{Label: "CronJob", Value: "cronjob"},
	}
}

// Common workload type options (for restart/scale operations)
func workloadTypeOptions() []resolver.SelectOption {
	return []resolver.SelectOption{
		{Label: "Deployment", Value: "deployment"},
		{Label: "StatefulSet", Value: "statefulset"},
		{Label: "DaemonSet", Value: "daemonset"},
		{Label: "ReplicaSet", Value: "replicaset"},
		{Label: "Job", Value: "job"},
	}
}

// ============================================================================
// TRIGGER NODE SCHEMAS
// ============================================================================

func makeK8sWatchSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-watch",
		DisplayName: "Watch Resources",
		Category:    "trigger",
		Description: "Trigger workflow when Kubernetes resources change",
		Icon: iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Hint: "Namespace to watch (leave empty for all)", Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Required: true, Options: resourceTypeOptions()},
					{Key: "labelSelector", Type: resolver.FieldTypeText, Label: "Label Selector", Hint: "Filter by labels (e.g., app=myapp)", Placeholder: "app=myapp"},
				},
			},
			{
				Title: "Events",
				Fields: []*resolver.FieldSchema{
					{Key: "eventTypes", Type: resolver.FieldTypeMultiselect, Label: "Event Types", Default: []string{"ADDED", "MODIFIED", "DELETED"}, Options: []resolver.SelectOption{
						{Label: "Added", Value: "ADDED"},
						{Label: "Modified", Value: "MODIFIED"},
						{Label: "Deleted", Value: "DELETED"},
					}},
				},
			},
		},
	}
}

func makeK8sEventSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-event",
		DisplayName: "Kubernetes Events",
		Category:    "trigger",
		Description: "Trigger workflow when Kubernetes events occur",
		Icon:        iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Hint: "Namespace to watch for events", Placeholder: "default"},
				},
			},
			{
				Title: "Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "involvedObjectKind", Type: resolver.FieldTypeSelect, Label: "Resource Kind", Hint: "Filter by resource kind", Options: append([]resolver.SelectOption{{Label: "All", Value: ""}}, resourceTypeOptions()...)},
					{Key: "involvedObjectName", Type: resolver.FieldTypeText, Label: "Resource Name", Hint: "Filter by resource name", Placeholder: "my-app"},
					{Key: "eventReason", Type: resolver.FieldTypeText, Label: "Event Reason", Hint: "Filter by event reason (e.g., Pulling, Pulled, Failed)", Placeholder: "Failed"},
					{Key: "eventTypes", Type: resolver.FieldTypeMultiselect, Label: "Event Types", Options: []resolver.SelectOption{
						{Label: "Normal", Value: "Normal"},
						{Label: "Warning", Value: "Warning"},
					}},
				},
			},
		},
	}
}

func makeK8sLogMonitorSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-log-monitor",
		DisplayName: "Log Monitor",
		Category:    "trigger",
		Description: "Periodically monitor logs and trigger when patterns match",
		Icon: iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Required: true, Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Required: true, Options: []resolver.SelectOption{
						{Label: "Pod", Value: "pod"},
						{Label: "Deployment", Value: "deployment"},
						{Label: "StatefulSet", Value: "statefulset"},
						{Label: "DaemonSet", Value: "daemonset"},
					}},
					{Key: "resourceName", Type: resolver.FieldTypeText, Label: "Resource Name", Required: true, Hint: "Name or label selector", Placeholder: "my-app"},
					{Key: "container", Type: resolver.FieldTypeText, Label: "Container", Hint: "Container name (optional for single-container pods)"},
				},
			},
			{
				Title: "Schedule",
				Fields: []*resolver.FieldSchema{
					{Key: "interval", Type: resolver.FieldTypeText, Label: "Check Interval", Hint: "How often to check (e.g., 1m, 5m, 1h)", Default: "5m", Placeholder: "5m"},
					{Key: "lookback", Type: resolver.FieldTypeText, Label: "Lookback", Hint: "How far back to look in logs", Default: "5m", Placeholder: "5m"},
				},
			},
			{
				Title: "Include Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "includePatterns", Type: resolver.FieldTypeTextarea, Label: "Regex Patterns", Hint: "Regex patterns to match (one per line)", Rows: 3, Placeholder: "ERROR.*\nException\nFATAL"},
					{Key: "includeKeywords", Type: resolver.FieldTypeTags, Label: "Keywords", Hint: "Simple keywords to match (faster)"},
				},
			},
			{
				Title: "Exclude Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "excludePatterns", Type: resolver.FieldTypeTextarea, Label: "Regex Patterns", Hint: "Patterns to ignore (one per line)", Rows: 3, Placeholder: "DEBUG.*\nhealth check"},
					{Key: "excludeKeywords", Type: resolver.FieldTypeTags, Label: "Keywords", Hint: "Keywords to ignore"},
				},
			},
			{
				Title: "Trigger Conditions",
				Fields: []*resolver.FieldSchema{
					{Key: "minMatches", Type: resolver.FieldTypeNumber, Label: "Min Matches", Hint: "Minimum matches to trigger", Default: 1, Min: ptr(1), Max: ptr(1000)},
					{Key: "maxMatches", Type: resolver.FieldTypeNumber, Label: "Max Matches", Hint: "Max matches in output (0 = all)", Default: 100, Min: ptr(0), Max: ptr(10000)},
				},
			},
		},
	}
}

// ============================================================================
// ACTION NODE SCHEMAS
// ============================================================================

func makeK8sGetSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-get",
		DisplayName: "Get Resource",
		Category:    "action",
		Description: "Get a Kubernetes resource by name",
		Icon:        iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Required: true, Options: resourceTypeOptions()},
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name", Required: true, Placeholder: "my-resource"},
				},
			},
			{
				Title: "Output",
				Fields: []*resolver.FieldSchema{
					{Key: "outputFormat", Type: resolver.FieldTypeSelect, Label: "Format", Default: "json", Options: []resolver.SelectOption{
						{Label: "JSON", Value: "json"},
						{Label: "YAML", Value: "yaml"},
						{Label: "Wide", Value: "wide"},
					}},
				},
			},
		},
	}
}

func makeK8sListSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-list",
		DisplayName: "List Resources",
		Category:    "action",
		Description: "List Kubernetes resources with optional filtering",
		Icon:        iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Hint: "Leave empty for all namespaces", Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Required: true, Options: resourceTypeOptions()},
				},
			},
			{
				Title: "Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "labelSelector", Type: resolver.FieldTypeText, Label: "Label Selector", Hint: "Filter by labels (e.g., app=myapp,tier=frontend)", Placeholder: "app=myapp"},
					{Key: "fieldSelector", Type: resolver.FieldTypeText, Label: "Field Selector", Hint: "Filter by fields (e.g., status.phase=Running)", Placeholder: "status.phase=Running"},
				},
			},
			{
				Title: "Output",
				Fields: []*resolver.FieldSchema{
					{Key: "limit", Type: resolver.FieldTypeNumber, Label: "Limit", Hint: "Maximum number of results", Default: 100, Min: ptr(1), Max: ptr(1000)},
					{Key: "outputFormat", Type: resolver.FieldTypeSelect, Label: "Format", Default: "table", Options: []resolver.SelectOption{
						{Label: "Table", Value: "table"},
						{Label: "JSON", Value: "json"},
						{Label: "YAML", Value: "yaml"},
					}},
				},
			},
		},
	}
}

func makeK8sLogsSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-logs",
		DisplayName: "Get Logs",
		Category:    "action",
		Description: "Get logs from a pod or container",
		Icon: iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Required: true, Placeholder: "default"},
					{Key: "pod", Type: resolver.FieldTypeText, Label: "Pod Name", Required: true, Placeholder: "my-pod"},
					{Key: "container", Type: resolver.FieldTypeText, Label: "Container", Hint: "Container name (optional for single-container pods)"},
				},
			},
			{
				Title: "Options",
				Fields: []*resolver.FieldSchema{
					{Key: "tailLines", Type: resolver.FieldTypeNumber, Label: "Tail Lines", Hint: "Number of lines from end", Default: 100, Min: ptr(1), Max: ptr(10000)},
					{Key: "sinceSeconds", Type: resolver.FieldTypeNumber, Label: "Since (seconds)", Hint: "Logs from last N seconds", Min: ptr(1)},
					{Key: "timestamps", Type: resolver.FieldTypeToggle, Label: "Show Timestamps", Default: false},
					{Key: "previous", Type: resolver.FieldTypeToggle, Label: "Previous Container", Hint: "Get logs from previous container instance", Default: false},
				},
			},
			{
				Title: "Filter",
				Fields: []*resolver.FieldSchema{
					{Key: "grep", Type: resolver.FieldTypeText, Label: "Grep Pattern", Hint: "Filter logs by pattern", Placeholder: "ERROR"},
				},
			},
		},
	}
}

func makeK8sEventsSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-events",
		DisplayName: "Get Events",
		Category:    "action",
		Description: "Get Kubernetes events for a resource or namespace",
		Icon:        iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Hint: "Filter by resource type", Options: append([]resolver.SelectOption{{Label: "All", Value: ""}}, resourceTypeOptions()...)},
					{Key: "resourceName", Type: resolver.FieldTypeText, Label: "Resource Name", Hint: "Filter by resource name", Placeholder: "my-app"},
				},
			},
			{
				Title: "Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "types", Type: resolver.FieldTypeMultiselect, Label: "Event Types", Options: []resolver.SelectOption{
						{Label: "Normal", Value: "Normal"},
						{Label: "Warning", Value: "Warning"},
					}},
					{Key: "limit", Type: resolver.FieldTypeNumber, Label: "Limit", Default: 50, Min: ptr(1), Max: ptr(1000)},
				},
			},
		},
	}
}

func makeK8sRestartSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-restart",
		DisplayName: "Restart Workload",
		Category:    "action",
		Description: "Restart a deployment, statefulset, or daemonset",
		Icon:        iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Required: true, Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Required: true, Options: []resolver.SelectOption{
						{Label: "Deployment", Value: "deployment"},
						{Label: "StatefulSet", Value: "statefulset"},
						{Label: "DaemonSet", Value: "daemonset"},
					}},
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name", Required: true, Placeholder: "my-app"},
				},
			},
			{
				Title: "Options",
				Fields: []*resolver.FieldSchema{
					{Key: "waitForRollout", Type: resolver.FieldTypeToggle, Label: "Wait for Rollout", Hint: "Wait for rollout to complete", Default: true},
					{Key: "timeout", Type: resolver.FieldTypeNumber, Label: "Timeout (seconds)", Hint: "Rollout timeout", Default: 300, Min: ptr(1), Max: ptr(3600)},
				},
			},
		},
	}
}

func makeK8sScaleSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-scale",
		DisplayName: "Scale Workload",
		Category:    "action",
		Description: "Scale a deployment or statefulset to desired replicas",
		Icon: iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Required: true, Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Required: true, Options: []resolver.SelectOption{
						{Label: "Deployment", Value: "deployment"},
						{Label: "StatefulSet", Value: "statefulset"},
						{Label: "ReplicaSet", Value: "replicaset"},
					}},
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name", Required: true, Placeholder: "my-app"},
				},
			},
			{
				Title: "Replicas",
				Fields: []*resolver.FieldSchema{
					{Key: "replicas", Type: resolver.FieldTypeSlider, Label: "Replicas", Required: true, Default: 1, Min: ptr(0), Max: ptr(100), ShowValue: true},
				},
			},
			{
				Title: "Options",
				Fields: []*resolver.FieldSchema{
					{Key: "waitForStable", Type: resolver.FieldTypeToggle, Label: "Wait for Stable", Hint: "Wait for stable state", Default: true},
					{Key: "timeout", Type: resolver.FieldTypeNumber, Label: "Timeout (seconds)", Default: 300, Min: ptr(1), Max: ptr(3600)},
				},
			},
		},
	}
}

func makeK8sPatchSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-patch",
		DisplayName: "Patch Resource",
		Category:    "action",
		Description: "Apply a patch to a Kubernetes resource",
		Icon: iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Required: true, Options: resourceTypeOptions()},
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name", Required: true, Placeholder: "my-resource"},
				},
			},
			{
				Title: "Patch",
				Fields: []*resolver.FieldSchema{
					{Key: "patchType", Type: resolver.FieldTypeSelect, Label: "Patch Type", Required: true, Default: "strategic", Options: []resolver.SelectOption{
						{Label: "Strategic Merge", Value: "strategic"},
						{Label: "JSON Merge", Value: "merge"},
						{Label: "JSON Patch", Value: "json"},
					}},
					{Key: "patch", Type: resolver.FieldTypeJSON, Label: "Patch", Required: true, Hint: "JSON patch to apply", Height: 200},
				},
			},
		},
	}
}

func makeK8sDeleteSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "k8s-delete",
		DisplayName: "Delete Resource",
		Category:    "action",
		Description: "Delete a Kubernetes resource",
		Icon: iconKubernetes,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "cluster", Type: resolver.FieldTypeText, Label: "Cluster", Hint: "Kubernetes cluster name", Placeholder: "default"},
					{Key: "namespace", Type: resolver.FieldTypeText, Label: "Namespace", Placeholder: "default"},
					{Key: "resourceType", Type: resolver.FieldTypeSelect, Label: "Resource Type", Required: true, Options: resourceTypeOptions()},
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name", Required: true, Placeholder: "my-resource"},
				},
			},
			{
				Title: "Options",
				Fields: []*resolver.FieldSchema{
					{Key: "force", Type: resolver.FieldTypeToggle, Label: "Force Delete", Hint: "Force deletion without waiting for graceful termination", Default: false},
					{Key: "gracePeriod", Type: resolver.FieldTypeNumber, Label: "Grace Period (seconds)", Hint: "Graceful termination period", Default: 30, Min: ptr(0), Max: ptr(3600)},
					{Key: "waitForDeletion", Type: resolver.FieldTypeToggle, Label: "Wait for Deletion", Default: true},
					{Key: "timeout", Type: resolver.FieldTypeNumber, Label: "Timeout (seconds)", Default: 60, Min: ptr(1), Max: ptr(600)},
				},
			},
		},
	}
}

func ptr(v float64) *float64 {
	return &v
}

// ============================================================================
// EXECUTORS
// ============================================================================

// Helper to get cluster ID from config (defaults to 1)
func getClusterId(config map[string]interface{}) int {
	if v, ok := config["cluster"]; ok {
		switch c := v.(type) {
		case string:
			if c == "" || c == "default" {
				return 1
			}
			if id, err := strconv.Atoi(c); err == nil {
				return id
			}
		case float64:
			return int(c)
		case int:
			return c
		}
	}
	return 1
}

// Helper to get string from config
func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Helper to get int from config
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

type K8sWatchExecutor struct{}

func (e *K8sWatchExecutor) Type() string { return "k8s-watch" }

func (e *K8sWatchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	// k8s-watch is a trigger - it's handled by Sentinel's trigger manager
	// When executed, it means the trigger fired and we're returning the event data
	return &executor.StepResult{
		Output: map[string]interface{}{
			"message": "k8s-watch trigger fired",
			"config":  step.Config,
		},
	}, nil
}

type K8sEventExecutor struct{}

func (e *K8sEventExecutor) Type() string { return "k8s-event" }

func (e *K8sEventExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	// k8s-event is a trigger - handled by Sentinel's trigger manager
	return &executor.StepResult{
		Output: map[string]interface{}{
			"message": "k8s-event trigger fired",
			"config":  step.Config,
		},
	}, nil
}

type K8sLogMonitorExecutor struct{}

func (e *K8sLogMonitorExecutor) Type() string { return "k8s-log-monitor" }

func (e *K8sLogMonitorExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	// k8s-log-monitor is a trigger - handled by Sentinel's trigger manager
	return &executor.StepResult{
		Output: map[string]interface{}{
			"message": "k8s-log-monitor trigger fired",
			"config":  step.Config,
			"matches": []map[string]interface{}{},
		},
	}, nil
}

type K8sGetExecutor struct{}

func (e *K8sGetExecutor) Type() string { return "k8s-get" }

func (e *K8sGetExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	clusterId := getClusterId(step.Config)
	namespace := getString(step.Config, "namespace")
	name := getString(step.Config, "name")
	resourceType := getString(step.Config, "resourceType")

	if resourceType == "" {
		return nil, fmt.Errorf("resourceType is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	resource, err := k8sClient.GetResource(ctx, clusterId, namespace, name, resourceType)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"resource": resource,
			"name":     name,
			"kind":     resourceType,
		},
	}, nil
}

type K8sListExecutor struct{}

func (e *K8sListExecutor) Type() string { return "k8s-list" }

func (e *K8sListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	clusterId := getClusterId(step.Config)
	namespace := getString(step.Config, "namespace")
	resourceType := getString(step.Config, "resourceType")

	if resourceType == "" {
		return nil, fmt.Errorf("resourceType is required")
	}

	resources, err := k8sClient.ListResources(ctx, clusterId, namespace, resourceType)
	if err != nil {
		return nil, fmt.Errorf("failed to list resources: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"items":    resources,
			"count":    len(resources),
			"kind":     resourceType,
		},
	}, nil
}

type K8sLogsExecutor struct{}

func (e *K8sLogsExecutor) Type() string { return "k8s-logs" }

func (e *K8sLogsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	clusterId := getClusterId(step.Config)
	namespace := getString(step.Config, "namespace")
	pod := getString(step.Config, "pod")
	container := getString(step.Config, "container")
	tailLines := getInt(step.Config, "tailLines", 100)

	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if pod == "" {
		return nil, fmt.Errorf("pod is required")
	}

	logs, err := k8sClient.GetPodLogs(ctx, clusterId, namespace, pod, container, tailLines)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"logs":     logs,
			"pod":      pod,
			"container": container,
		},
	}, nil
}

type K8sEventsExecutor struct{}

func (e *K8sEventsExecutor) Type() string { return "k8s-events" }

func (e *K8sEventsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	clusterId := getClusterId(step.Config)
	namespace := getString(step.Config, "namespace")
	resourceType := getString(step.Config, "resourceType")
	resourceName := getString(step.Config, "resourceName")

	events, err := k8sClient.ListEvents(ctx, clusterId, namespace, resourceType, resourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"events": events,
			"count":  len(events),
		},
	}, nil
}

type K8sRestartExecutor struct{}

func (e *K8sRestartExecutor) Type() string { return "k8s-restart" }

func (e *K8sRestartExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	clusterId := getClusterId(step.Config)
	namespace := getString(step.Config, "namespace")
	name := getString(step.Config, "name")
	resourceType := getString(step.Config, "resourceType")

	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if resourceType == "" {
		return nil, fmt.Errorf("resourceType is required")
	}

	err := k8sClient.RestartResource(ctx, clusterId, namespace, name, resourceType)
	if err != nil {
		return nil, fmt.Errorf("failed to restart resource: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message":  "resource restarted successfully",
			"name":     name,
			"kind":     resourceType,
		},
	}, nil
}

type K8sScaleExecutor struct{}

func (e *K8sScaleExecutor) Type() string { return "k8s-scale" }

func (e *K8sScaleExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	clusterId := getClusterId(step.Config)
	namespace := getString(step.Config, "namespace")
	name := getString(step.Config, "name")
	resourceType := getString(step.Config, "resourceType")
	replicas := getInt(step.Config, "replicas", 1)

	if namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if resourceType == "" {
		return nil, fmt.Errorf("resourceType is required")
	}

	err := k8sClient.ScaleResource(ctx, clusterId, namespace, name, resourceType, replicas)
	if err != nil {
		return nil, fmt.Errorf("failed to scale resource: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message":  "resource scaled successfully",
			"name":     name,
			"kind":     resourceType,
			"replicas": replicas,
		},
	}, nil
}

type K8sPatchExecutor struct{}

func (e *K8sPatchExecutor) Type() string { return "k8s-patch" }

func (e *K8sPatchExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	clusterId := getClusterId(step.Config)
	namespace := getString(step.Config, "namespace")
	name := getString(step.Config, "name")
	resourceType := getString(step.Config, "resourceType")

	if resourceType == "" {
		return nil, fmt.Errorf("resourceType is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	// Get patch from config
	patch, ok := step.Config["patch"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("patch is required and must be an object")
	}

	resource, err := k8sClient.UpdateResource(ctx, clusterId, namespace, name, resourceType, patch)
	if err != nil {
		return nil, fmt.Errorf("failed to patch resource: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message":  "resource patched successfully",
			"resource": resource,
			"name":     name,
			"kind":     resourceType,
		},
	}, nil
}

type K8sDeleteExecutor struct{}

func (e *K8sDeleteExecutor) Type() string { return "k8s-delete" }

func (e *K8sDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	clusterId := getClusterId(step.Config)
	namespace := getString(step.Config, "namespace")
	name := getString(step.Config, "name")
	resourceType := getString(step.Config, "resourceType")

	if resourceType == "" {
		return nil, fmt.Errorf("resourceType is required")
	}
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	err := k8sClient.DeleteResource(ctx, clusterId, namespace, name, resourceType)
	if err != nil {
		return nil, fmt.Errorf("failed to delete resource: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"message": "resource deleted successfully",
			"name":    name,
			"kind":    resourceType,
		},
	}, nil
}