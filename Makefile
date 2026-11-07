SHELL := bash
.SHELLFLAGS := -eo pipefail -c

STATICCHECK := $(shell command -v staticcheck 2>/dev/null || echo "$(shell go env GOPATH)/bin/staticcheck")

.PHONY: gate fmt vet lint test tools

## gate: the one command run before every commit
gate: fmt vet lint test

fmt:
	@test -z "$$(gofmt -l .)" || { echo "gofmt: these files need formatting:"; gofmt -l .; exit 1; }

vet:
	go vet ./...

lint: tools
	$(STATICCHECK) ./...

test:
	go test ./...

tools:
	@test -x "$(STATICCHECK)" || go install honnef.co/go/tools/cmd/staticcheck@v0.8.2
