.PHONY: run-control run-runtime run-sandbox test compose-up compose-down compose-config check-js

run-control:
	go run ./cmd/control-plane

run-runtime:
	go run ./cmd/agent-runtime

run-sandbox:
	go run ./cmd/sandbox-executor

test:
	go test ./...

check-js:
	@files=$$(find web -type f -name '*.js'); \
	if [ -z "$$files" ]; then \
		echo "未找到 JS 文件，跳过语法检查。"; \
	else \
		for file in $$files; do node --check "$$file"; done; \
	fi

compose-up:
	docker compose -f deployments/docker-compose.yml up

compose-down:
	docker compose -f deployments/docker-compose.yml down

compose-config:
	docker compose -f deployments/docker-compose.yml config
