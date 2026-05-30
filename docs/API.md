# NiceAgent API

## Public APIs

`POST /api/chats`

Create a chat session.

`GET /api/chats`

List the current user's active chat sessions.

`GET /api/chats/{chat_id}`

Return a chat session and its messages.

`POST /api/chats/{chat_id}/messages`

Create a user message and queue a run.

Request:

```json
{ "content": "Explain the current workspace" }
```

`GET /api/runs/{run_id}`

Return run status.

`GET /api/runs/{run_id}/events`

Subscribe to server-sent events for the run. Use `?after={seq}` to replay missed events.

`POST /api/runs/{run_id}/cancel`

Cancel a run.

`GET /api/skills`

List available skills.

`POST /api/skills/{skill_id}/approve`

Approve a sensitive skill invocation.

## Internal APIs

`POST /internal/runs/execute`

Agent Runtime endpoint for executing a `RunRequest`.

`POST /internal/sandbox/exec`

Sandbox Executor endpoint for running a command under policy.

