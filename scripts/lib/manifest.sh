#!/usr/bin/env bash
# Manifest validation functions for skills

run_manifest_validation() {
    local manifest_file
    local validation_count=0

    if [ ! -d "$SKILLS_DIR" ] || [ -z "$(ls -A "$SKILLS_DIR" 2>/dev/null)" ]; then
        log_warn "No skills found in $SKILLS_DIR - skipping manifest validation"
        SKIPPED=$((SKIPPED + 1))
        TOTAL=$((TOTAL + 1))
        return 0
    fi

    for skill_dir in "$SKILLS_DIR"/*/; do
        [ -d "$skill_dir" ] || continue

        local skill_name
        skill_name="$(basename "$skill_dir")"
        manifest_file="$skill_dir/skill.yaml"

        if [ ! -f "$manifest_file" ]; then
            log_warn "Skipping $skill_name - no skill.yaml found"
            SKIPPED=$((SKIPPED + 1))
            TOTAL=$((TOTAL + 1))
            continue
        fi

        validation_count=$((validation_count + 1))
        log_info "Validating manifest for $skill_name..."

        local valid=true

        # The repository authors only canonical OpenSeal SkillDefinitions.
        # Full semantic validation is available through:
        #   openseal skill validate <skill.yaml>
        for field in \
            "^apiVersion: openseal.dev/v1alpha1$" \
            "^kind: SkillDefinition$" \
            "^definition:$" \
            "^[[:space:]]\\+id:" \
            "^[[:space:]]\\+version:" \
            "^[[:space:]]\\+name:" \
            "^[[:space:]]\\+actions:" \
            "^[[:space:]]\\+transport:" \
            "^[[:space:]]\\+installers:"; do
            if ! grep -q "$field" "$manifest_file" 2>/dev/null; then
                log_fail "$skill_name: missing required field '$field'"
                valid=false
            fi
        done

        if ! grep -q "^[[:space:]]\\+kind: oci$" "$manifest_file" 2>/dev/null ||
           ! grep -q "^[[:space:]]\\+package:" "$manifest_file" 2>/dev/null; then
            log_fail "$skill_name: executable Skill must declare one OCI installer package"
            valid=false
        fi

        if [ "$valid" = true ]; then
            log_pass "$skill_name manifest valid"
            PASSED=$((PASSED + 1))
        else
            FAILED=$((FAILED + 1))
        fi
        TOTAL=$((TOTAL + 1))
    done

    if [ "$validation_count" -eq 0 ]; then
        log_warn "No manifests found - skipping manifest validation"
        SKIPPED=$((SKIPPED + 1))
        TOTAL=$((TOTAL + 1))
    fi
}
