IMAGE_REGISTRY ?= niceagent
IMAGE_TAG ?= local
KIND_CLUSTER ?= niceagent
K8S_NAMESPACE ?= niceagent

.PHONY: run-control run-runtime run-sandbox run-web build-web test smoke-three-services smoke-three-services-ui smoke-three-services-redis smoke-control-plane-fanout smoke-sandbox-container smoke-deepseek-runtime compose-up compose-down compose-config check-js check-scripts check-alerts check-k8s-sandbox docker-build docker-build-control docker-build-runtime docker-build-sandbox kind-create kind-delete kind-load k8s-apply k8s-status k8s-port-forward kind-deploy

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

smoke-three-services:
	python3 scripts/smoke_three_services.py

smoke-three-services-ui:
	python3 scripts/smoke_three_services_ui.py

smoke-three-services-redis:
	python3 scripts/smoke_three_services.py --dispatch-mode redis --start-redis --runtime-count 2 --run-count 2

smoke-control-plane-fanout:
	python3 scripts/smoke_multi_control_plane_fanout.py

smoke-sandbox-container:
	python3 scripts/smoke_sandbox_container.py

smoke-deepseek-runtime:
	python3 scripts/smoke_deepseek_runtime.py

check-js:
	@if [ -d frontend/node_modules ]; then \
		cd frontend && npm run check && npm run build; \
	else \
		echo "未安装 frontend/node_modules，跳过 React/Rspack 构建检查；先运行 cd frontend && npm install。"; \
	fi

check-scripts:
	python3 -m unittest discover -s scripts -p 'test_*.py'

check-alerts:
	python3 scripts/check_prometheus_alerts.py deployments/monitoring/prometheus-alerts.yml
	python3 scripts/check_alertmanager_config.py deployments/monitoring/alertmanager.example.yml
	python3 scripts/check_grafana_dashboards.py deployments/monitoring/grafana/dashboards/skill-governance.json

check-k8s-sandbox:
	python3 scripts/check_k8s_sandbox_hardening.py deployments/k8s/sandbox-hardening.yaml deployments/k8s/sandbox-runtimeclass.example.yaml

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

kind-create:
	kind create cluster --name $(KIND_CLUSTER)

kind-delete:
	kind delete cluster --name $(KIND_CLUSTER)

kind-load:
	kind load docker-image $(IMAGE_REGISTRY)/niceagent-control-plane:$(IMAGE_TAG) --name $(KIND_CLUSTER)
	kind load docker-image $(IMAGE_REGISTRY)/niceagent-agent-runtime:$(IMAGE_TAG) --name $(KIND_CLUSTER)
	kind load docker-image $(IMAGE_REGISTRY)/niceagent-sandbox-executor:$(IMAGE_TAG) --name $(KIND_CLUSTER)

k8s-apply:
	kubectl apply -f deployments/k8s/namespace.yaml
	kubectl apply -f deployments/k8s/configmap.yaml
	kubectl apply -f deployments/k8s/sandbox-hardening.yaml
	kubectl apply -f deployments/k8s/redis.yaml
	kubectl apply -f deployments/k8s/sandbox-executor.yaml
	kubectl apply -f deployments/k8s/agent-runtime.yaml
	kubectl apply -f deployments/k8s/control-plane.yaml
	kubectl -n $(K8S_NAMESPACE) set image deployment/niceagent-control-plane control-plane=$(IMAGE_REGISTRY)/niceagent-control-plane:$(IMAGE_TAG)
	kubectl -n $(K8S_NAMESPACE) set image deployment/niceagent-agent-runtime agent-runtime=$(IMAGE_REGISTRY)/niceagent-agent-runtime:$(IMAGE_TAG)
	kubectl -n $(K8S_NAMESPACE) set image deployment/niceagent-sandbox-executor sandbox-executor=$(IMAGE_REGISTRY)/niceagent-sandbox-executor:$(IMAGE_TAG)
	kubectl -n $(K8S_NAMESPACE) rollout status deployment/niceagent-control-plane --timeout=180s
	kubectl -n $(K8S_NAMESPACE) rollout status deployment/niceagent-agent-runtime --timeout=180s
	kubectl -n $(K8S_NAMESPACE) rollout status deployment/niceagent-sandbox-executor --timeout=180s

k8s-status:
	kubectl -n $(K8S_NAMESPACE) get pods,svc

k8s-port-forward:
	kubectl -n $(K8S_NAMESPACE) port-forward svc/niceagent-control-plane 8080:8080

kind-deploy: docker-build kind-load k8s-apply
