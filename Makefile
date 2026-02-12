.PHONY: check-lint
check-lint:
	@which golangci-lint > /dev/null || (echo "golangci-lint is not installed. Please install and try again."; exit 1)

.PHONY: check-go
check-go:
	@which go > /dev/null || (echo "Go is not installed.. Please install and try again."; exit 1)

.PHONY: lint
lint: check-lint
	golangci-lint run --config .golangci.yml

.PHONY: unit-test
unit-test: check-go
	go test -coverprofile coverage.out ./... 
