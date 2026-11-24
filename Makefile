.PHONY: check test run up down

check:
	go vet ./...
	go test ./...

test:
	go test ./...

run:
	go run ./cmd/pahe serve

up:
	docker compose -f docker-compose.local.yml up -d --build

down:
	docker compose -f docker-compose.local.yml down
