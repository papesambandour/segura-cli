BINARY    = segura
INSTALL   = /usr/local/bin/$(BINARY)
ZSHRC     = $(HOME)/.zshrc
ENV_FILE  = .env
BLOCK_TAG = \# SEGURA-CLI env
DIST      = dist

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE  = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GITHUB_REPO ?= papesambandour/segura-cli

LDFLAGS   = -s -w \
            -X main.Version=$(VERSION) \
            -X main.Commit=$(COMMIT) \
            -X main.BuildDate=$(BUILD_DATE) \
            -X main.GithubRepo=$(GITHUB_REPO)

PLATFORMS = darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

.PHONY: build release release-checksums install uninstall clean deploy deploy-minor deploy-major

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

release: clean
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; \
		arch=$${platform#*/}; \
		output=$(DIST)/$(BINARY)-$${os}-$${arch}; \
		echo "Building $${output}..."; \
		CGO_ENABLED=0 GOOS=$${os} GOARCH=$${arch} go build -ldflags "$(LDFLAGS)" -o $${output} . || exit 1; \
	done
	@echo "Release binaries in $(DIST)/"

release-checksums: release
	@cd $(DIST) && shasum -a 256 $(BINARY)-* > checksums.txt
	@echo "Checksums written to $(DIST)/checksums.txt"

install: build
	@echo "==> Installing $(BINARY) to $(INSTALL)..."
	sudo cp $(BINARY) $(INSTALL)
	sudo chmod +x $(INSTALL)
	@echo "==> Injecting env vars into $(ZSHRC)..."
	@if grep -q "$(BLOCK_TAG)" "$(ZSHRC)" 2>/dev/null; then \
		echo "    Env block already exists in $(ZSHRC), skipping."; \
	else \
		echo "" >> "$(ZSHRC)"; \
		echo "$(BLOCK_TAG)" >> "$(ZSHRC)"; \
		while IFS='=' read -r key value; do \
			case "$$key" in \
				\#*|"") continue ;; \
			esac; \
			echo "export $$key=$$value" >> "$(ZSHRC)"; \
		done < $(ENV_FILE); \
		echo "$(BLOCK_TAG) end" >> "$(ZSHRC)"; \
		echo "    Env vars added to $(ZSHRC). Run: source $(ZSHRC)"; \
	fi
	@echo "==> Done. Run 'source $(ZSHRC)' to load env vars."

uninstall:
	@echo "==> Removing $(INSTALL)..."
	sudo rm -f $(INSTALL)
	@echo "==> Removing env block from $(ZSHRC)..."
	@if grep -q "$(BLOCK_TAG)" "$(ZSHRC)" 2>/dev/null; then \
		sed -i '' '/$(BLOCK_TAG)/,/$(BLOCK_TAG) end/d' "$(ZSHRC)"; \
		echo "    Env block removed from $(ZSHRC)."; \
	else \
		echo "    No env block found in $(ZSHRC)."; \
	fi
	@echo "==> Done."

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)

# ─── Deploy (commit check + push branch + tag + push tag) ────────────────────
# make deploy       → v1.0.8 -> v1.0.9  (patch)
# make deploy-minor → v1.0.9 -> v1.1.0  (minor)
# make deploy-major → v1.1.0 -> v2.0.0  (major)

LATEST_TAG = $(shell git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")

define check_clean
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Error: you have uncommitted changes. Commit or stash them first."; \
		git status --short; \
		exit 1; \
	fi
endef

deploy:
	$(check_clean)
	@MAJOR=$$(echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f1); \
	MINOR=$$(echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f2); \
	PATCH=$$(echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f3); \
	PATCH=$$((PATCH + 1)); \
	NEW_TAG="v$$MAJOR.$$MINOR.$$PATCH"; \
	echo "$(LATEST_TAG) -> $$NEW_TAG"; \
	git push origin release && \
	git tag "$$NEW_TAG" && git push origin "$$NEW_TAG" && \
	echo "Tag $$NEW_TAG pushed. GitHub Actions will create the release."

deploy-minor:
	$(check_clean)
	@MAJOR=$$(echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f1); \
	MINOR=$$(echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f2); \
	MINOR=$$((MINOR + 1)); \
	NEW_TAG="v$$MAJOR.$$MINOR.0"; \
	echo "$(LATEST_TAG) -> $$NEW_TAG"; \
	git push origin release && \
	git tag "$$NEW_TAG" && git push origin "$$NEW_TAG" && \
	echo "Tag $$NEW_TAG pushed. GitHub Actions will create the release."

deploy-major:
	$(check_clean)
	@MAJOR=$$(echo $(LATEST_TAG) | sed 's/v//' | cut -d. -f1); \
	MAJOR=$$((MAJOR + 1)); \
	NEW_TAG="v$$MAJOR.0.0"; \
	echo "$(LATEST_TAG) -> $$NEW_TAG"; \
	git push origin release && \
	git tag "$$NEW_TAG" && git push origin "$$NEW_TAG" && \
	echo "Tag $$NEW_TAG pushed. GitHub Actions will create the release."
