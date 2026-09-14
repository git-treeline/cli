GOBIN ?= $(shell go env GOPATH)/bin
GOLANGCI_LINT_VERSION := v2.11.4
GOVULNCHECK_VERSION := v1.8.0

.PHONY: test test-e2e vet fmt-check lint vulncheck build install ci

test:
	go test ./...

test-e2e:
	go test -tags e2e ./test/e2e/... -run TestWorktreeLifecycle -v

vet:
	go vet ./...

fmt-check:
	@files="$$(git ls-files '*.go' | xargs gofmt -l)"; \
	if [ -n "$$files" ]; then echo "Run gofmt on:"; echo "$$files"; exit 1; fi

lint:
	@"$(GOBIN)/golangci-lint" version 2>/dev/null | grep -Fq 'version $(patsubst v%,%,$(GOLANGCI_LINT_VERSION)) ' || GOBIN="$(GOBIN)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	"$(GOBIN)/golangci-lint" run ./...

vulncheck:
	@test -x "$(GOBIN)/govulncheck" || GOBIN="$(GOBIN)" go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	"$(GOBIN)/govulncheck" ./...

build:
	go build -o git-treeline .

install: build
	mkdir -p "$(GOBIN)"
	cp git-treeline "$(GOBIN)/git-treeline"
	ln -sf "$(GOBIN)/git-treeline" "$(GOBIN)/gtl"

ci: fmt-check test vet lint vulncheck build
	@echo "\nAll checks passed."
