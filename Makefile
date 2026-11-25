.PHONY: check test run

check:
	go vet ./...
	go test ./...

test:
	go test ./...

run:
	go run ./cmd/volarr serve
