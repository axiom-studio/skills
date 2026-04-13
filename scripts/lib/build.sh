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

    for skill_dir in "$SKILLS_DIR"/*/; do
        [ -d "$skill_dir" ] || continue

        local skill_name
        skill_name="$(basename "$skill_dir")"

        if [ ! -f "$skill_dir/main.go" ]; then
            log_warn "Skipping $skill_name - no main.go found"
            SKIPPED=$((SKIPPED + 1))
            TOTAL=$((TOTAL + 1))
            continue
        fi

        build_count=$((build_count + 1))
        log_info "Building $skill_name..."

        if (cd "$MONOREPO_DIR" && go build -buildvcs=false -o /dev/null "./skills/${skill_name}/..." 2>&1); then
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
