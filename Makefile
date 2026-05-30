.PHONY: run-control run-runtime run-sandbox run-web build-web test compose-up compose-down compose-config check-js

run-control:
	cd services/control-plane && go run ./cmd

run-runtime:
	cd services/agent-runtime && go run ./cmd

run-sandbox:
	cd services/sandbox-executor && go run ./cmd

run-web:
	cd frontend && npm run dev

build-web:
	cd frontend && npm run build

test:
	go test ./packages/common/... ./services/control-plane/... ./services/agent-runtime/... ./services/sandbox-executor/...

check-js:
	node --check frontend/rspack.config.cjs
	@if [ -d frontend/node_modules ]; then \
		cd frontend && npm run build; \
	else \
		echo "未安装 frontend/node_modules，跳过 React/Rspack 构建检查；先运行 cd frontend && npm install。"; \
	fi

compose-up:
	docker compose -f deployments/docker-compose.yml up

compose-down:
	docker compose -f deployments/docker-compose.yml down

compose-config:
	docker compose -f deployments/docker-compose.yml config
