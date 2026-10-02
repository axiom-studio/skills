# Axiom Skills Monorepo - Docker Build System
# Builds all skills as Docker images

SHELL := /bin/bash

SKILLS_DIR := skills
REGISTRY := axiomstudio

# Skills that are NOT buildable as Go binaries
# mongodb: has go.mod but no Go source files (incomplete)
SKIP_SKILLS := mongodb

# Discover buildable Go, Python, and Node Skill services.
ALL_SKILL_DIRS := $(filter-out $(addprefix $(SKILLS_DIR)/,$(SKIP_SKILLS)),\
                   $(sort $(dir $(wildcard $(SKILLS_DIR)/*/main.go) $(wildcard $(SKILLS_DIR)/*/pyproject.toml) $(wildcard $(SKILLS_DIR)/*/package.json))))
SKILL_NAMES := $(notdir $(patsubst %/,%,$(ALL_SKILL_DIRS)))

.PHONY: docker-build docker-push clean help

docker-build: ## Build Docker images for all skills
	@echo "Building $(words $(SKILL_NAMES)) skill images..."
	@echo ""
	@failed=0; for skill in $(SKILL_NAMES); do \
		port=50051; \
		image=$$(awk '/^[[:space:]]+installers:/{f=1} f&&/^[[:space:]]+package:/{print $$2; exit}' $(SKILLS_DIR)/$$skill/skill.yaml); \
		echo "  Building $$image..."; \
		dockerfile="$(SKILLS_DIR)/$$skill/Dockerfile"; \
		[ -f "$$dockerfile" ] || dockerfile="Dockerfile"; \
		docker build -f $$dockerfile \
			--build-arg SKILL_NAME=$$skill \
			--build-arg SKILL_PORT=$$port \
			-t $$image \
			. && \
		echo "    ✓ $$skill" || \
		{ echo "    ✗ $$skill FAILED"; failed=1; }; \
	done; \
	echo ""; \
	echo "Build complete."; \
	test "$$failed" = 0

docker-push: ## Push Docker images to registry
	@echo "Pushing images..."
	@echo ""
	@failed=0; for skill in $(SKILL_NAMES); do \
		image=$$(awk '/^[[:space:]]+installers:/{f=1} f&&/^[[:space:]]+package:/{print $$2; exit}' $(SKILLS_DIR)/$$skill/skill.yaml); \
		echo "  Pushing $$image..."; \
		docker push $$image && \
		echo "    ✓ $$skill" || \
		{ echo "    ✗ $$skill FAILED"; failed=1; }; \
	done; \
	echo ""; \
	echo "Push complete."; \
	test "$$failed" = 0

clean: ## Remove built binaries and dangling images
	@echo "Cleaning..."
	@for dir in $(SKILLS_DIR)/*/; do \
		rm -f "$$dir"/skill-*-linux-*; \
	done
	@docker image prune -f >/dev/null 2>&1 || true
	@echo "Clean complete."

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
