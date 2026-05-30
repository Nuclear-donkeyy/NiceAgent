# NiceAgent

NiceAgent is a scaffold for a web-based remote agent platform. It includes:

- a Go Control Plane with chat, run, skill, and SSE APIs;
- a stateless Agent Runtime package and service entrypoint;
- a Sandbox Executor service entrypoint;
- a static web chat UI served by the Control Plane;
- Postgres migrations and a Redis/Postgres Docker Compose topology.

## Repository Layout

```text
cmd/control-plane      Web/API service and embedded local run dispatcher
cmd/agent-runtime      Stateless runtime service entrypoint
cmd/sandbox-executor   CLI sandbox service entrypoint
internal/protocol      Shared JSON contracts
internal/controlplane  In-memory control plane state and HTTP handlers
internal/runtime       Agentic loop boundary, ready for Eino integration
internal/sandbox       Local command policy executor
web/static             Browser chat UI
migrations             Postgres schema
deployments            Local Docker Compose topology
docs                   Architecture and API notes
```

## Run Locally

With Go installed:

```bash
go run ./cmd/control-plane
```

Then open `http://localhost:8080`.

The in-memory demo supports a basic loop and a guarded CLI skill:

```text
/cli echo hello
```

With Docker:

```bash
docker compose -f deployments/docker-compose.yml up
```

## Next Implementation Steps

1. Replace `internal/controlplane.Store` with a Postgres-backed repository.
2. Replace the embedded local dispatcher with Redis Streams run queue consumption.
3. Integrate CloudWeGo Eino ADK inside `internal/runtime.Engine`.
4. Replace the local sandbox executor with container-backed execution.
5. Add authentication, organization/project membership, quotas, and approvals.

