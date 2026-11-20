.PHONY: check test run

check:
	go vet ./...
	go test ./...

test:
	go test ./...

run:
	PAHE_CONFIG_DIR="$${PAHE_CONFIG_DIR:-/tmp/pahe}" go run ./cmd/pahe serve
