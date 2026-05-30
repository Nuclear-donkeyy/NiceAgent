# NiceAgent Architecture

NiceAgent is split into a control plane and stateless execution workers.

## Control Plane

The control plane owns user-facing state: users, projects, chat sessions, messages, runs, events, skills, workspaces, approvals, quotas, and audit data. It exposes REST APIs for the web client and an SSE event stream for each run.

The current implementation uses an in-memory store so the full loop can run without external dependencies. The migration in `migrations/001_init.sql` defines the Postgres contract that should replace the in-memory store.

## Agent Runtime

The runtime is intentionally stateless. It accepts a `RunRequest`, fetches or receives context, executes the agentic loop, invokes skills, and emits normalized `RunEvent` records. The runtime package is designed as the integration point for CloudWeGo Eino ADK:

- map NiceAgent `Skill` records into Eino tools;
- wrap provider adapters as Eino chat models;
- use Eino Runner/TurnLoop or Graph/Workflow for the loop;
- emit events at every model/tool/checkpoint boundary.

## Sandbox Executor

The sandbox executor is the execution boundary for CLI and filesystem skills. The local implementation only allows a small command allowlist. Production should replace it with container or Kubernetes job execution, including CPU, memory, disk, network, timeout, workspace mount, and audit policy controls.

## Event Flow

1. Web client posts a user message.
2. Control Plane stores the message and creates a queued run.
3. Runtime executes the run and emits events.
4. Control Plane persists events and fans them out over SSE.
5. Final assistant message is stored in the chat session.

## Production Adapters

The current scaffold keeps external services behind clear seams:

- Postgres store adapter for authoritative state.
- Redis Streams queue for run dispatch and event fanout.
- Eino runtime adapter for agentic loop orchestration.
- Model provider adapters for OpenAI, Anthropic, and OpenAI-compatible endpoints.
- Container sandbox adapter for CLI execution.

