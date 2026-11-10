SHELL := bash
.SHELLFLAGS := -eo pipefail -c

STATICCHECK := go run honnef.co/go/tools/cmd/staticcheck@v0.8.2

.PHONY: gate fmt vet lint test

## gate: the one command run before every commit
gate: fmt vet lint test

fmt:
	@test -z "$$(gofmt -l .)" || { echo "gofmt: these files need formatting:"; gofmt -l .; exit 1; }

vet:
	go vet ./...
	GOOS=darwin go vet ./...

lint:
	$(STATICCHECK) ./...

test:
	go test ./...
