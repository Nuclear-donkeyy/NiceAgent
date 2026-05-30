.PHONY: run-control run-runtime run-sandbox test compose-up compose-down

run-control:
	go run ./cmd/control-plane

run-runtime:
	go run ./cmd/agent-runtime

run-sandbox:
	go run ./cmd/sandbox-executor

test:
	go test ./...

compose-up:
	docker compose -f deployments/docker-compose.yml up

compose-down:
	docker compose -f deployments/docker-compose.yml down

