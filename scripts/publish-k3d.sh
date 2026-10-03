#!/usr/bin/env bash
# Publish locally built official Skill images to the development cluster's mirror.
set -euo pipefail

cluster="${K3D_CLUSTER:-axiom-dev}"
registry="${K3D_SKILL_REGISTRY:-axiom-skills}"
port="${K3D_SKILL_REGISTRY_PORT:-5111}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
verified_images=()
if [[ ! "$cluster" =~ ^[a-zA-Z0-9-]+$ || ! "$registry" =~ ^[a-zA-Z0-9-]+$ || ! "$port" =~ ^[0-9]+$ || $# -eq 0 ]]; then
    echo "Usage: $0 axiomstudio/skill-<name>:<declared-tag> [...]" >&2
    exit 2
fi
for image in "$@"; do
    if [[ ! "$image" =~ ^axiomstudio/skill-[a-zA-Z0-9-]+:(latest|[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?)$ ]]; then
        echo "Expected an official Skill image with its declared latest or version tag: $image" >&2
        exit 2
    fi
    # Validate the whole batch before publishing or changing node configuration.
    # Keep the inspected image ID even if a mutable tag moves during the probe.
    verified_images+=("$("$script_dir/verify-image.sh" "$image")")
done
mapfile -t nodes < <(docker ps --format '{{.Names}}' | awk -v prefix="k3d-$cluster-" 'index($0,prefix)==1 && ($0 ~ /-server-[0-9]+$/ || $0 ~ /-agent-[0-9]+$/)')
if [[ ${#nodes[@]} -eq 0 ]]; then
    echo "No running nodes found for K3D cluster $cluster" >&2
    exit 1
fi
if ! docker inspect "k3d-$registry" >/dev/null 2>&1; then
    k3d registry create "$registry" --port "127.0.0.1:$port" \
        --default-network "k3d-$cluster" --volume "axiom-skill-registry-$cluster:/var/lib/registry" --no-help
fi
index=0
for image in "$@"; do
    docker tag "${verified_images[$index]}" "localhost:$port/$image"
    docker push "localhost:$port/$image"
    index=$((index + 1))
done
# hosts.toml is reloaded by containerd; no cluster restart is required. Preserve
# an existing mirror configuration rather than silently replacing it.
hosts=$(mktemp)
trap 'rm -f "$hosts"' EXIT
cat >"$hosts" <<EOF
server = "https://registry-1.docker.io"

[host."http://k3d-$registry:5000"]
  capabilities = ["pull", "resolve"]
EOF
for node in "${nodes[@]}"; do
    directory=/var/lib/rancher/k3s/agent/etc/containerd/certs.d/docker.io
    existing=$(docker exec "$node" sh -c "cat $directory/hosts.toml 2>/dev/null || true")
    if [[ -n "$existing" && "$existing" != "$(cat "$hosts")" ]]; then
        echo "Existing registry configuration differs on $node; retain it and add the Skill mirror explicitly." >&2
        exit 1
    fi
    docker exec "$node" mkdir -p "$directory"
    docker cp "$hosts" "$node:$directory/hosts.toml"
    for image in "$@"; do
        docker exec "$node" crictl pull "docker.io/$image"
    done
done
