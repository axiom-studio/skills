package main

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const (
	iconDocker = "box"
)

var dockerClient *client.Client

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50074"
	}

	// Initialize Docker client
	var err error
	dockerClient, err = createDockerClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Docker client: %v\n", err)
		os.Exit(1)
	}
	defer dockerClient.Close()

	// Test connection
	_, err = dockerClient.Info(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to Docker daemon: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully connected to Docker daemon")

	server := grpc.NewSkillServer("skill-docker", "1.0.0")

	// Register container nodes
	server.RegisterExecutorWithSchema("docker-container-list", &ContainerListExecutor{}, makeContainerListSchema())
	server.RegisterExecutorWithSchema("docker-container-create", &ContainerCreateExecutor{}, makeContainerCreateSchema())
	server.RegisterExecutorWithSchema("docker-container-start", &ContainerStartExecutor{}, makeContainerStartSchema())
	server.RegisterExecutorWithSchema("docker-container-stop", &ContainerStopExecutor{}, makeContainerStopSchema())
	server.RegisterExecutorWithSchema("docker-container-delete", &ContainerDeleteExecutor{}, makeContainerDeleteSchema())
	server.RegisterExecutorWithSchema("docker-container-logs", &ContainerLogsExecutor{}, makeContainerLogsSchema())

	// Register image nodes
	server.RegisterExecutorWithSchema("docker-image-list", &ImageListExecutor{}, makeImageListSchema())
	server.RegisterExecutorWithSchema("docker-image-pull", &ImagePullExecutor{}, makeImagePullSchema())
	server.RegisterExecutorWithSchema("docker-image-build", &ImageBuildExecutor{}, makeImageBuildSchema())

	// Register volume nodes
	server.RegisterExecutorWithSchema("docker-volume-list", &VolumeListExecutor{}, makeVolumeListSchema())

	// Register network nodes
	server.RegisterExecutorWithSchema("docker-network-list", &NetworkListExecutor{}, makeNetworkListSchema())

	fmt.Printf("Starting skill-docker gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// createDockerClient creates a Docker client with proper environment handling
func createDockerClient() (*client.Client, error) {
	opts := []client.Opt{
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	}

	// Check for explicit Docker host
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		opts = append(opts, client.WithHost(host))
	}

	// Check for TLS configuration
	if certPath := os.Getenv("DOCKER_CERT_PATH"); certPath != "" {
		opts = append(opts,
			client.WithTLSClientConfig(
				filepath.Join(certPath, "ca.pem"),
				filepath.Join(certPath, "cert.pem"),
				filepath.Join(certPath, "key.pem"),
			),
		)
	}

	return client.NewClientWithOpts(opts...)
}

// ============================================================================
// CONTAINER OPERATIONS
// ============================================================================

type ContainerListExecutor struct{}

func (e *ContainerListExecutor) Type() string { return "docker-container-list" }

func (e *ContainerListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	// Build filter options
	filterArgs := filters.NewArgs()

	if all, ok := config["all"].(bool); ok && all {
		filterArgs.Add("status", "created")
		filterArgs.Add("status", "restarting")
		filterArgs.Add("status", "running")
		filterArgs.Add("status", "removing")
		filterArgs.Add("status", "paused")
		filterArgs.Add("status", "exited")
		filterArgs.Add("status", "dead")
	}

	if name, ok := config["name"].(string); ok && name != "" {
		filterArgs.Add("name", name)
	}

	if status, ok := config["status"].(string); ok && status != "" {
		filterArgs.Add("status", status)
	}

	if label, ok := config["label"].(string); ok && label != "" {
		filterArgs.Add("label", label)
	}

	if ancestor, ok := config["ancestor"].(string); ok && ancestor != "" {
		filterArgs.Add("ancestor", ancestor)
	}

	// Build list options
	listOpts := container.ListOptions{
		All:     filterArgs.Len() == 0, // Show all if no filters
		Filters: filterArgs,
	}

	if limit, ok := config["limit"].(float64); ok && limit > 0 {
		listOpts.Limit = int(limit)
	}

	containers, err := dockerClient.ContainerList(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Convert to output format
	output := make([]map[string]interface{}, 0, len(containers))
	for _, c := range containers {
		output = append(output, map[string]interface{}{
			"id":         c.ID,
			"names":      c.Names,
			"image":      c.Image,
			"imageID":    c.ImageID,
			"command":    c.Command,
			"created":    c.Created,
			"ports":      c.Ports,
			"labels":     c.Labels,
			"state":      c.State,
			"status":     c.Status,
			"hostConfig": map[string]interface{}{"networkMode": string(c.HostConfig.NetworkMode)},
			"networkSettings": map[string]interface{}{
				"networks": c.NetworkSettings,
			},
			"mounts": c.Mounts,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"containers": output,
			"count":      len(output),
		},
	}, nil
}

type ContainerCreateExecutor struct{}

func (e *ContainerCreateExecutor) Type() string { return "docker-container-create" }

func (e *ContainerCreateExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	imageName, ok := config["image"].(string)
	if !ok || imageName == "" {
		return nil, fmt.Errorf("image is required")
	}

	// Build container config
	containerConfig := &container.Config{
		Image: imageName,
	}

	if name, ok := config["name"].(string); ok && name != "" {
		containerConfig.Hostname = name
	}

	if cmd, ok := config["command"].(string); ok && cmd != "" {
		containerConfig.Cmd = strings.Fields(cmd)
	}

	if entrypoint, ok := config["entrypoint"].(string); ok && entrypoint != "" {
		containerConfig.Entrypoint = strings.Fields(entrypoint)
	}

	if workdir, ok := config["workdir"].(string); ok && workdir != "" {
		containerConfig.WorkingDir = workdir
	}

	if user, ok := config["user"].(string); ok && user != "" {
		containerConfig.User = user
	}

	// Environment variables
	if envRaw, ok := config["env"]; ok {
		switch v := envRaw.(type) {
		case map[string]interface{}:
			for k, val := range v {
				containerConfig.Env = append(containerConfig.Env, fmt.Sprintf("%s=%v", k, val))
			}
		case []interface{}:
			for _, e := range v {
				if s, ok := e.(string); ok {
					containerConfig.Env = append(containerConfig.Env, s)
				}
			}
		}
	}

	// Labels
	if labelsRaw, ok := config["labels"].(map[string]interface{}); ok {
		containerConfig.Labels = make(map[string]string)
		for k, v := range labelsRaw {
			if s, ok := v.(string); ok {
				containerConfig.Labels[k] = s
			}
		}
	}

	// Exposed ports
	if portsRaw, ok := config["ports"]; ok {
		containerConfig.ExposedPorts = make(nat.PortSet)
		switch v := portsRaw.(type) {
		case []interface{}:
			for _, p := range v {
				if s, ok := p.(string); ok {
					port := nat.Port(s)
					containerConfig.ExposedPorts[port] = struct{}{}
				}
			}
		}
	}

	// Build host config
	hostConfig := &container.HostConfig{}

	// Port bindings
	if portsRaw, ok := config["ports"]; ok {
		hostConfig.PortBindings = make(nat.PortMap)
		switch v := portsRaw.(type) {
		case []interface{}:
			for _, p := range v {
				if s, ok := p.(string); ok {
					parts := strings.Split(s, ":")
					if len(parts) == 2 {
						hostPort := parts[0]
						containerPort := parts[1]
						port := nat.Port(containerPort + "/tcp")
						hostConfig.PortBindings[port] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: hostPort}}
					} else if len(parts) == 3 {
						hostIP := parts[0]
						hostPort := parts[1]
						containerPort := parts[2]
						port := nat.Port(containerPort + "/tcp")
						hostConfig.PortBindings[port] = []nat.PortBinding{{HostIP: hostIP, HostPort: hostPort}}
					}
				}
			}
		}
	}

	// Volume mounts
	if volumesRaw, ok := config["volumes"]; ok {
		switch v := volumesRaw.(type) {
		case []interface{}:
			for _, vol := range v {
				if s, ok := vol.(string); ok {
					parts := strings.Split(s, ":")
					if len(parts) >= 2 {
						hostConfig.Mounts = append(hostConfig.Mounts, mount.Mount{
							Type:   mount.TypeVolume,
							Source: parts[0],
							Target: parts[1],
						})
					}
				}
			}
		}
	}

	// Network
	if networkName, ok := config["network"].(string); ok && networkName != "" {
		hostConfig.NetworkMode = container.NetworkMode(networkName)
	}

	// Restart policy
	if restart, ok := config["restart"].(string); ok && restart != "" {
		hostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyMode(restart)}
	}

	// Resource limits
	if memory, ok := config["memory"].(float64); ok && memory > 0 {
		hostConfig.Memory = int64(memory) * 1024 * 1024 // Convert MB to bytes
	}
	if memoryStr, ok := config["memory"].(string); ok && memoryStr != "" {
		// Parse memory string like "512m" or "1g"
		if mem, err := parseMemoryString(memoryStr); err == nil {
			hostConfig.Memory = mem
		}
	}
	if cpus, ok := config["cpus"].(float64); ok && cpus > 0 {
		hostConfig.NanoCPUs = int64(cpus * 1e9)
	}

	// Privileged
	if privileged, ok := config["privileged"].(bool); ok && privileged {
		hostConfig.Privileged = true
	}

	// Auto remove
	if autoRemove, ok := config["autoRemove"].(bool); ok && autoRemove {
		hostConfig.AutoRemove = true
	}

	// Create container
	name := ""
	if n, ok := config["name"].(string); ok && n != "" {
		name = n
	}

	resp, err := dockerClient.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Attach to network if specified
	if networkName, ok := config["network"].(string); ok && networkName != "" {
		err = dockerClient.NetworkConnect(ctx, networkName, resp.ID, nil)
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			// Non-fatal error, just log
			fmt.Fprintf(os.Stderr, "Warning: failed to connect to network: %v\n", err)
		}
	}

	// Start container if requested
	if startNow, ok := config["start"].(bool); ok && startNow {
		err = dockerClient.ContainerStart(ctx, resp.ID, container.StartOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to start container: %w", err)
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":       resp.ID,
			"name":     name,
			"warnings": resp.Warnings,
			"started": func() bool {
				if startNow, ok := config["start"].(bool); ok {
					return startNow
				}
				return false
			}(),
		},
	}, nil
}

type ContainerStartExecutor struct{}

func (e *ContainerStartExecutor) Type() string { return "docker-container-start" }

func (e *ContainerStartExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	containerID, ok := config["container"].(string)
	if !ok || containerID == "" {
		return nil, fmt.Errorf("container is required")
	}

	opts := container.StartOptions{}
	if checkpoint, ok := config["checkpoint"].(string); ok && checkpoint != "" {
		opts.CheckpointID = checkpoint
	}
	if checkpointDir, ok := config["checkpointDir"].(string); ok && checkpointDir != "" {
		opts.CheckpointDir = checkpointDir
	}

	err := dockerClient.ContainerStart(ctx, containerID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Get container info to return details
	info, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":        info.ID,
			"name":      info.Name,
			"state":     info.State.Status,
			"running":   info.State.Running,
			"startedAt": info.State.StartedAt,
		},
	}, nil
}

type ContainerStopExecutor struct{}

func (e *ContainerStopExecutor) Type() string { return "docker-container-stop" }

func (e *ContainerStopExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	containerID, ok := config["container"].(string)
	if !ok || containerID == "" {
		return nil, fmt.Errorf("container is required")
	}

	timeout := 10 // default 10 seconds
	if t, ok := config["timeout"].(float64); ok {
		timeout = int(t)
	}

	err := dockerClient.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: &timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to stop container: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      containerID,
			"stopped": true,
		},
	}, nil
}

type ContainerDeleteExecutor struct{}

func (e *ContainerDeleteExecutor) Type() string { return "docker-container-delete" }

func (e *ContainerDeleteExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	containerID, ok := config["container"].(string)
	if !ok || containerID == "" {
		return nil, fmt.Errorf("container is required")
	}

	force := false
	if f, ok := config["force"].(bool); ok {
		force = f
	}

	removeVolumes := false
	if rv, ok := config["removeVolumes"].(bool); ok {
		removeVolumes = rv
	}

	err := dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{
		RemoveVolumes: removeVolumes,
		Force:         force,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to delete container: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":      containerID,
			"deleted": true,
		},
	}, nil
}

type ContainerLogsExecutor struct{}

func (e *ContainerLogsExecutor) Type() string { return "docker-container-logs" }

func (e *ContainerLogsExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	containerID, ok := config["container"].(string)
	if !ok || containerID == "" {
		return nil, fmt.Errorf("container is required")
	}

	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	}

	if tail, ok := config["tail"].(string); ok && tail != "" {
		opts.Tail = tail
	} else {
		opts.Tail = "100" // default 100 lines
	}

	if since, ok := config["since"].(string); ok && since != "" {
		opts.Since = since
	}

	if until, ok := config["until"].(string); ok && until != "" {
		opts.Until = until
	}

	if timestamps, ok := config["timestamps"].(bool); ok && timestamps {
		opts.Timestamps = true
	}

	if follow, ok := config["follow"].(bool); ok && follow {
		opts.Follow = true
	}

	reader, err := dockerClient.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get container logs: %w", err)
	}
	defer reader.Close()

	// Read all logs
	logs, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read logs: %w", err)
	}

	// Docker logs have a header, strip it for cleaner output
	logContent := string(logs)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"container": containerID,
			"logs":      logContent,
		},
	}, nil
}

// ============================================================================
// IMAGE OPERATIONS
// ============================================================================

type ImageListExecutor struct{}

func (e *ImageListExecutor) Type() string { return "docker-image-list" }

func (e *ImageListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	// Build filter options
	filterArgs := filters.NewArgs()

	if name, ok := config["name"].(string); ok && name != "" {
		filterArgs.Add("reference", name)
	}

	if dangling, ok := config["dangling"].(bool); ok {
		if dangling {
			filterArgs.Add("dangling", "true")
		} else {
			filterArgs.Add("dangling", "false")
		}
	}

	listOpts := image.ListOptions{
		All:     false,
		Filters: filterArgs,
	}

	if all, ok := config["all"].(bool); ok && all {
		listOpts.All = true
	}

	images, err := dockerClient.ImageList(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	// Convert to output format
	output := make([]map[string]interface{}, 0, len(images))
	for _, img := range images {
		output = append(output, map[string]interface{}{
			"id":          img.ID,
			"repoTags":    img.RepoTags,
			"repoDigests": img.RepoDigests,
			"created":     img.Created,
			"size":        img.Size,
			"virtualSize": img.VirtualSize,
			"labels":      img.Labels,
			"containers":  img.Containers,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"images": output,
			"count":  len(output),
		},
	}, nil
}

type ImagePullExecutor struct{}

func (e *ImagePullExecutor) Type() string { return "docker-image-pull" }

func (e *ImagePullExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	imageRef, ok := config["image"].(string)
	if !ok || imageRef == "" {
		return nil, fmt.Errorf("image is required")
	}

	opts := image.PullOptions{}

	// Handle platform
	if platform, ok := config["platform"].(string); ok && platform != "" {
		opts.Platform = platform
	}

	// Handle authentication
	if username, ok := config["username"].(string); ok && username != "" {
		if password, ok := config["password"].(string); ok && password != "" {
			authConfig := registry.AuthConfig{
				Username: username,
				Password: password,
			}
			encodedJSON, err := json.Marshal(authConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to encode auth config: %w", err)
			}
			opts.RegistryAuth = base64.URLEncoding.EncodeToString(encodedJSON)
		}
	}

	// Pull the image
	reader, err := dockerClient.ImagePull(ctx, imageRef, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to pull image: %w", err)
	}
	defer reader.Close()

	// Read pull output
	output, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read pull output: %w", err)
	}

	// Parse the output to get image info
	inspect, _, err := dockerClient.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect pulled image: %w", err)
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":       inspect.ID,
			"image":    imageRef,
			"repoTags": inspect.RepoTags,
			"size":     inspect.Size,
			"output":   string(output),
		},
	}, nil
}

type ImageBuildExecutor struct{}

func (e *ImageBuildExecutor) Type() string { return "docker-image-build" }

func (e *ImageBuildExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	contextPath, ok := config["context"].(string)
	if !ok || contextPath == "" {
		return nil, fmt.Errorf("context is required")
	}

	dockerfile := "Dockerfile"
	if df, ok := config["dockerfile"].(string); ok && df != "" {
		dockerfile = df
	}

	// Build options
	opts := types.ImageBuildOptions{
		Dockerfile: dockerfile,
		Remove:     true,
	}

	// Tags
	if tagRaw, ok := config["tag"]; ok {
		switch v := tagRaw.(type) {
		case string:
			opts.Tags = []string{v}
		case []interface{}:
			for _, t := range v {
				if s, ok := t.(string); ok {
					opts.Tags = append(opts.Tags, s)
				}
			}
		}
	}

	// Build args
	if buildArgsRaw, ok := config["buildArgs"].(map[string]interface{}); ok {
		opts.BuildArgs = make(map[string]*string)
		for k, v := range buildArgsRaw {
			if s, ok := v.(string); ok {
				opts.BuildArgs[k] = &s
			}
		}
	}

	// Target
	if target, ok := config["target"].(string); ok && target != "" {
		opts.Target = target
	}

	// Platform
	if platform, ok := config["platform"].(string); ok && platform != "" {
		opts.Platform = platform
	}

	// No cache
	if noCache, ok := config["noCache"].(bool); ok && noCache {
		opts.NoCache = true
	}

	// Pull base image
	if pullParent, ok := config["pullParent"].(bool); ok && pullParent {
		opts.PullParent = true
	}

	// Labels
	if labelsRaw, ok := config["labels"].(map[string]interface{}); ok {
		opts.Labels = make(map[string]string)
		for k, v := range labelsRaw {
			if s, ok := v.(string); ok {
				opts.Labels[k] = s
			}
		}
	}

	// Create tar archive from context
	tarReader, err := createTarFromContext(contextPath, dockerfile)
	if err != nil {
		return nil, fmt.Errorf("failed to create context archive: %w", err)
	}
	defer tarReader.Close()

	// Build the image
	resp, err := dockerClient.ImageBuild(ctx, tarReader, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build image: %w", err)
	}
	defer resp.Body.Close()

	// Read build output
	output, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read build output: %w", err)
	}

	// Get image info from first tag
	var imageID string
	var repoTags []string
	if len(opts.Tags) > 0 {
		inspect, _, err := dockerClient.ImageInspectWithRaw(ctx, opts.Tags[0])
		if err == nil {
			imageID = inspect.ID
			repoTags = inspect.RepoTags
		}
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"id":       imageID,
			"tags":     opts.Tags,
			"repoTags": repoTags,
			"output":   string(output),
		},
	}, nil
}

// createTarFromContext creates a tar archive from the build context
func createTarFromContext(contextPath, dockerfile string) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer tw.Close()

		err := filepath.Walk(contextPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Skip .dockerignore, .git, etc.
			if info.Name() == ".git" || info.Name() == ".dockerignore" {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			// Get relative path
			relPath, err := filepath.Rel(contextPath, path)
			if err != nil {
				return err
			}

			// Create tar header
			header, err := tar.FileInfoHeader(info, info.Name())
			if err != nil {
				return err
			}
			header.Name = relPath

			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			if !info.IsDir() {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				defer file.Close()

				if _, err := io.Copy(tw, file); err != nil {
					return err
				}
			}

			return nil
		})

		if err != nil {
			pw.CloseWithError(err)
		}
	}()

	return pr, nil
}

// ============================================================================
// VOLUME OPERATIONS
// ============================================================================

type VolumeListExecutor struct{}

func (e *VolumeListExecutor) Type() string { return "docker-volume-list" }

func (e *VolumeListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	// Build filter options
	filterArgs := filters.NewArgs()

	if name, ok := config["name"].(string); ok && name != "" {
		filterArgs.Add("name", name)
	}

	if driver, ok := config["driver"].(string); ok && driver != "" {
		filterArgs.Add("driver", driver)
	}

	if dangling, ok := config["dangling"].(bool); ok {
		if dangling {
			filterArgs.Add("dangling", "true")
		} else {
			filterArgs.Add("dangling", "false")
		}
	}

	volumes, err := dockerClient.VolumeList(ctx, volume.ListOptions{
		Filters: filterArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list volumes: %w", err)
	}

	// Convert to output format
	output := make([]map[string]interface{}, 0, len(volumes.Volumes))
	for _, vol := range volumes.Volumes {
		output = append(output, map[string]interface{}{
			"name":       vol.Name,
			"driver":     vol.Driver,
			"mountpoint": vol.Mountpoint,
			"createdAt":  vol.CreatedAt,
			"labels":     vol.Labels,
			"scope":      vol.Scope,
			"options":    vol.Options,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"volumes":  output,
			"count":    len(output),
			"warnings": volumes.Warnings,
		},
	}, nil
}

// ============================================================================
// NETWORK OPERATIONS
// ============================================================================

type NetworkListExecutor struct{}

func (e *NetworkListExecutor) Type() string { return "docker-network-list" }

func (e *NetworkListExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := step.Config

	// Build filter options
	filterArgs := filters.NewArgs()

	if name, ok := config["name"].(string); ok && name != "" {
		filterArgs.Add("name", name)
	}

	if networkType, ok := config["type"].(string); ok && networkType != "" {
		filterArgs.Add("type", networkType)
	}

	if driver, ok := config["driver"].(string); ok && driver != "" {
		filterArgs.Add("driver", driver)
	}

	networks, err := dockerClient.NetworkList(ctx, network.ListOptions{
		Filters: filterArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list networks: %w", err)
	}

	// Convert to output format
	output := make([]map[string]interface{}, 0, len(networks))
	for _, net := range networks {
		containers := make([]map[string]interface{}, 0, len(net.Containers))
		for id, ep := range net.Containers {
			containers = append(containers, map[string]interface{}{
				"id":         id,
				"name":       ep.Name,
				"ipv4":       ep.IPv4Address,
				"ipv6":       ep.IPv6Address,
				"macAddress": ep.MacAddress,
			})
		}

		output = append(output, map[string]interface{}{
			"id":         net.ID,
			"name":       net.Name,
			"driver":     net.Driver,
			"scope":      net.Scope,
			"attachable": net.Attachable,
			"ingress":    net.Ingress,
			"internal":   net.Internal,
			"enableIPv6": net.EnableIPv6,
			"ipam": map[string]interface{}{
				"driver": net.IPAM.Driver,
				"config": net.IPAM.Config,
			},
			"containers": containers,
			"labels":     net.Labels,
			"created":    net.Created,
		})
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"networks": output,
			"count":    len(output),
		},
	}, nil
}

// ============================================================================
// SCHEMA DEFINITIONS
// ============================================================================

func makeContainerListSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-container-list",
		DisplayName: "List Containers",
		Category:    "action",
		Description: "List Docker containers with optional filtering",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "all", Type: resolver.FieldTypeToggle, Label: "Show All", Hint: "Show all containers including stopped", Default: false},
					{Key: "limit", Type: resolver.FieldTypeNumber, Label: "Limit", Hint: "Maximum number of containers", Default: 100, Min: ptr(1), Max: ptr(1000)},
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name Filter", Hint: "Filter by container name", Placeholder: "my-container"},
					{Key: "status", Type: resolver.FieldTypeSelect, Label: "Status", Hint: "Filter by status", Options: []resolver.SelectOption{
						{Label: "All", Value: ""},
						{Label: "Created", Value: "created"},
						{Label: "Running", Value: "running"},
						{Label: "Paused", Value: "paused"},
						{Label: "Restarting", Value: "restarting"},
						{Label: "Removing", Value: "removing"},
						{Label: "Exited", Value: "exited"},
						{Label: "Dead", Value: "dead"},
					}},
					{Key: "label", Type: resolver.FieldTypeText, Label: "Label Filter", Hint: "Filter by label (e.g., app=myapp)", Placeholder: "app=myapp"},
					{Key: "ancestor", Type: resolver.FieldTypeText, Label: "Ancestor", Hint: "Filter by image name", Placeholder: "nginx:latest"},
				},
			},
		},
	}
}

func makeContainerCreateSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-container-create",
		DisplayName: "Create Container",
		Category:    "action",
		Description: "Create and optionally start a Docker container",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Image",
				Fields: []*resolver.FieldSchema{
					{Key: "image", Type: resolver.FieldTypeText, Label: "Image", Required: true, Placeholder: "nginx:latest"},
					{Key: "name", Type: resolver.FieldTypeText, Label: "Container Name", Hint: "Optional container name", Placeholder: "my-container"},
				},
			},
			{
				Title: "Command",
				Fields: []*resolver.FieldSchema{
					{Key: "command", Type: resolver.FieldTypeText, Label: "Command", Hint: "Command to run", Placeholder: "nginx -g 'daemon off;'"},
					{Key: "entrypoint", Type: resolver.FieldTypeText, Label: "Entrypoint", Hint: "Override entrypoint", Placeholder: "/bin/sh"},
					{Key: "workdir", Type: resolver.FieldTypeText, Label: "Working Dir", Hint: "Working directory", Placeholder: "/app"},
					{Key: "user", Type: resolver.FieldTypeText, Label: "User", Hint: "User to run as", Placeholder: "1000:1000"},
				},
			},
			{
				Title: "Environment",
				Fields: []*resolver.FieldSchema{
					{Key: "env", Type: resolver.FieldTypeKeyValue, Label: "Environment Variables", Hint: "Key-value pairs", KeyPlaceholder: "KEY", ValuePlaceholder: "value"},
					{Key: "labels", Type: resolver.FieldTypeKeyValue, Label: "Labels", Hint: "Container labels", KeyPlaceholder: "label", ValuePlaceholder: "value"},
				},
			},
			{
				Title: "Networking",
				Fields: []*resolver.FieldSchema{
					{Key: "ports", Type: resolver.FieldTypeTags, Label: "Port Mappings", Hint: "Format: hostPort:containerPort (e.g., 8080:80)"},
					{Key: "network", Type: resolver.FieldTypeText, Label: "Network", Hint: "Network name", Placeholder: "bridge"},
				},
			},
			{
				Title: "Storage",
				Fields: []*resolver.FieldSchema{
					{Key: "volumes", Type: resolver.FieldTypeTags, Label: "Volume Mounts", Hint: "Format: hostPath:containerPath"},
				},
			},
			{
				Title: "Resources",
				Fields: []*resolver.FieldSchema{
					{Key: "memory", Type: resolver.FieldTypeText, Label: "Memory Limit", Hint: "e.g., 512m, 1g", Placeholder: "512m"},
					{Key: "cpus", Type: resolver.FieldTypeNumber, Label: "CPU Limit", Hint: "Number of CPUs", Min: ptr(0), Max: ptr(128)},
				},
			},
			{
				Title: "Options",
				Fields: []*resolver.FieldSchema{
					{Key: "restart", Type: resolver.FieldTypeSelect, Label: "Restart Policy", Options: []resolver.SelectOption{
						{Label: "No", Value: "no"},
						{Label: "Always", Value: "always"},
						{Label: "On Failure", Value: "on-failure"},
						{Label: "Unless Stopped", Value: "unless-stopped"},
					}},
					{Key: "privileged", Type: resolver.FieldTypeToggle, Label: "Privileged", Default: false},
					{Key: "autoRemove", Type: resolver.FieldTypeToggle, Label: "Auto Remove", Hint: "Remove container on exit", Default: false},
					{Key: "start", Type: resolver.FieldTypeToggle, Label: "Start Now", Hint: "Start container after creation", Default: true},
				},
			},
		},
	}
}

func makeContainerStartSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-container-start",
		DisplayName: "Start Container",
		Category:    "action",
		Description: "Start a stopped Docker container",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "container", Type: resolver.FieldTypeText, Label: "Container", Required: true, Placeholder: "container-name or ID"},
					{Key: "checkpoint", Type: resolver.FieldTypeText, Label: "Checkpoint", Hint: "Restore from checkpoint", Placeholder: "checkpoint-name"},
					{Key: "checkpointDir", Type: resolver.FieldTypeText, Label: "Checkpoint Dir", Hint: "Checkpoint directory", Placeholder: "/path/to/checkpoint"},
				},
			},
		},
	}
}

func makeContainerStopSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-container-stop",
		DisplayName: "Stop Container",
		Category:    "action",
		Description: "Stop a running Docker container",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "container", Type: resolver.FieldTypeText, Label: "Container", Required: true, Placeholder: "container-name or ID"},
					{Key: "timeout", Type: resolver.FieldTypeNumber, Label: "Timeout (seconds)", Hint: "Seconds to wait before killing", Default: 10, Min: ptr(1), Max: ptr(300)},
				},
			},
		},
	}
}

func makeContainerDeleteSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-container-delete",
		DisplayName: "Delete Container",
		Category:    "action",
		Description: "Delete a Docker container",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "container", Type: resolver.FieldTypeText, Label: "Container", Required: true, Placeholder: "container-name or ID"},
				},
			},
			{
				Title: "Options",
				Fields: []*resolver.FieldSchema{
					{Key: "force", Type: resolver.FieldTypeToggle, Label: "Force", Hint: "Force removal of running container", Default: false},
					{Key: "removeVolumes", Type: resolver.FieldTypeToggle, Label: "Remove Volumes", Hint: "Remove associated volumes", Default: false},
				},
			},
		},
	}
}

func makeContainerLogsSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-container-logs",
		DisplayName: "Get Container Logs",
		Category:    "action",
		Description: "Get logs from a Docker container",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Target",
				Fields: []*resolver.FieldSchema{
					{Key: "container", Type: resolver.FieldTypeText, Label: "Container", Required: true, Placeholder: "container-name or ID"},
				},
			},
			{
				Title: "Options",
				Fields: []*resolver.FieldSchema{
					{Key: "tail", Type: resolver.FieldTypeText, Label: "Tail", Hint: "Number of lines (or 'all')", Default: "100", Placeholder: "100"},
					{Key: "since", Type: resolver.FieldTypeText, Label: "Since", Hint: "Show logs since timestamp", Placeholder: "2024-01-01T00:00:00"},
					{Key: "until", Type: resolver.FieldTypeText, Label: "Until", Hint: "Show logs until timestamp", Placeholder: "2024-01-02T00:00:00"},
					{Key: "timestamps", Type: resolver.FieldTypeToggle, Label: "Show Timestamps", Default: false},
					{Key: "follow", Type: resolver.FieldTypeToggle, Label: "Follow", Hint: "Follow log output", Default: false},
				},
			},
		},
	}
}

func makeImageListSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-image-list",
		DisplayName: "List Images",
		Category:    "action",
		Description: "List Docker images with optional filtering",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "all", Type: resolver.FieldTypeToggle, Label: "Show All", Hint: "Show all images including intermediate", Default: false},
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name Filter", Hint: "Filter by image name/reference", Placeholder: "nginx"},
					{Key: "dangling", Type: resolver.FieldTypeToggle, Label: "Dangling Only", Hint: "Show only dangling (untagged) images", Default: false},
				},
			},
		},
	}
}

func makeImagePullSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-image-pull",
		DisplayName: "Pull Image",
		Category:    "action",
		Description: "Pull a Docker image from a registry",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Image",
				Fields: []*resolver.FieldSchema{
					{Key: "image", Type: resolver.FieldTypeText, Label: "Image", Required: true, Placeholder: "nginx:latest"},
					{Key: "platform", Type: resolver.FieldTypeText, Label: "Platform", Hint: "Target platform", Placeholder: "linux/amd64"},
				},
			},
			{
				Title: "Authentication (Optional)",
				Fields: []*resolver.FieldSchema{
					{Key: "username", Type: resolver.FieldTypeText, Label: "Username", Placeholder: "registry-user"},
					{Key: "password", Type: resolver.FieldTypeText, Label: "Password", Placeholder: "registry-password", Sensitive: true},
				},
			},
		},
	}
}

func makeImageBuildSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-image-build",
		DisplayName: "Build Image",
		Category:    "action",
		Description: "Build a Docker image from a Dockerfile",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Build Context",
				Fields: []*resolver.FieldSchema{
					{Key: "context", Type: resolver.FieldTypeText, Label: "Context Path", Required: true, Placeholder: "./app"},
					{Key: "dockerfile", Type: resolver.FieldTypeText, Label: "Dockerfile", Hint: "Dockerfile path", Default: "Dockerfile", Placeholder: "Dockerfile"},
				},
			},
			{
				Title: "Image Tags",
				Fields: []*resolver.FieldSchema{
					{Key: "tag", Type: resolver.FieldTypeTags, Label: "Tags", Hint: "Image tags (e.g., myapp:latest)"},
				},
			},
			{
				Title: "Build Options",
				Fields: []*resolver.FieldSchema{
					{Key: "buildArgs", Type: resolver.FieldTypeKeyValue, Label: "Build Args", Hint: "Build-time variables", KeyPlaceholder: "ARG", ValuePlaceholder: "value"},
					{Key: "target", Type: resolver.FieldTypeText, Label: "Target", Hint: "Build stage target", Placeholder: "production"},
					{Key: "platform", Type: resolver.FieldTypeText, Label: "Platform", Hint: "Target platform", Placeholder: "linux/amd64"},
					{Key: "labels", Type: resolver.FieldTypeKeyValue, Label: "Labels", Hint: "Image labels", KeyPlaceholder: "label", ValuePlaceholder: "value"},
				},
			},
			{
				Title: "Advanced",
				Fields: []*resolver.FieldSchema{
					{Key: "noCache", Type: resolver.FieldTypeToggle, Label: "No Cache", Default: false},
					{Key: "pullParent", Type: resolver.FieldTypeToggle, Label: "Pull Parent", Hint: "Pull base image before build", Default: false},
				},
			},
		},
	}
}

func makeVolumeListSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-volume-list",
		DisplayName: "List Volumes",
		Category:    "action",
		Description: "List Docker volumes with optional filtering",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name Filter", Hint: "Filter by volume name", Placeholder: "my-volume"},
					{Key: "driver", Type: resolver.FieldTypeText, Label: "Driver Filter", Hint: "Filter by driver", Placeholder: "local"},
					{Key: "dangling", Type: resolver.FieldTypeToggle, Label: "Dangling Only", Hint: "Show only unused volumes", Default: false},
				},
			},
		},
	}
}

func makeNetworkListSchema() *resolver.NodeSchema {
	return &resolver.NodeSchema{
		Name:        "docker-network-list",
		DisplayName: "List Networks",
		Category:    "action",
		Description: "List Docker networks with optional filtering",
		Icon:        iconDocker,
		Sections: []*resolver.ConfigSection{
			{
				Title: "Filters",
				Fields: []*resolver.FieldSchema{
					{Key: "name", Type: resolver.FieldTypeText, Label: "Name Filter", Hint: "Filter by network name", Placeholder: "my-network"},
					{Key: "type", Type: resolver.FieldTypeSelect, Label: "Type", Options: []resolver.SelectOption{
						{Label: "All", Value: ""},
						{Label: "Custom", Value: "custom"},
						{Label: "Built-in", Value: "builtin"},
					}},
					{Key: "driver", Type: resolver.FieldTypeText, Label: "Driver Filter", Hint: "Filter by driver", Placeholder: "bridge"},
				},
			},
		},
	}
}

// Helper function for pointer values
func ptr(v float64) *float64 {
	return &v
}

// parseMemoryString parses memory strings like "512m", "1g", "1024k"
func parseMemoryString(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	var multiplier int64 = 1

	if strings.HasSuffix(s, "g") {
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "g")
	} else if strings.HasSuffix(s, "m") {
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "m")
	} else if strings.HasSuffix(s, "k") {
		multiplier = 1024
		s = strings.TrimSuffix(s, "k")
	}

	var value int64
	_, err := fmt.Sscanf(s, "%d", &value)
	if err != nil {
		return 0, err
	}

	return value * multiplier, nil
}
