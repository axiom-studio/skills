#!/usr/bin/env bash
# Verify the exact release image through SDK Health, without credentials,
# provider actions, or external network access.

verify_skill_image() (
  set -euo pipefail
  local image="$1" supplied_manifest="${2:-}" root workspace container=""
  local image_id image_os image_arch host_os host_arch checker metadata manifest="" count=0 image_metadata environment exposures
  local identity version declared_image transport port candidate
  local -a configured_ports exposed_ports
  root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  if [[ ! "$image" =~ ^axiomstudio/skill-[a-zA-Z0-9-]+:(latest|[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?)$ ]]; then
    echo "Unsupported official Skill image: $image" >&2
    return 1
  fi
  image_metadata="$(docker image inspect --format '{{.Id}} {{.Os}} {{.Architecture}}' "$image")" || return 1
  read -r image_id image_os image_arch <<< "$image_metadata" || return 1
  if [[ ! "$image_id" =~ ^sha256:[0-9a-f]{64}$ || "$image_os" != linux || ! "$image_arch" =~ ^(amd64|arm64)$ ]]; then
    echo "Skill release requires an inspected Linux amd64/arm64 image: $image" >&2
    return 1
  fi
  workspace="$(mktemp -d "${TMPDIR:-/tmp}/axiom-skill-health.XXXXXX")" || return 1
  cleanup_skill_health() {
    if [[ -n "$container" ]]; then docker rm --force "$container" >/dev/null 2>&1 || true; fi
    rm -rf "$workspace"
  }
  trap cleanup_skill_health EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  host_os="$(go env GOHOSTOS)" || return 1
  host_arch="$(go env GOHOSTARCH)" || return 1
  checker="$workspace/skill-healthcheck-host"
  (cd "$root" && CGO_ENABLED=0 GOOS="$host_os" GOARCH="$host_arch" go build -mod=vendor -buildvcs=false -trimpath -o "$checker" ./internal/cmd/skill-healthcheck) || return 1
  if [[ -n "$supplied_manifest" ]]; then
    manifest="$supplied_manifest"
  else
    for candidate in "$root"/skills/*/skill.yaml; do
      [[ -f "$candidate" ]] || continue
      metadata="$("$checker" --manifest "$candidate" --describe)" || return 1
      IFS=$'\t' read -r identity version declared_image transport <<< "$metadata"
      if [[ "$declared_image" == "$image" ]]; then manifest="$candidate"; count=$((count + 1)); fi
    done
    if [[ "$count" != 1 ]]; then
      echo "Expected exactly one canonical manifest declaring $image; found $count" >&2
      return 1
    fi
  fi
  # Metadata and the runtime probe read one frozen canonical declaration.
  cp "$manifest" "$workspace/skill.yaml" || return 1
  metadata="$("$checker" --manifest "$workspace/skill.yaml" --image "$image" --describe)" || return 1
  IFS=$'\t' read -r identity version declared_image transport <<< "$metadata"
  if [[ "${image##*:}" != latest && "${image##*:}" != "$version" ]]; then
    echo "Versioned image tag must equal the canonical definition version: $image ($version)" >&2
    return 1
  fi
  if [[ "$transport" != tool ]]; then
    echo "SDK Health is not applicable to declared $transport transport: $image" >&2
    cmp -s "$manifest" "$workspace/skill.yaml" || return 1
    printf '%s\n' "$image_id"
    return 0
  fi
  environment="$(docker image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image_id")" || return 1
  mapfile -t configured_ports < <(printf '%s\n' "$environment" | awk -F= '$1 == "SKILL_PORT" {print substr($0, 12)}')
  if [[ ${#configured_ports[@]} -eq 1 ]]; then
    port="${configured_ports[0]}"
  elif [[ ${#configured_ports[@]} -eq 0 ]]; then
    exposures="$(docker image inspect --format '{{range $port, $_ := .Config.ExposedPorts}}{{println $port}}{{end}}' "$image_id")" || return 1
    mapfile -t exposed_ports < <(printf '%s\n' "$exposures" | awk '/^[0-9]+\/tcp$/ {sub(/\/tcp$/, ""); print}')
    if [[ ${#exposed_ports[@]} -ne 1 ]]; then
      echo "Cannot identify the gRPC port for $image; declare SKILL_PORT or expose exactly one TCP port" >&2
      return 1
    fi
    port="${exposed_ports[0]}"
  else
    echo "Ambiguous SKILL_PORT configuration for $image" >&2
    return 1
  fi
  if [[ ! "$port" =~ ^[1-9][0-9]{0,4}$ ]] || ((port > 65535)); then
    echo "Invalid gRPC port for $image: $port" >&2
    return 1
  fi
  if [[ "$host_os" != linux || "$host_arch" != "$image_arch" ]]; then
    checker="$workspace/skill-healthcheck-linux"
    (cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$image_arch" go build -mod=vendor -buildvcs=false -trimpath -o "$checker" ./internal/cmd/skill-healthcheck) || return 1
  fi
  chmod 755 "$checker" || return 1
  chmod 644 "$workspace/skill.yaml" || return 1
  echo "Verifying $image ($image_id), expected $identity $version..." >&2
  # Know the cleanup identity even if Docker starts the container but loses its
  # response. Never depend on a successful run response to find the container.
  container="axiom-skill-health-$(basename "$workspace")"
  docker run --detach --name "$container" --network none \
    --mount "type=bind,source=$checker,target=/axiom-skill-healthcheck,readonly" \
    --mount "type=bind,source=$workspace/skill.yaml,target=/axiom-skill-health-manifest.yaml,readonly" \
    "$image_id" >/dev/null || return 1
  docker exec "$container" /axiom-skill-healthcheck \
    --manifest /axiom-skill-health-manifest.yaml --image "$image" \
    --address "127.0.0.1:$port" --timeout 30s >&2 || return 1
  if ! cmp -s "$manifest" "$workspace/skill.yaml"; then
    echo "Canonical manifest changed during image validation: $manifest" >&2
    return 1
  fi
  printf '%s\n' "$image_id"
)

run_health_checks() {
  local skill_dir skill_name image health_count=0
  if [[ ! -d "$SKILLS_DIR" ]]; then
    log_warn "Skills directory not found: $SKILLS_DIR"
    SKIPPED=$((SKIPPED + 1)); TOTAL=$((TOTAL + 1))
    return 0
  fi
  for skill_dir in "$SKILLS_DIR"/*/; do
    [[ -f "$skill_dir/main.go" || -f "$skill_dir/pyproject.toml" || -f "$skill_dir/package.json" ]] || continue
    skill_name="$(basename "$skill_dir")"
    image="$(awk '/^[[:space:]]+installers:/{f=1} f&&/^[[:space:]]+package:/{print $2; exit}' "$skill_dir/skill.yaml")"
    health_count=$((health_count + 1)); TOTAL=$((TOTAL + 1))
    if verify_skill_image "$image" "$skill_dir/skill.yaml" >/dev/null; then
      log_pass "Image release validation passed: $skill_name"
      PASSED=$((PASSED + 1))
    else
      log_fail "Image Health validation failed: $skill_name (build its declared image before validation)"
      FAILED=$((FAILED + 1))
    fi
  done
  if [[ "$health_count" == 0 ]]; then
    log_warn "No executable Skill services found"
    SKIPPED=$((SKIPPED + 1)); TOTAL=$((TOTAL + 1))
  fi
}
