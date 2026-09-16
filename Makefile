.PHONY: test test-race vet postgres-up postgres-down migrate run

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

postgres-up:
	docker compose up -d postgres

postgres-down:
	docker compose down

migrate:
	go run ./cmd/contexta migrate

run:
	go run ./cmd/contexta serve
