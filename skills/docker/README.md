# Docker Skill

Docker container and image operations for Atlas agents.

## Overview

This skill provides comprehensive Docker operations including image building, container management, registry operations, and Docker Compose support.

## Features

- **Image Operations**: Build, push, pull, tag, and remove images
- **Container Operations**: Run, stop, remove, inspect containers
- **Container Interaction**: Execute commands, copy files, view logs
- **Registry Operations**: Login/logout to container registries
- **Docker Compose**: Deploy and manage multi-container applications

## Node Types

### Image Operations

#### docker-build

Build a Docker image from a Dockerfile.

**Configuration:**
- `context`: Build context path
- `dockerfile`: Dockerfile path (default: Dockerfile)
- `tag`: Image tag (can specify multiple)
- `buildArgs`: Build arguments (key-value)
- `target`: Target build stage
- `noCache`: Disable cache (default: false)
- `pull`: Pull base images (default: false)
- `platform`: Target platform (e.g., linux/amd64)
- `labels`: Image labels
- `quiet`: Suppress build output (default: false)

**Output:**
- `imageId`: Built image ID
- `tags`: Applied tags
- `output`: Build output

#### docker-push

Push an image to a registry.

**Configuration:**
- `image`: Image name with tag
- `registry`: Registry URL (optional, uses default)
- `username`: Registry username (optional)
- `password`: Registry password (optional)

**Output:**
- `digest`: Image digest
- `status`: Push status

#### docker-pull

Pull an image from a registry.

**Configuration:**
- `image`: Image name with tag
- `platform`: Target platform (optional)
- `quiet`: Suppress output (default: false)

**Output:**
- `imageId`: Pulled image ID
- `digest`: Image digest

#### docker-tag

Tag an image.

**Configuration:**
- `source`: Source image
- `target`: Target image tag

**Output:**
- `status`: Tag status

#### docker-rmi

Remove an image.

**Configuration:**
- `image`: Image name or ID
- `force`: Force removal (default: false)
- `noPrune`: Don't prune untagged parents (default: false)

**Output:**
- `removed`: Removed images
- `freedSpace`: Freed disk space

#### docker-images

List images.

**Configuration:**
- `all`: Show all images (default: false)
- `filters`: Filter images (key-value)
- `format`: Output format (json, table)

**Output:**
- `images`: List of images
- `count`: Image count

### Container Operations

#### docker-run

Run a container.

**Configuration:**
- `image`: Image name
- `name`: Container name (optional)
- `command`: Command to run (optional)
- `args`: Command arguments
- `env`: Environment variables (key-value)
- `ports`: Port mappings (e.g., "8080:80")
- `volumes`: Volume mounts (e.g., "/host:/container")
- `workdir`: Working directory
- `user`: User to run as
- `detach`: Run in background (default: true)
- `remove`: Remove container on exit (default: false)
- `network`: Network to connect to
- `restart`: Restart policy (no, always, on-failure)
- `memory`: Memory limit (e.g., "512m")
- `cpus`: CPU limit (e.g., "0.5")
- `privileged`: Run in privileged mode (default: false)

**Output:**
- `containerId`: Container ID
- `name`: Container name
- `status`: Container status

#### docker-stop

Stop a container.

**Configuration:**
- `container`: Container name or ID
- `time`: Seconds to wait before killing (default: 10)

**Output:**
- `containerId`: Container ID
- `status`: Stop status

#### docker-rm

Remove a container.

**Configuration:**
- `container`: Container name or ID
- `force`: Force removal (default: false)
- `volumes`: Remove associated volumes (default: false)

**Output:**
- `containerId`: Container ID
- `status`: Removal status

#### docker-ps

List containers.

**Configuration:**
- `all`: Show all containers (default: false)
- `filter`: Filter containers (key-value)
- `format`: Output format (json, table)
- `last`: Show n last created containers

**Output:**
- `containers`: List of containers
- `count`: Container count

#### docker-logs

Get container logs.

**Configuration:**
- `container`: Container name or ID
- `follow`: Follow log output (default: false)
- `tail`: Number of lines to show (default: all)
- `since`: Show logs since timestamp
- `timestamps`: Show timestamps (default: false)

**Output:**
- `logs`: Log output

#### docker-exec

Execute a command in a container.

**Configuration:**
- `container`: Container name or ID
- `command`: Command to execute
- `args`: Command arguments
- `env`: Environment variables
- `user`: User to run as
- `workdir`: Working directory
- `detach`: Run in background (default: false)
- `interactive`: Keep STDIN open (default: false)
- `tty`: Allocate a TTY (default: false)

**Output:**
- `exitCode`: Command exit code
- `output`: Command output

#### docker-cp

Copy files to/from a container.

**Configuration:**
- `container`: Container name or ID
- `source`: Source path
- `destination`: Destination path
- `direction`: Copy direction (to, from)

**Output:**
- `status`: Copy status

#### docker-inspect

Get container details.

**Configuration:**
- `container`: Container name or ID
- `format`: Output format (json, go template)

**Output:**
- `details`: Container details
- `state`: Container state
- `network`: Network settings

### Registry Operations

#### docker-login

Login to a container registry.

**Configuration:**
- `registry`: Registry URL (optional, uses Docker Hub)
- `username`: Username
- `password`: Password

**Output:**
- `status`: Login status
- `registry`: Registry URL

#### docker-logout

Logout from a container registry.

**Configuration:**
- `registry`: Registry URL (optional)

**Output:**
- `status`: Logout status

### Docker Compose Operations

#### docker-compose-up

Deploy a Docker Compose application.

**Configuration:**
- `file`: Compose file path (default: docker-compose.yml)
- `projectName`: Project name (optional)
- `detach`: Run in background (default: true)
- `build`: Build images before starting (default: false)
- `forceRecreate`: Recreate containers (default: false)
- `noDeps`: Don't start linked services (default: false)
- `services`: Specific services to start (optional)

**Output:**
- `services`: Started services
- `status`: Deployment status

#### docker-compose-down

Stop and remove a Docker Compose application.

**Configuration:**
- `file`: Compose file path
- `projectName`: Project name
- `volumes`: Remove volumes (default: false)
- `images`: Remove images (all, local)
- `removeOrphans`: Remove orphan containers (default: false)

**Output:**
- `removed`: Removed resources
- `status`: Removal status

#### docker-compose-ps

List Docker Compose services.

**Configuration:**
- `file`: Compose file path
- `projectName`: Project name

**Output:**
- `services`: List of services
- `status`: Service status

#### docker-compose-logs

Get Docker Compose logs.

**Configuration:**
- `file`: Compose file path
- `projectName`: Project name
- `services`: Specific services (optional)
- `tail`: Number of lines (default: all)
- `follow`: Follow output (default: false)

**Output:**
- `logs`: Log output

## Usage Examples

### Build and Push an Image

```yaml
- id: build-image
  type: docker-build
  config:
    context: ./app
    dockerfile: Dockerfile
    tag:
      - myapp:latest
      - myapp:1.0.0
    buildArgs:
      NODE_ENV: production

- id: push-image
  type: docker-push
  config:
    image: myregistry.com/myapp:latest
    username: "{{secrets.registry_user}}"
    password: "{{secrets.registry_password}}"
```

### Run a Container

```yaml
- id: run-container
  type: docker-run
  config:
    image: nginx:latest
    name: web-server
    ports:
      - "8080:80"
    volumes:
      - "./html:/usr/share/nginx/html"
    env:
      NGINX_HOST: example.com
    detach: true
```

### Execute Command in Container

```yaml
- id: exec-command
  type: docker-exec
  config:
    container: web-server
    command: nginx
    args:
      - "-s"
      - "reload"
```

### Deploy with Docker Compose

```yaml
- id: deploy-app
  type: docker-compose-up
  config:
    file: docker-compose.prod.yml
    projectName: myapp
    build: true
    detach: true
```

### Get Container Logs

```yaml
- id: get-logs
  type: docker-logs
  config:
    container: myapp
    tail: "100"
    timestamps: true
```

### Login to Registry

```yaml
- id: registry-login
  type: docker-login
  config:
    registry: myregistry.azurecr.io
    username: "{{secrets.acr_username}}"
    password: "{{secrets.acr_password}}"
```

## Security Considerations

- Use secrets for registry credentials
- Avoid running containers as root
- Use minimal base images
- Scan images for vulnerabilities
- Limit container resources
- Use read-only filesystems where possible

## Building

```bash
go mod tidy
CGO_ENABLED=0 go build -o skill-docker .
```

## Running

```bash
# Default port 50074
./skill-docker

# Custom port
SKILL_PORT=50080 ./skill-docker
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SKILL_PORT` | 50074 | gRPC server port |

## Requirements

- Docker must be installed and running on the host
- Docker socket must be accessible (usually `/var/run/docker.sock`)

## License

MIT