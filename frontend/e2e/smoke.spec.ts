import { expect, test, type Page, type Route } from "@playwright/test";

const now = "2026-05-31T00:00:00Z";

interface ChatSession {
  id: string;
  user_id: string;
  project_id: string;
  title: string;
  archived: boolean;
  last_run_id?: string;
  created_at: string;
  updated_at: string;
  message_count: number;
}

interface Message {
  id: string;
  chat_id: string;
  run_id?: string;
  role: "user" | "assistant";
  content: string;
  created_at: string;
}

interface Skill {
  id: string;
  scope: "system" | "user";
  kind: "builtin" | "http";
  name: string;
  version: string;
  description: string;
  risk: "low" | "medium" | "high";
  requires_auth: boolean;
  enabled: boolean;
}

interface Artifact {
  id: string;
  run_id: string;
  chat_id: string;
  workspace_id: string;
  path: string;
  name: string;
  mime_type: string;
  size_bytes: number;
  created_at: string;
}

test("smoke covers chat, CLI state, artifacts, HTTP Skill and refresh recovery", async ({
  page,
}) => {
  await installMockEventSource(page);
  await installApiMocks(page);

  await page.goto("/");

  await expect(page.getByRole("heading", { name: "远端任务演示" })).toBeVisible();
  await expect(page.getByText("先检查一下工作区")).toBeVisible();
  await expect(page.getByRole("link", { name: "下载 summary.txt" })).toBeVisible();

  await page.getByPlaceholder("发送消息给远端 agent").fill("请用 CLI 获取信息并生成报告");
  await page.getByRole("button", { name: "发送" }).click();

  await expect(page.getByText("请用 CLI 获取信息并生成报告")).toBeVisible();
  await expect(page.getByText("正在通过系统 CLI 获取信息：curl https://example.com")).toBeVisible();
  await expect(page.getByText("连接暂时中断，正在重连")).toBeVisible();
  await expect(page.getByText("连接已恢复，正在补齐事件")).toBeVisible();
  await expect(page.getByRole("link", { name: "下载 report.txt" })).toHaveAttribute(
    "href",
    "/api/artifacts/art-run/download",
  );
  await expect(page.getByText("报告已生成。")).toBeVisible();
  await expect(page.getByText("报告已生成。报告已生成。")).toHaveCount(0);

  await page.getByRole("button", { name: "添加" }).click();
  await page.getByRole("button", { name: "保存 HTTP Skill" }).click();
  await expect(page.getByText("请输入 Skill 名称")).toBeVisible();
  await expect(page.getByText("请输入请求地址")).toBeVisible();
  await expect(page.getByText("请填写调用说明")).toBeVisible();

  await page.getByLabel("Skill 名称").fill("天气 HTTP");
  await page.getByLabel("请求地址").fill("not-a-url");
  await page.getByLabel("调用说明").fill("查询当前城市天气");
  await page.getByRole("button", { name: "保存 HTTP Skill" }).click();
  await expect(page.getByText("请输入有效的 URL")).toBeVisible();

  await page.getByLabel("请求地址").fill("http://api.example.com/weather");
  await page.getByRole("button", { name: "保存 HTTP Skill" }).click();
  await expect(page.getByText("请求地址必须使用 https")).toBeVisible();

  await page.getByLabel("请求地址").fill("https://api.example.com/weather");
  await page.getByRole("button", { name: "保存 HTTP Skill" }).click();
  await expect(page.getByText("服务端返回：上游服务暂不可用")).toBeVisible();

  await page.getByRole("button", { name: "保存 HTTP Skill" }).click();
  const skillCard = page.locator("article").filter({ hasText: "天气 HTTP" });
  await expect(skillCard.getByText("已启用")).toBeVisible();

  await skillCard.getByRole("button", { name: "停用" }).click();
  await expect(skillCard.getByText("已停用")).toBeVisible();
  await skillCard.getByRole("button", { name: "启用" }).click();
  await expect(skillCard.getByText("已启用")).toBeVisible();

  await expect(page.locator("header").getByText("已完成")).toBeVisible();
  await page.reload();

  await expect(page.getByRole("heading", { name: "远端任务演示" })).toBeVisible();
  await expect(page.getByText("报告已生成。")).toBeVisible();
  await expect(page.getByRole("link", { name: "下载 report.txt" })).toBeVisible();
  await expect(page.locator("header").getByText("已完成")).toBeVisible();
});

async function installMockEventSource(page: Page) {
  await page.addInitScript(() => {
    type StreamItem = { data: unknown; delay: number; type: string };

    const streams: Record<string, StreamItem[]> = {
      "/api/runs/run-cli/events": [
        {
          delay: 30,
          type: "run.started",
          data: runEvent(1, "evt-start", "run-cli", "run.started", "Agent Runtime 已开始处理"),
        },
        {
          delay: 120,
          type: "tool.started",
          data: runEvent(2, "evt-tool", "run-cli", "tool.started", "调用系统 CLI", {
            command: ["curl", "https://example.com"],
            skill_id: "system.cli",
          }),
        },
        {
          delay: 760,
          type: "artifact.created",
          data: runEvent(3, "evt-artifact", "run-cli", "artifact.created", "Artifact created.", {
            id: "art-run",
            run_id: "run-cli",
            chat_id: "chat-1",
            workspace_id: "ws-run",
            path: "output/report.txt",
            name: "report.txt",
            mime_type: "text/plain",
            size_bytes: 2048,
            created_at: "2026-05-31T00:00:05Z",
          }),
        },
        {
          delay: 900,
          type: "model.token",
          data: runEvent(4, "evt-token", "run-cli", "model.token", "报告已生成。"),
        },
        {
          delay: 1_100,
          type: "run.succeeded",
          data: runEvent(5, "evt-done", "run-cli", "run.succeeded", "Run succeeded."),
        },
      ],
    };

    class MockEventSource extends EventTarget {
      onerror: ((event: Event) => void) | null = null;
      onopen: ((event: Event) => void) | null = null;
      readyState = 0;
      url: string;
      private openCount = 0;

      constructor(url: string) {
        super();
        this.url = url;
        setTimeout(() => this.open(), 0);
      }

      close() {
        this.readyState = 2;
      }

      private open() {
        if (this.readyState === 2) return;
        this.openCount += 1;
        this.readyState = 1;
        const openEvent = new Event("open");
        this.onopen?.(openEvent);
        this.dispatchEvent(openEvent);
        const items = this.itemsForConnection();
        for (const item of items) {
          setTimeout(() => this.emit(item), item.delay);
        }
        if (this.url === "/api/runs/run-cli/events" && this.openCount === 1) {
          setTimeout(() => this.disconnect(), 220);
        }
      }

      private itemsForConnection(): StreamItem[] {
        const items = streams[this.url] || [];
        if (this.url !== "/api/runs/run-cli/events") return items;
        if (this.openCount === 1)
          return items.filter(
            (item) => item.type === "run.started" || item.type === "tool.started",
          );
        return [
          {
            delay: 20,
            type: "tool.started",
            data: runEvent(2, "evt-tool-duplicate", "run-cli", "tool.started", "重复工具事件", {
              command: ["curl", "https://example.com"],
              skill_id: "system.cli",
            }),
          },
          ...items.filter((item) => item.type !== "run.started" && item.type !== "tool.started"),
        ];
      }

      private disconnect() {
        if (this.readyState === 2) return;
        this.readyState = 0;
        const errorEvent = new Event("error");
        this.onerror?.(errorEvent);
        this.dispatchEvent(errorEvent);
        setTimeout(() => this.open(), 180);
      }

      private emit(item: StreamItem) {
        if (this.readyState === 2) return;
        this.dispatchEvent(new MessageEvent(item.type, { data: JSON.stringify(item.data) }));
      }
    }

    function runEvent(
      seq: number,
      id: string,
      runID: string,
      type: string,
      message: string,
      payload: Record<string, unknown> = {},
    ) {
      return {
        id,
        run_id: runID,
        chat_id: "chat-1",
        seq,
        type,
        message,
        payload,
        created_at: "2026-05-31T00:00:00Z",
      };
    }

    window.EventSource = MockEventSource as unknown as typeof EventSource;
  });
}

async function installApiMocks(page: Page) {
  const summaryArtifact: Artifact = {
    id: "art-restored",
    run_id: "run-restored",
    chat_id: "chat-1",
    workspace_id: "ws-restored",
    path: "output/summary.txt",
    name: "summary.txt",
    mime_type: "text/plain",
    size_bytes: 128,
    created_at: now,
  };
  const reportArtifact: Artifact = {
    id: "art-run",
    run_id: "run-cli",
    chat_id: "chat-1",
    workspace_id: "ws-run",
    path: "output/report.txt",
    name: "report.txt",
    mime_type: "text/plain",
    size_bytes: 2048,
    created_at: "2026-05-31T00:00:05Z",
  };
  const chat: ChatSession = {
    id: "chat-1",
    user_id: "demo-user",
    project_id: "default",
    title: "远端任务演示",
    archived: false,
    last_run_id: "run-restored",
    created_at: now,
    updated_at: now,
    message_count: 2,
  };
  const systemSkill: Skill = {
    id: "system.cli",
    scope: "system",
    kind: "builtin",
    name: "系统 CLI",
    version: "1.0.0",
    description: "执行受策略限制的系统命令",
    risk: "medium",
    requires_auth: false,
    enabled: true,
  };

  let messages: Message[] = [
    {
      id: "msg-1",
      chat_id: "chat-1",
      role: "user",
      content: "先检查一下工作区",
      created_at: now,
    },
    {
      id: "msg-2",
      chat_id: "chat-1",
      run_id: "run-restored",
      role: "assistant",
      content: "已生成初始摘要。",
      created_at: now,
    },
  ];
  let userSkills: Skill[] = [];
  let createSkillAttempts = 0;
  let artifactsByRun: Record<string, Artifact[]> = {
    "run-restored": [summaryArtifact],
    "run-cli": [],
  };

  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();

    if (method === "GET" && path === "/api/chats") {
      await json(route, { chats: [chat] });
      return;
    }

    if (method === "GET" && path === "/api/chats/chat-1") {
      await json(route, { chat, messages });
      return;
    }

    if (method === "POST" && path === "/api/chats/chat-1/messages") {
      const body = parseBody(request.postData());
      const userMessage: Message = {
        id: "msg-user-run",
        chat_id: "chat-1",
        run_id: "run-cli",
        role: "user",
        content: typeof body.content === "string" ? body.content : "",
        created_at: now,
      };
      const assistantMessage: Message = {
        id: "msg-assistant-run",
        chat_id: "chat-1",
        run_id: "run-cli",
        role: "assistant",
        content: "报告已生成。",
        created_at: now,
      };
      messages = [...messages, userMessage, assistantMessage];
      artifactsByRun = { ...artifactsByRun, "run-cli": [reportArtifact] };
      chat.last_run_id = "run-cli";
      chat.message_count = messages.length;
      chat.updated_at = now;
      await json(route, {
        message: userMessage,
        run: runPayload("run-cli", "queued"),
      });
      return;
    }

    if (method === "GET" && path === "/api/runs/run-restored") {
      await json(route, runPayload("run-restored", "succeeded"));
      return;
    }

    if (method === "GET" && path === "/api/runs/run-cli") {
      await json(route, runPayload("run-cli", "succeeded"));
      return;
    }

    if (method === "GET" && path === "/api/runs/run-restored/artifacts") {
      await json(route, { artifacts: artifactsByRun["run-restored"] });
      return;
    }

    if (method === "GET" && path === "/api/runs/run-cli/artifacts") {
      await json(route, { artifacts: artifactsByRun["run-cli"] });
      return;
    }

    if (method === "GET" && path === "/api/skills") {
      await json(route, {
        skills: [systemSkill, ...userSkills],
        groups: { system: [systemSkill], user: userSkills },
      });
      return;
    }

    if (method === "POST" && path === "/api/skills/http") {
      createSkillAttempts += 1;
      if (createSkillAttempts === 1) {
        await json(route, { error: "上游服务暂不可用" }, 503);
        return;
      }
      const skill: Skill = {
        id: "skill-weather",
        scope: "user",
        kind: "http",
        name: "天气 HTTP",
        version: "1.0.0",
        description: "查询当前城市天气",
        risk: "low",
        requires_auth: false,
        enabled: true,
      };
      userSkills = [skill];
      await json(route, skill, 201);
      return;
    }

    if (method === "POST" && path === "/api/skills/skill-weather/disable") {
      userSkills = userSkills.map((skill) => ({ ...skill, enabled: false }));
      await json(route, userSkills[0]);
      return;
    }

    if (method === "POST" && path === "/api/skills/skill-weather/enable") {
      userSkills = userSkills.map((skill) => ({ ...skill, enabled: true }));
      await json(route, userSkills[0]);
      return;
    }

    await json(route, { error: `unhandled mock route ${method} ${path}` }, 500);
  });
}

function parseBody(body: string | null): Record<string, unknown> {
  if (!body) return {};
  return JSON.parse(body) as Record<string, unknown>;
}

function runPayload(id: string, status: "queued" | "succeeded") {
  return {
    id,
    chat_id: "chat-1",
    user_id: "demo-user",
    workspace_id: id === "run-cli" ? "ws-run" : "ws-restored",
    status,
    created_at: now,
    updated_at: now,
  };
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    body: JSON.stringify(body),
    contentType: "application/json",
    status,
  });
}
