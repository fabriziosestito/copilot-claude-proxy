GOLANGCI_LINT_VERSION := v2.12.2

.PHONY: all build test fmt vet lint clean

all: build test lint

build:
	go build -o bin/copilot-claude-proxy .

test: vet
	go test -race ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: bin/golangci-lint
	bin/golangci-lint run

bin/golangci-lint:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
		| sh -s -- -b bin $(GOLANGCI_LINT_VERSION)

clean:
	rm -rf bin
