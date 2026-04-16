// Package k8sclient provides a client for K8s operations via Atlas proxy.
// Skills use this to interact with Kubernetes resources through Cortex's kubelink.
package k8sclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Client provides K8s operations via Atlas proxy
type Client struct {
	atlasURL   string
	httpClient *http.Client
}

// NewClient creates a new K8s client.
// If atlasURL is empty, it uses ATLAS_URL env var or defaults to localhost.
func NewClient(atlasURL string) *Client {
	if atlasURL == "" {
		atlasURL = os.Getenv("ATLAS_URL")
		if atlasURL == "" {
			atlasURL = "http://localhost:8081"
		}
	}
	return &Client{
		atlasURL: atlasURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ResourceIdentifier identifies a K8s resource
type ResourceIdentifier struct {
	Name             string           `json:"name,omitempty"`
	Namespace        string           `json:"namespace,omitempty"`
	GroupVersionKind GroupVersionKind `json:"groupVersionKind"`
}

// GroupVersionKind represents a K8s GVK
type GroupVersionKind struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// ResourceRequest is the request format for K8s API calls
type ResourceRequest struct {
	ClusterId        int                `json:"clusterId,omitempty"`
	ResourceIdentifier ResourceIdentifier `json:"resourceIdentifier,omitempty"`
}

// GetResource fetches a K8s resource
func (c *Client) GetResource(ctx context.Context, clusterId int, namespace, name, kind string) (map[string]interface{}, error) {
	gvk, err := parseKind(kind)
	if err != nil {
		return nil, err
	}

	req := ResourceRequest{
		ClusterId: clusterId,
		ResourceIdentifier: ResourceIdentifier{
			Name:             name,
			Namespace:        namespace,
			GroupVersionKind: gvk,
		},
	}

	var result struct {
		ManifestResponse struct {
			Manifest map[string]interface{} `json:"manifest"`
		} `json:"manifestResponse"`
	}

	if err := c.doRequest(ctx, "/resource", req, &result); err != nil {
		return nil, err
	}

	return result.ManifestResponse.Manifest, nil
}

// ListResources lists K8s resources
func (c *Client) ListResources(ctx context.Context, clusterId int, namespace, kind string) ([]map[string]interface{}, error) {
	gvk, err := parseKind(kind)
	if err != nil {
		return nil, err
	}

	req := ResourceRequest{
		ClusterId: clusterId,
		ResourceIdentifier: ResourceIdentifier{
			Namespace:        namespace,
			GroupVersionKind: gvk,
		},
	}

	var result struct {
		Resources struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"resources"`
	}

	if err := c.doRequest(ctx, "/resource/list", req, &result); err != nil {
		return nil, err
	}

	return result.Resources.Items, nil
}

// DeleteResource deletes a K8s resource
func (c *Client) DeleteResource(ctx context.Context, clusterId int, namespace, name, kind string) error {
	gvk, err := parseKind(kind)
	if err != nil {
		return err
	}

	req := ResourceRequest{
		ClusterId: clusterId,
		ResourceIdentifier: ResourceIdentifier{
			Name:             name,
			Namespace:        namespace,
			GroupVersionKind: gvk,
		},
	}

	return c.doRequest(ctx, "/resource/delete", req, nil)
}

// UpdateResource updates a K8s resource
func (c *Client) UpdateResource(ctx context.Context, clusterId int, namespace, name, kind string, patch map[string]interface{}) (map[string]interface{}, error) {
	gvk, err := parseKind(kind)
	if err != nil {
		return nil, err
	}

	req := map[string]interface{}{
		"clusterId": clusterId,
		"k8sRequest": map[string]interface{}{
			"resourceIdentifier": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
				"groupVersionKind": map[string]string{
					"group":   gvk.Group,
					"version": gvk.Version,
					"kind":    gvk.Kind,
				},
			},
			"patch": mustMarshal(patch),
		},
	}

	var result struct {
		ManifestResponse struct {
			Manifest map[string]interface{} `json:"manifest"`
		} `json:"manifestResponse"`
	}

	if err := c.doRequest(ctx, "/resource/update", req, &result); err != nil {
		return nil, err
	}

	return result.ManifestResponse.Manifest, nil
}

// LogsRequest is the request for getting pod logs
type LogsRequest struct {
	ClusterId     int    `json:"clusterId"`
	Namespace     string `json:"namespace"`
	PodName       string `json:"podName"`
	ContainerName string `json:"containerName,omitempty"`
	TailLines     *int64 `json:"tailLines,omitempty"`
	SinceSeconds  *int64 `json:"sinceSeconds,omitempty"`
}

// GetPodLogs fetches pod logs
func (c *Client) GetPodLogs(ctx context.Context, clusterId int, namespace, podName, containerName string, tailLines int) (string, error) {
	req := LogsRequest{
		ClusterId:     clusterId,
		Namespace:     namespace,
		PodName:       podName,
		ContainerName: containerName,
	}

	if tailLines > 0 {
		tl := int64(tailLines)
		req.TailLines = &tl
	}

	return c.doRequestRaw(ctx, "/logs", req)
}

// EventsRequest is the request for listing events
type EventsRequest struct {
	ClusterId        int                `json:"clusterId"`
	ResourceIdentifier ResourceIdentifier `json:"resourceIdentifier"`
}

// ListEvents lists K8s events
func (c *Client) ListEvents(ctx context.Context, clusterId int, namespace, resourceKind, resourceName string) ([]map[string]interface{}, error) {
	gvk, err := parseKind(resourceKind)
	if err != nil {
		return nil, err
	}

	req := EventsRequest{
		ClusterId: clusterId,
		ResourceIdentifier: ResourceIdentifier{
			Name:             resourceName,
			Namespace:        namespace,
			GroupVersionKind: gvk,
		},
	}

	var result struct {
		Events struct {
			Items []map[string]interface{} `json:"items"`
		} `json:"events"`
	}

	if err := c.doRequest(ctx, "/events", req, &result); err != nil {
		return nil, err
	}

	return result.Events.Items, nil
}

// RestartRequest is the request for restarting workloads
type RestartRequest struct {
	ClusterId int                       `json:"clusterId"`
	Resources  []map[string]interface{} `json:"resources"`
}

// RestartResource restarts a deployment, statefulset, or daemonset
func (c *Client) RestartResource(ctx context.Context, clusterId int, namespace, name, kind string) error {
	gvk, err := parseKind(kind)
	if err != nil {
		return err
	}

	req := RestartRequest{
		ClusterId: clusterId,
		Resources: []map[string]interface{}{
			{
				"name":      name,
				"namespace": namespace,
				"groupVersionKind": map[string]string{
					"group":   gvk.Group,
					"version": gvk.Version,
					"kind":    gvk.Kind,
				},
			},
		},
	}

	return c.doRequest(ctx, "/restart", req, nil)
}

// ScaleResource scales a deployment or statefulset
func (c *Client) ScaleResource(ctx context.Context, clusterId int, namespace, name, kind string, replicas int) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": replicas,
		},
	}
	_, err := c.UpdateResource(ctx, clusterId, namespace, name, kind, patch)
	return err
}

// doRequest makes a request to the K8s proxy endpoint
func (c *Client) doRequest(ctx context.Context, path string, req interface{}, result interface{}) error {
	url := fmt.Sprintf("%s/internal/k8s%s", c.atlasURL, path)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("K8s API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// doRequestRaw makes a request and returns the raw response body
func (c *Client) doRequestRaw(ctx context.Context, path string, req interface{}) (string, error) {
	url := fmt.Sprintf("%s/internal/k8s%s", c.atlasURL, path)

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("K8s API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(respBody), nil
}

// mustMarshal marshals to JSON string, panicking on error
func mustMarshal(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// parseKind converts a simple kind string to GroupVersionKind
func parseKind(kind string) (GroupVersionKind, error) {
	// Map of common kinds to their GVK
	kindMap := map[string]GroupVersionKind{
		"pod":                     {Group: "", Version: "v1", Kind: "Pod"},
		"pods":                    {Group: "", Version: "v1", Kind: "Pod"},
		"service":                 {Group: "", Version: "v1", Kind: "Service"},
		"services":                {Group: "", Version: "v1", Kind: "Service"},
		"configmap":               {Group: "", Version: "v1", Kind: "ConfigMap"},
		"configmaps":              {Group: "", Version: "v1", Kind: "ConfigMap"},
		"secret":                  {Group: "", Version: "v1", Kind: "Secret"},
		"secrets":                 {Group: "", Version: "v1", Kind: "Secret"},
		"persistentvolumeclaim":   {Group: "", Version: "v1", Kind: "PersistentVolumeClaim"},
		"persistentvolumeclaims":  {Group: "", Version: "v1", Kind: "PersistentVolumeClaim"},
		"pvc":                     {Group: "", Version: "v1", Kind: "PersistentVolumeClaim"},
		"persistentvolume":        {Group: "", Version: "v1", Kind: "PersistentVolume"},
		"persistentvolumes":       {Group: "", Version: "v1", Kind: "PersistentVolume"},
		"pv":                      {Group: "", Version: "v1", Kind: "PersistentVolume"},
		"node":                    {Group: "", Version: "v1", Kind: "Node"},
		"nodes":                   {Group: "", Version: "v1", Kind: "Node"},
		"namespace":               {Group: "", Version: "v1", Kind: "Namespace"},
		"namespaces":              {Group: "", Version: "v1", Kind: "Namespace"},
		"serviceaccount":          {Group: "", Version: "v1", Kind: "ServiceAccount"},
		"serviceaccounts":         {Group: "", Version: "v1", Kind: "ServiceAccount"},
		"deployment":              {Group: "apps", Version: "v1", Kind: "Deployment"},
		"deployments":             {Group: "apps", Version: "v1", Kind: "Deployment"},
		"statefulset":             {Group: "apps", Version: "v1", Kind: "StatefulSet"},
		"statefulsets":            {Group: "apps", Version: "v1", Kind: "StatefulSet"},
		"daemonset":               {Group: "apps", Version: "v1", Kind: "DaemonSet"},
		"daemonsets":              {Group: "apps", Version: "v1", Kind: "DaemonSet"},
		"replicaset":              {Group: "apps", Version: "v1", Kind: "ReplicaSet"},
		"replicasets":             {Group: "apps", Version: "v1", Kind: "ReplicaSet"},
		"job":                     {Group: "batch", Version: "v1", Kind: "Job"},
		"jobs":                    {Group: "batch", Version: "v1", Kind: "Job"},
		"cronjob":                 {Group: "batch", Version: "v1", Kind: "CronJob"},
		"cronjobs":                {Group: "batch", Version: "v1", Kind: "CronJob"},
		"ingress":                 {Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
		"ingresses":               {Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
		"networkpolicy":           {Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		"networkpolicies":         {Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
		"horizontalpodautoscaler": {Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"},
		"hpa":                     {Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"},
	}

	gvk, ok := kindMap[kind]
	if !ok {
		return GroupVersionKind{}, fmt.Errorf("unsupported kind: %s", kind)
	}
	return gvk, nil
}