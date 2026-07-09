GOBIN ?= $(shell go env GOPATH)/bin

.PHONY: test test-e2e vet lint vulncheck build ci

test:
	go test ./...

test-e2e:
	go test -tags e2e ./test/e2e/... -run TestWorktreeLifecycle -v

vet:
	go vet ./...

lint:
	@test -x $(GOBIN)/golangci-lint || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	$(GOBIN)/golangci-lint run ./...

vulncheck:
	@test -x $(GOBIN)/govulncheck || go install golang.org/x/vuln/cmd/govulncheck@latest
	$(GOBIN)/govulncheck ./...

build:
	go build .

install: build
	cp git-treeline $(GOBIN)/git-treeline
	ln -sf $(GOBIN)/git-treeline $(GOBIN)/gtl

ci: test vet lint vulncheck build
	@echo "\nAll checks passed."
