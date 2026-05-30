IMAGE_REGISTRY ?= niceagent
IMAGE_TAG ?= local

.PHONY: run-control run-runtime run-sandbox run-web build-web test compose-up compose-down compose-config check-js docker-build docker-build-control docker-build-runtime docker-build-sandbox

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

docker-build: docker-build-control docker-build-runtime docker-build-sandbox

docker-build-control:
	docker build -f build/docker/control-plane.Dockerfile -t $(IMAGE_REGISTRY)/niceagent-control-plane:$(IMAGE_TAG) .

docker-build-runtime:
	docker build -f build/docker/agent-runtime.Dockerfile -t $(IMAGE_REGISTRY)/niceagent-agent-runtime:$(IMAGE_TAG) .

docker-build-sandbox:
	docker build -f build/docker/sandbox-executor.Dockerfile -t $(IMAGE_REGISTRY)/niceagent-sandbox-executor:$(IMAGE_TAG) .
