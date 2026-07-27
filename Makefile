# Axiom Skills Monorepo - Docker Build System
# Builds all skills as Docker images

SHELL := /bin/bash

SKILLS_DIR := skills
REGISTRY := axiomstudio

# Skills that are NOT buildable as Go binaries
# mcp: manifest-only, uses npx/uvx for external MCP servers
# mongodb: has go.mod but no Go source files (incomplete)
SKIP_SKILLS := mcp mongodb

# Discover all buildable skills. The Skill transport is language agnostic;
# every implementation supplies its own Dockerfile when the root Go image is
# not applicable.
GO_SKILL_DIRS := $(dir $(wildcard $(SKILLS_DIR)/*/main.go))
JS_SKILL_DIRS := $(dir $(wildcard $(SKILLS_DIR)/*/package.json))
ALL_SKILL_DIRS := $(filter-out $(addsuffix /,$(addprefix $(SKILLS_DIR)/,$(SKIP_SKILLS))),\
                   $(sort $(GO_SKILL_DIRS) $(JS_SKILL_DIRS)))
SKILL_NAMES := $(notdir $(patsubst %/,%,$(ALL_SKILL_DIRS)))

.PHONY: docker-build docker-push clean help

docker-build: ## Build Docker images for all skills
	@echo "Building $(words $(SKILL_NAMES)) skill images..."
	@echo ""
	@for skill in $(SKILL_NAMES); do \
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
		echo "    ✗ $$skill FAILED"; \
	done
	@echo ""
	@echo "Build complete."

docker-push: ## Push Docker images to registry
	@echo "Pushing images..."
	@echo ""
	@for skill in $(SKILL_NAMES); do \
		image=$$(awk '/^[[:space:]]+installers:/{f=1} f&&/^[[:space:]]+package:/{print $$2; exit}' $(SKILLS_DIR)/$$skill/skill.yaml); \
		echo "  Pushing $$image..."; \
		docker push $$image && \
		echo "    ✓ $$skill" || \
		echo "    ✗ $$skill FAILED"; \
	done
	@echo ""
	@echo "Push complete."

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
