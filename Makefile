# gofakes3 Makefile
#
# Common targets for local development and release builds.
# Advanced cover/docker/release helpers also live in makefile.go.

PKG            := ./...
CMD            := ./cmd/gofakes3
BIN            := gofakes3
BUILD_DIR      := build
COVERAGE_FILE  := coverage.txt
DOCKER_IMAGE   := johannesboyne/gofakes3
GO             ?= go
GOFLAGS        ?=
LDFLAGS        ?=

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build cmd/gofakes3 into ./build/gofakes3
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BIN) $(CMD)

.PHONY: install
install: ## Install cmd/gofakes3 to $$GOPATH/bin
	$(GO) install $(GOFLAGS) -ldflags '$(LDFLAGS)' $(CMD)

.PHONY: run
run: ## Run the in-memory S3 server (default port 9000)
	$(GO) run $(CMD) -backend mem

.PHONY: test
test: ## Run unit tests
	$(GO) test $(GOFLAGS) $(PKG)

.PHONY: test-race
test-race: ## Run unit tests with the race detector (matches CI)
	$(GO) test $(GOFLAGS) $(PKG) -race -count=1

.PHONY: cover
cover: ## Generate coverage profile (coverage.txt) via makefile.go
	$(GO) run makefile.go cover $(COVERAGE_FILE)
	@echo "coverage written to $(COVERAGE_FILE)"

.PHONY: cover-html
cover-html: cover ## Generate coverage.txt and open an HTML report
	$(GO) tool cover -html=$(COVERAGE_FILE)

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(PKG)

.PHONY: fmt
fmt: ## Format all Go sources with gofmt
	find . -name '*.go' -not -path './s3test/*' -print0 | xargs -0 gofmt -w

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-clean
	@test -z "$$(find . -name '*.go' -not -path './s3test/*' -print0 | xargs -0 gofmt -l | tee /dev/stderr)"

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

.PHONY: check
check: fmt-check vet test-race ## Format check + vet + race tests

.PHONY: docker
docker: ## Build the Docker image (via makefile.go)
	$(GO) run makefile.go builddocker

.PHONY: docker-run
docker-run: ## Run the published Docker image locally on :9000
	docker run --rm -p 9000:9000 $(DOCKER_IMAGE)

.PHONY: release-build
release-build: ## Cross-compile release binaries into ./build (via makefile.go)
	$(GO) run makefile.go buildrelease

.PHONY: release
release: ## Tag a release: make release VERSION=v0.0.0
	@test -n "$(VERSION)" || (echo "VERSION is required, e.g. make release VERSION=v0.0.0" >&2; exit 1)
	$(GO) run makefile.go release $(VERSION)

.PHONY: clean
clean: ## Remove build artifacts and coverage output
	rm -rf $(BUILD_DIR) $(COVERAGE_FILE) *.out
