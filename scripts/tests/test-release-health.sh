#!/usr/bin/env bash
# Release ordering regressions: every Docker, k3d and Go call is isolated/faked.
set -euo pipefail

repository=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
workspace=$(mktemp -d)
trap 'rm -rf "$workspace"' EXIT
fixture="$workspace/repo"
mkdir -p "$fixture/scripts/lib" "$fixture/skills/alpha" "$fixture/skills/beta" "$workspace/bin" "$workspace/state" "$workspace/tmp"
for path in scripts/lib/health.sh scripts/lib/helpers.sh scripts/verify-image.sh scripts/publish-k3d.sh Makefile; do
    [[ -f "$repository/$path" ]] || { echo "Missing release gate: $path" >&2; exit 1; }
    cp -f "$repository/$path" "$fixture/$path"
done
for name in alpha beta; do
    cat >"$fixture/skills/$name/skill.yaml" <<EOF
apiVersion: openseal.dev/v1alpha1
kind: SkillDefinition
definition:
  id: skill-$name
  version: 1.0.0
  name: $name
  source:
    kind: repository
    uri: https://example.test/skills
    reference: $name
    resolvedVersion: 1.0.0
  transport:
    kind: tool
    endpoint: skill-$name
  installers:
    - id: oci
      kind: oci
      package: axiomstudio/skill-$name:latest
  actions: []
EOF
    touch "$fixture/skills/$name/main.go"
done

cat >"$workspace/health-fixture.py" <<'PY'
#!/usr/bin/env python3
import json, os, pathlib, re, sys
args = sys.argv[1:]
with open(os.environ['RELEASE_TEST_LOG'], 'a') as log:
    log.write(json.dumps(['health', *args]) + '\n')
def flag(name):
    try: return args[args.index(name) + 1]
    except (ValueError, IndexError): return ''
manifest = pathlib.Path(flag('--manifest'))
text = manifest.read_text()
identity = re.search(r'^  id: (.+)$', text, re.M).group(1)
version = re.search(r'^  version: (.+)$', text, re.M).group(1)
image = re.search(r'^      package: (.+)$', text, re.M).group(1)
transport = re.search(r'^  transport:\n    kind: (.+)$', text, re.M).group(1)
if image.rsplit(':', 1)[-1] not in ('latest', version):
    sys.exit(1)
if flag('--image') and flag('--image') != image:
    sys.exit(1)
if '--describe' in args:
    print('\t'.join([identity, version, image, transport]))
    sys.exit(0)
mode = os.environ.get('RELEASE_TEST_HEALTH', 'healthy')
if mode in ('mismatch', 'unhealthy', 'crash') or (mode == 'beta-mismatch' and identity == 'skill-beta'):
    print('Health identity mismatch/unhealthy or probe failed', file=sys.stderr)
    sys.exit(1)
if mode == 'manifest-race':
    with open(os.environ['RELEASE_TEST_SOURCE_MANIFEST'], 'a') as source:
        source.write('\n# changed during verification\n')
if mode == 'retag-race':
    pathlib.Path(os.environ['RELEASE_TEST_STATE'], 'retagged').touch()
print('\t'.join(['healthy', identity, version]))
PY
chmod +x "$workspace/health-fixture.py"

cat >"$workspace/bin/tool-fixture" <<'PY'
#!/usr/bin/env python3
import json, os, pathlib, shutil, subprocess, sys
tool, args = pathlib.Path(sys.argv[0]).name, sys.argv[1:]
state = pathlib.Path(os.environ['RELEASE_TEST_STATE'])
with open(os.environ['RELEASE_TEST_LOG'], 'a') as log:
    log.write(json.dumps([tool, *args]) + '\n')
def option(name):
    try: return args[args.index(name) + 1]
    except (ValueError, IndexError): return ''
if tool == 'go':
    if args and args[0] == 'build':
        if os.environ.get('RELEASE_TEST_BUILD') == 'fail': sys.exit(1)
        destination = pathlib.Path(option('-o'))
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(os.environ['RELEASE_TEST_HEALTH_BINARY'], destination)
        destination.chmod(0o755)
        sys.exit(0)
    if args and args[0] == 'env':
        for variable in args[1:]:
            print({'GOOS':'linux', 'GOHOSTOS':'linux', 'GOARCH':'amd64', 'GOHOSTARCH':'amd64'}.get(variable, ''))
        sys.exit(0)
    raise SystemExit('unexpected fake go call: ' + repr(args))
if tool == 'k3d':
    sys.exit(0)
if tool != 'docker':
    raise SystemExit('unexpected fake tool')
if args[:2] == ['image', 'inspect']:
    image = args[-1]
    if os.environ.get('RELEASE_TEST_IMAGE') == 'missing': sys.exit(1)
    image_id = 'sha256:' + ('b' if 'beta' in image else 'a') * 64
    if (state / 'retagged').exists() and 'alpha' in image and not image.startswith('sha256:'):
        image_id = 'sha256:' + 'c' * 64
    if image.startswith('sha256:'): image_id = image
    ports = os.environ.get('RELEASE_TEST_PORTS', 'single')
    exposed = {} if ports == 'missing' else {'50051/tcp': {}}
    if ports == 'ambiguous': exposed['50052/tcp'] = {}
    environment = []
    if os.environ.get('RELEASE_TEST_EXPLICIT_PORT'): environment.append('SKILL_PORT=' + os.environ['RELEASE_TEST_EXPLICIT_PORT'])
    fmt = option('--format') or option('-f')
    if '.Id' in fmt and '.Os' in fmt and '.Architecture' in fmt:
        print(image_id + ' linux amd64')
    elif '.Id' in fmt: print(image_id)
    elif '.Config.Env' in fmt:
        if 'json' in fmt: print(json.dumps(environment))
        else: print('\n'.join(environment))
    elif '.Config.ExposedPorts' in fmt:
        if 'json' in fmt: print(json.dumps(exposed))
        else: print('\n'.join(exposed))
    else: print(json.dumps([{'Id': image_id, 'Os':'linux', 'Architecture':'amd64', 'Config':{'ExposedPorts': exposed, 'Env':environment}}]))
    sys.exit(0)
if args and args[0] == 'run':
    counter = state / 'counter'
    number = int(counter.read_text()) + 1 if counter.exists() else 1
    counter.write_text(str(number))
    container = option('--name') or 'release-probe-' + str(number)
    mounts = {}
    for i, arg in enumerate(args):
        if arg == '--mount':
            values = dict(piece.split('=', 1) for piece in args[i+1].split(',') if '=' in piece)
            mounts[values.get('target', values.get('dst', values.get('destination', '')))] = values.get('source', values.get('src', ''))
    (state / container).write_text(json.dumps(mounts))
    if os.environ.get('RELEASE_TEST_RUN') == 'fail-after-start': sys.exit(1)
    print(container)
    sys.exit(0)
if args and args[0] == 'exec':
    container = args[1]
    if container.startswith(('release-probe-', 'axiom-skill-health-')):
        mounts = json.loads((state / container).read_text())
        translated = [mounts.get(arg, arg) for arg in args[2:]]
        result = subprocess.run(translated)
        sys.exit(result.returncode)
    # Existing node hosts.toml is empty; other calls are merely recorded.
    sys.exit(0)
if args and args[0] == 'rm':
    for arg in args[1:]:
        if arg.startswith(('release-probe-', 'axiom-skill-health-')): (state / arg).unlink(missing_ok=True)
    sys.exit(0)
if args and args[0] == 'ps':
    print('k3d-axiom-dev-server-0')
    sys.exit(0)
if args and args[0] == 'inspect':
    if args[-1].startswith('k3d-axiom-skills'): sys.exit(1)
    print('true')
    sys.exit(0)
if args and args[0] in ('tag', 'push', 'cp', 'logs', 'build'):
    sys.exit(0)
raise SystemExit('unexpected fake docker call: ' + repr(args))
PY
chmod +x "$workspace/bin/tool-fixture"
for tool in docker go k3d; do ln -s tool-fixture "$workspace/bin/$tool"; done

export PATH="$workspace/bin:$PATH"
export TMPDIR="$workspace/tmp"
export RELEASE_TEST_LOG="$workspace/calls.jsonl"
export RELEASE_TEST_STATE="$workspace/state"
export RELEASE_TEST_HEALTH_BINARY="$workspace/health-fixture.py"
export RELEASE_TEST_SOURCE_MANIFEST="$fixture/skills/alpha/skill.yaml"
export K3D_CLUSTER=axiom-dev K3D_SKILL_REGISTRY=axiom-skills K3D_SKILL_REGISTRY_PORT=5111

reset_case() {
    : >"$RELEASE_TEST_LOG"
    rm -f "$workspace/state"/*
    unset RELEASE_TEST_HEALTH RELEASE_TEST_IMAGE RELEASE_TEST_PORTS RELEASE_TEST_EXPLICIT_PORT RELEASE_TEST_BUILD RELEASE_TEST_RUN
}

assert_calls() {
    python3 - "$RELEASE_TEST_LOG" "$1" <<'PY'
import json, os, pathlib, sys
calls = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()]
mode = sys.argv[2]
def mutation(call):
    return call[0] == 'k3d' or (call[0] == 'docker' and len(call)>1 and (call[1] in ('tag','push','cp') or (call[1] == 'exec' and call[2].startswith('k3d-'))))
if mode.startswith('reject'):
    assert not any(mutation(c) for c in calls), f'rejected image caused publication/cluster mutation: {calls}'
    if mode == 'reject-before-run': assert not any(c[:2] == ['docker','run'] for c in calls), calls
if mode in ('reject-cleanup','success','success-batch'):
    started = [c for c in calls if c[:2] == ['docker','run']]
    cleaned = [c for c in calls if c[:2] == ['docker','rm']]
    assert len(started) == len(cleaned) and started, f'probe container leaked: {calls}'
    for number, call in enumerate(started, 1):
        container = call[call.index('--name')+1] if '--name' in call else 'release-probe-'+str(number)
        assert any(container in cleanup for cleanup in cleaned), f'wrong probe container cleaned: {cleaned}'
    for call in started:
        assert '--network' in call and call[call.index('--network')+1] == 'none', call
        assert any(piece.startswith('sha256:') for piece in call), f'probe did not run immutable image: {call}'
        mounts = [call[i+1] for i,arg in enumerate(call) if arg == '--mount']
        assert len(mounts) == 2 and all(',readonly' in value for value in mounts), f'checker/manifest were not mounted readonly: {call}'
assert not list(pathlib.Path(os.environ['TMPDIR']).glob('axiom-skill-health.*')), 'probe workspace leaked'
if mode in ('success','success-batch'):
    probes = [i for i,c in enumerate(calls) if c[0] == 'health' and '--describe' not in c]
    first_mutation = next(i for i,c in enumerate(calls) if mutation(c))
    assert probes and max(probes) < first_mutation, f'publication preceded final Health verification: {calls}'
    tags = [c for c in calls if c[:2] == ['docker','tag']]
    assert tags and all(c[2].startswith('sha256:') for c in tags), f'mutable tags published: {tags}'
    assert tags[0][2] == 'sha256:' + 'a'*64, f'verified image was replaced before publishing: {tags}'
    if mode == 'success-batch': assert len(tags) == 2 and tags[1][2] == 'sha256:'+'b'*64, tags
for call in calls:
    if call[0] == 'health' and '--describe' not in call:
        assert call[call.index('--address')+1].startswith('127.0.0.1:'), f'probe escaped isolated container loopback: {call}'
        assert call[call.index('--timeout')+1] == '30s', f'probe has no bounded deadline: {call}'
PY
}

expect_reject() {
    local assertion=$1
    shift
    if (cd "$fixture" && bash scripts/publish-k3d.sh "$@") >"$workspace/out" 2>"$workspace/err"; then
        echo "Release unexpectedly succeeded: $*" >&2
        cat "$workspace/out" "$workspace/err" >&2
        exit 1
    fi
    assert_calls "$assertion"
}

expect_make_reject() {
    if (cd "$fixture" && make "$1") >"$workspace/out" 2>"$workspace/err"; then
        echo "Make release gate unexpectedly succeeded: $1" >&2
        cat "$workspace/out" "$workspace/err" >&2
        exit 1
    fi
    assert_calls reject-cleanup
}

for failure in mismatch unhealthy crash; do
    reset_case
    export RELEASE_TEST_HEALTH=$failure
    expect_reject reject-cleanup axiomstudio/skill-alpha:latest
done
reset_case
export RELEASE_TEST_HEALTH=beta-mismatch
expect_reject reject-cleanup axiomstudio/skill-alpha:latest axiomstudio/skill-beta:latest

# A sourced validator calls admission inside `if`; Bash suppresses implicit
# errexit there. A failed RPC must still increment failure rather than pass.
reset_case
export RELEASE_TEST_HEALTH=mismatch
if ! (cd "$fixture" && bash -c '
    set -euo pipefail
    source scripts/lib/helpers.sh
    source scripts/lib/health.sh
    SKILLS_DIR="$PWD/skills"
    TOTAL=0 PASSED=0 FAILED=0 SKIPPED=0
    run_health_checks
    [[ "$FAILED" == 2 && "$PASSED" == 0 && "$TOTAL" == 2 ]]
') >"$workspace/out" 2>"$workspace/err"; then
    echo 'Conditional health validation counted rejected images as passed' >&2
    cat "$workspace/out" "$workspace/err" >&2
    exit 1
fi
assert_calls reject-cleanup

reset_case
export RELEASE_TEST_BUILD=fail
expect_reject reject-before-run axiomstudio/skill-alpha:latest

reset_case
export RELEASE_TEST_RUN=fail-after-start
expect_reject reject-cleanup axiomstudio/skill-alpha:latest

reset_case
export RELEASE_TEST_HEALTH=mismatch
expect_make_reject docker-build
reset_case
export RELEASE_TEST_HEALTH=beta-mismatch
expect_make_reject docker-push

reset_case
export RELEASE_TEST_IMAGE=missing
expect_reject reject-before-run axiomstudio/skill-alpha:latest
for ports in missing ambiguous; do
    reset_case
    export RELEASE_TEST_PORTS=$ports
    expect_reject reject-before-run axiomstudio/skill-alpha:latest
done

reset_case
export RELEASE_TEST_HEALTH=manifest-race
expect_reject reject-cleanup axiomstudio/skill-alpha:latest

reset_case
export RELEASE_TEST_HEALTH=retag-race
(cd "$fixture" && bash scripts/publish-k3d.sh axiomstudio/skill-alpha:latest) >"$workspace/out" 2>"$workspace/err"
assert_calls success

reset_case
(cd "$fixture" && bash scripts/publish-k3d.sh axiomstudio/skill-alpha:latest axiomstudio/skill-beta:latest) >"$workspace/out" 2>"$workspace/err"
assert_calls success-batch

reset_case
export RELEASE_TEST_PORTS=ambiguous RELEASE_TEST_EXPLICIT_PORT=50052
(cd "$fixture" && bash scripts/publish-k3d.sh axiomstudio/skill-alpha:latest) >"$workspace/out" 2>"$workspace/err"
assert_calls success
python3 - "$RELEASE_TEST_LOG" <<'PY'
import json, pathlib, sys
calls = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()]
probe = next(c for c in calls if c[0] == 'health' and '--describe' not in c)
assert probe[probe.index('--address')+1] == '127.0.0.1:50052', probe
PY

reset_case
export RELEASE_TEST_HEALTH=retag-race
(cd "$fixture" && make docker-push) >"$workspace/out" 2>"$workspace/err"
assert_calls success-batch

reset_case
python3 - "$fixture/skills/alpha/skill.yaml" <<'PY'
import pathlib, sys
path = pathlib.Path(sys.argv[1])
path.write_text(path.read_text().replace('version: 1.0.0', 'version: 2.3.4').replace('skill-alpha:latest', 'skill-alpha:2.3.4'))
PY
(cd "$fixture" && bash scripts/publish-k3d.sh axiomstudio/skill-alpha:2.3.4) >"$workspace/out" 2>"$workspace/err"
assert_calls success
reset_case
python3 - "$fixture/skills/alpha/skill.yaml" <<'PY'
import pathlib, sys
path = pathlib.Path(sys.argv[1])
path.write_text(path.read_text().replace('skill-alpha:2.3.4', 'skill-alpha:2.3.3'))
PY
expect_reject reject-before-run axiomstudio/skill-alpha:2.3.3
python3 - "$fixture/skills/alpha/skill.yaml" <<'PY'
import pathlib, sys
path = pathlib.Path(sys.argv[1])
path.write_text(path.read_text().replace('version: 2.3.4', 'version: 1.0.0').replace('skill-alpha:2.3.3', 'skill-alpha:latest'))
PY

reset_case
python3 - "$fixture/skills/alpha/skill.yaml" <<'PY'
import pathlib, sys
path = pathlib.Path(sys.argv[1])
path.write_text(path.read_text().replace('    kind: tool\n', '    kind: prompt\n'))
PY
digest=$(cd "$fixture" && bash scripts/verify-image.sh axiomstudio/skill-alpha:latest)
[[ "$digest" == "sha256:$(printf 'a%.0s' {1..64})" ]]
python3 - "$RELEASE_TEST_LOG" <<'PY'
import json, pathlib, sys
calls = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()]
assert not any(c[:2] == ['docker','run'] for c in calls), f'non-tool attempted SDK service Health: {calls}'
PY

echo 'PASS: release Health gate rejects before publishing, cleans probes, and seals verified images'
