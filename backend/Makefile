.PHONY: fmt check test test-e2e test-e2e-live build

fmt:
	gofmt -w cmd internal e2e

check:
	test -z "$$(gofmt -l cmd internal e2e)"
	go vet -tags=e2e ./...

test:
	go test -race -shuffle=on -count=1 -timeout=5m ./...

test-e2e:
	go test -tags=e2e -race -shuffle=on -count=1 -timeout=5m -v ./e2e

test-e2e-live:
	LIQUOR_E2E_LIVE=1 go test -tags=e2e -race -shuffle=on -count=1 -timeout=5m -v ./e2e

build:
	go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server
