# Axiom Skills Monorepo - Build System
# Builds all skills as Linux amd64 static binaries

SHELL := /bin/bash

SKILLS_DIR := skills

# Skills that are NOT buildable as Go binaries
# mcp: manifest-only, uses npx/uvx for external MCP servers
# mongodb: has go.mod but no Go source files (incomplete)
SKIP_SKILLS := mcp mongodb

# Discover all buildable skills (those with main.go, excluding skipped)
ALL_SKILL_DIRS := $(filter-out $(addprefix $(SKILLS_DIR)/,$(SKIP_SKILLS)),\
                   $(sort $(dir $(wildcard $(SKILLS_DIR)/*/main.go))))
SKILL_NAMES := $(notdir $(patsubst %/,%,$(ALL_SKILL_DIRS)))

# Build flags
CGO_ENABLED := 0
GOOS := linux
GOARCH := amd64
LDFLAGS := -s -w

.PHONY: build clean help

build: ## Build all skills as Linux amd64 binaries
	@echo "Building $(words $(SKILL_NAMES)) skills for linux-amd64..."
	@echo ""
	@for skill in $(SKILL_NAMES); do \
		echo "  Building $$skill..."; \
		CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
			go build -ldflags="$(LDFLAGS)" -buildvcs=false \
			-o $(SKILLS_DIR)/$$skill/skill-$$skill-linux-amd64 \
			./$(SKILLS_DIR)/$$skill/... && \
			echo "    ✓ $$skill" || \
			echo "    ✗ $$skill FAILED"; \
	done
	@echo ""
	@echo "Build complete."

clean: ## Remove all built binaries
	@echo "Cleaning built binaries..."
	@for dir in $(SKILLS_DIR)/*/; do \
		rm -f "$$dir"/skill-*-linux-amd64; \
	done
	@echo "Clean complete."

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
