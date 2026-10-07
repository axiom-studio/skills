#!/usr/bin/env bash
# Build validation functions for skills

run_build_validation() {
    local skill_dir
    local build_count=0

    if [ ! -d "$SKILLS_DIR" ] || [ -z "$(ls -A "$SKILLS_DIR" 2>/dev/null)" ]; then
        log_warn "No skills found in $SKILLS_DIR - skipping build validation"
        SKIPPED=$((SKIPPED + 1))
        TOTAL=$((TOTAL + 1))
        return 0
    fi

    # Shared libraries are not Skills; run their own test suites first.
    for library_dir in "$SKILLS_DIR"/_lib/*/; do
        [ -f "$library_dir/package.json" ] || continue
        local library_name
        library_name="_lib/$(basename "$library_dir")"
        log_info "Testing shared library $library_name..."
        if (cd "$MONOREPO_DIR" && npm --prefix "$library_dir" test 2>&1); then
            log_pass "$library_name tests passed"
            PASSED=$((PASSED + 1))
        else
            log_fail "$library_name tests failed"
            FAILED=$((FAILED + 1))
        fi
        TOTAL=$((TOTAL + 1))
    done

    for skill_dir in "$SKILLS_DIR"/*/; do
        [ -d "$skill_dir" ] || continue

        local skill_name
        skill_name="$(basename "$skill_dir")"
        [ "$skill_name" = "_lib" ] && continue

        if [ ! -f "$skill_dir/main.go" ] && [ ! -f "$skill_dir/pyproject.toml" ] && [ ! -f "$skill_dir/package.json" ]; then
            log_warn "Skipping $skill_name - no supported Go, Python, or Node entrypoint found"
            SKIPPED=$((SKIPPED + 1))
            TOTAL=$((TOTAL + 1))
            continue
        fi

        build_count=$((build_count + 1))
        log_info "Building $skill_name..."

        if [ -f "$skill_dir/main.go" ]; then
            build_command=(go build -mod=mod -buildvcs=false -o /dev/null "./skills/${skill_name}/...")
        elif [ -f "$skill_dir/pyproject.toml" ]; then
            build_command=(python3 -m unittest discover -s "$skill_dir" -p 'test_*.py')
        else
            build_command=(npm --prefix "$skill_dir" test)
        fi

        if (cd "$MONOREPO_DIR" && "${build_command[@]}" 2>&1); then
            log_pass "$skill_name built successfully"
            PASSED=$((PASSED + 1))
        else
            log_fail "$skill_name build failed"
            FAILED=$((FAILED + 1))
        fi
        TOTAL=$((TOTAL + 1))
    done

    if [ "$build_count" -eq 0 ]; then
        log_warn "No buildable skills found - skipping build validation"
        SKIPPED=$((SKIPPED + 1))
        TOTAL=$((TOTAL + 1))
    fi
}
