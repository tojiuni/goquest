# goquest-mcp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `goquest-mcp`, a TypeScript MCP server that proxies the goquest ticket REST/OpenAPI contract (Phase 1) so LLMs create/manage tickets by name, discover the API, and follow the ticket-authoring guide.

**Architecture:** Stateless proxy. Testable core split into `client.ts` (HTTP + error mapping), `tools.ts` (tool definitions: `{name, description, inputSchema, handler(args, client)}`), `guide.ts` (loads embedded guide). `goquest-mcp-server.ts` registers tools + a resource on an `McpServer` and wires stdio/HTTP transport. goquest is the single source of truth.

**Tech Stack:** TypeScript, `@modelcontextprotocol/sdk` ^1.10, `zod`, `tsx`, Node 20+, `node:test` for unit tests. Mirrors `/workspace/repos/gopedia_mcp`.

## Global Constraints

- Language: TypeScript run via `tsx`; Node 20+. Typecheck with `npx tsc --noEmit`.
- MCP SDK: `@modelcontextprotocol/sdk` ^1.10.2 (`McpServer` + `server.registerTool` + `server.registerResource`).
- Every goquest call sends header `X-API-Key: <GOQUEST_API_KEY>`.
- Env: `GOQUEST_API_URL` (default `http://goquest-service.goquest.svc.cluster.local:8080`), `GOQUEST_API_KEY` (required), `MCP_HTTP_PORT` (optional → StreamableHTTP; else stdio).
- goquest REST paths are under `/api/v1`; `GOQUEST_API_URL` is the host root.
- Tool schemas hand-written. Name-based args (`workspace`/`project`/`tag_names`) preferred; UUIDs never required for create/list/by-name.
- 8 tools total: `create_ticket`, `get_ticket`, `get_ticket_by_name`, `list_tickets`, `update_ticket`, `comment`, `list_api`, `ticket_guide`.
- `ticket-guide.md` in the repo is a synced copy; the geneso doc `goquest/guide/ticket-authoring-guide.md` is the source of truth.
- Repo location: new repo at `/workspace/repos/goquest-mcp` (own git repo, not a goquest worktree).

---

## Task 1: Repo scaffold + config

**Files:**
- Create: `/workspace/repos/goquest-mcp/package.json`
- Create: `/workspace/repos/goquest-mcp/tsconfig.json`
- Create: `/workspace/repos/goquest-mcp/.gitignore`
- Create: `/workspace/repos/goquest-mcp/src/version.ts`

**Interfaces:**
- Produces: `SERVER_NAME` (`"goquest"`) and `SERVER_VERSION` (`"0.1.0"`) from `src/version.ts`.

- [ ] **Step 1: Create the repo directory and init git**

Run:
```bash
mkdir -p /workspace/repos/goquest-mcp/src /workspace/repos/goquest-mcp/scripts /workspace/repos/goquest-mcp/deploy
cd /workspace/repos/goquest-mcp && git init -q
```

- [ ] **Step 2: Write `package.json`**

```json
{
  "name": "goquest-mcp-server",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "scripts": {
    "start": "tsx src/goquest-mcp-server.ts",
    "typecheck": "tsc --noEmit",
    "test": "node --import tsx --test src/*.test.ts",
    "smoke": "tsx scripts/mcp-smoke-test.ts"
  },
  "dependencies": {
    "@modelcontextprotocol/sdk": "^1.10.2",
    "dotenv": "^17.3.1",
    "zod": "^3.23.8"
  },
  "devDependencies": {
    "@types/node": "^20.14.0",
    "tsx": "^4.19.0",
    "typescript": "^5.6.0"
  }
}
```

- [ ] **Step 3: Write `tsconfig.json`**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "allowImportingTsExtensions": true,
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "resolveJsonModule": true,
    "noEmit": true,
    "types": ["node"]
  },
  "include": ["src", "scripts"]
}
```

- [ ] **Step 4: Write `.gitignore`**

```
node_modules/
*.log
.env
```

- [ ] **Step 5: Write `src/version.ts`**

```ts
export const SERVER_NAME = "goquest";
export const SERVER_VERSION = "0.1.0";
```

- [ ] **Step 6: Install and typecheck**

Run:
```bash
cd /workspace/repos/goquest-mcp && npm install && npm run typecheck
```
Expected: install succeeds; `tsc --noEmit` prints nothing (exit 0).

- [ ] **Step 7: Commit**

```bash
cd /workspace/repos/goquest-mcp
git add -A && git commit -q -m "chore: scaffold goquest-mcp (package.json, tsconfig, version)"
```

---

## Task 2: goquest HTTP client (`client.ts`)

**Files:**
- Create: `/workspace/repos/goquest-mcp/src/client.ts`
- Test: `/workspace/repos/goquest-mcp/src/client.test.ts`

**Interfaces:**
- Consumes: none.
- Produces:
  - `type GqResult = { ok: boolean; status: number; data?: unknown; error?: string; replayed?: boolean }`
  - `interface GoquestClient { request(method: string, path: string, body?: unknown): Promise<GqResult> }`
  - `function createClient(opts: { baseUrl: string; apiKey: string; fetchImpl?: typeof fetch }): GoquestClient`
  - `function mapError(status: number, bodyText: string): string` — human hint for a non-2xx status.

- [ ] **Step 1: Write the failing test**

`src/client.test.ts`:
```ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { createClient, mapError } from "./client.ts";

function fakeFetch(status: number, body: unknown, headers: Record<string, string> = {}) {
  return async (_url: string | URL, _init?: RequestInit) =>
    new Response(typeof body === "string" ? body : JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json", ...headers },
    }) as unknown as Response;
}

test("request sends X-API-Key and returns parsed data on 200", async () => {
  let seenAuth = "";
  const fetchImpl = (async (_u: string | URL, init?: RequestInit) => {
    seenAuth = new Headers(init?.headers).get("X-API-Key") ?? "";
    return new Response(JSON.stringify({ id: "t1" }), { status: 200, headers: { "content-type": "application/json" } });
  }) as unknown as typeof fetch;
  const c = createClient({ baseUrl: "http://gq", apiKey: "KEY", fetchImpl });
  const r = await c.request("GET", "/api/v1/tickets/t1");
  assert.equal(seenAuth, "KEY");
  assert.equal(r.ok, true);
  assert.deepEqual(r.data, { id: "t1" });
});

test("replayed flag set from X-Idempotent-Replay header", async () => {
  const c = createClient({ baseUrl: "http://gq", apiKey: "K", fetchImpl: fakeFetch(200, { id: "x" }, { "X-Idempotent-Replay": "true" }) });
  const r = await c.request("POST", "/api/v1/tickets", { title: "t" });
  assert.equal(r.replayed, true);
});

test("non-2xx yields ok=false with mapped error hint", async () => {
  const c = createClient({ baseUrl: "http://gq", apiKey: "K", fetchImpl: fakeFetch(400, { error: "title_name must be lowercase" }) });
  const r = await c.request("POST", "/api/v1/tickets", {});
  assert.equal(r.ok, false);
  assert.equal(r.status, 400);
  assert.match(r.error!, /title_name/);
});

test("mapError gives transition hint on 422", () => {
  assert.match(mapError(422, "bad transition"), /transition/i);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: FAIL — cannot find module `./client.ts`.

- [ ] **Step 3: Write `src/client.ts`**

```ts
export type GqResult = {
  ok: boolean;
  status: number;
  data?: unknown;
  error?: string;
  replayed?: boolean;
};

export interface GoquestClient {
  request(method: string, path: string, body?: unknown): Promise<GqResult>;
}

// mapError turns a goquest non-2xx status into an agent-actionable hint,
// preserving the upstream message.
export function mapError(status: number, bodyText: string): string {
  let msg = bodyText;
  try {
    const j = JSON.parse(bodyText);
    msg = (j && typeof j === "object" && "error" in j ? String((j as any).error) : bodyText) || bodyText;
  } catch {
    /* keep raw */
  }
  switch (status) {
    case 400:
      return `Bad request: ${msg}. Check title_name format ^[a-z][a-z0-9]*(-[a-z0-9]+)+$ and required fields.`;
    case 404:
      return `Not found: ${msg}.`;
    case 409:
      return `Conflict: ${msg}.`;
    case 422:
      return `Unprocessable (likely an invalid status transition): ${msg}. Valid: pending→assigned→in_progress→completed.`;
    default:
      return status >= 500 ? `goquest server error (${status}): ${msg}` : `HTTP ${status}: ${msg}`;
  }
}

export function createClient(opts: { baseUrl: string; apiKey: string; fetchImpl?: typeof fetch }): GoquestClient {
  const base = opts.baseUrl.replace(/\/$/, "");
  const doFetch = opts.fetchImpl ?? fetch;
  return {
    async request(method, path, body) {
      let res: Response;
      try {
        res = await doFetch(`${base}${path}`, {
          method,
          headers: {
            "X-API-Key": opts.apiKey,
            ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
          },
          ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
        });
      } catch (err) {
        return { ok: false, status: 0, error: `network error contacting ${base}: ${String(err)}` };
      }
      const text = await res.text();
      if (!res.ok) {
        return { ok: false, status: res.status, error: mapError(res.status, text) };
      }
      let data: unknown = text;
      try {
        data = text ? JSON.parse(text) : null;
      } catch {
        /* leave as text */
      }
      const replayed = res.headers.get("X-Idempotent-Replay") === "true";
      return { ok: true, status: res.status, data, ...(replayed ? { replayed: true } : {}) };
    },
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
cd /workspace/repos/goquest-mcp
git add src/client.ts src/client.test.ts && git commit -q -m "feat: goquest HTTP client with X-API-Key + error mapping"
```

---

## Task 3: Read tools (`get_ticket`, `get_ticket_by_name`, `list_tickets`)

**Files:**
- Create: `/workspace/repos/goquest-mcp/src/tools.ts`
- Test: `/workspace/repos/goquest-mcp/src/tools.test.ts`

**Interfaces:**
- Consumes: `GoquestClient`, `GqResult` from `./client.ts`.
- Produces:
  - `type ToolDef = { name: string; description: string; inputSchema: Record<string, z.ZodTypeAny>; handler: (args: any, client: GoquestClient) => Promise<{ content: { type: "text"; text: string }[]; isError?: boolean }> }`
  - `function textResult(value: unknown, isError?: boolean)` helper.
  - `const readTools: ToolDef[]` containing `get_ticket`, `get_ticket_by_name`, `list_tickets`.

- [ ] **Step 1: Write the failing test**

`src/tools.test.ts`:
```ts
import { test } from "node:test";
import assert from "node:assert/strict";
import type { GoquestClient, GqResult } from "./client.ts";
import { readTools } from "./tools.ts";

function stubClient(handler: (m: string, p: string, b?: unknown) => GqResult): GoquestClient {
  return { async request(m, p, b) { return handler(m, p, b); } };
}
function byName(name: string) {
  const t = readTools.find((x) => x.name === name);
  if (!t) throw new Error(`no tool ${name}`);
  return t;
}

test("get_ticket calls GET /api/v1/tickets/{id}", async () => {
  let seen = "";
  const c = stubClient((m, p) => { seen = `${m} ${p}`; return { ok: true, status: 200, data: { id: "abc" } }; });
  const r = await byName("get_ticket").handler({ id: "abc" }, c);
  assert.equal(seen, "GET /api/v1/tickets/abc");
  assert.match(r.content[0].text, /"id": "abc"/);
});

test("get_ticket_by_name builds by-name path with workspace+project query", async () => {
  let seen = "";
  const c = stubClient((m, p) => { seen = p; return { ok: true, status: 200, data: {} }; });
  await byName("get_ticket_by_name").handler({ title_name: "goq-x", workspace: "toji", project: "whoami" }, c);
  assert.ok(seen.startsWith("/api/v1/tickets/by-name/goq-x?"));
  assert.match(seen, /workspace=toji/);
  assert.match(seen, /project=whoami/);
});

test("list_tickets appends repeated tag and filters", async () => {
  let seen = "";
  const c = stubClient((m, p) => { seen = p; return { ok: true, status: 200, data: { tickets: [] } }; });
  await byName("list_tickets").handler({ project_id: "p1", tag: ["infra", "urgent"], status: "pending" }, c);
  assert.match(seen, /tag=infra/);
  assert.match(seen, /tag=urgent/);
  assert.match(seen, /status=pending/);
  assert.match(seen, /project_id=p1/);
});

test("error result marks isError and shows the hint", async () => {
  const c = stubClient(() => ({ ok: false, status: 404, error: "Not found: x." }));
  const r = await byName("get_ticket").handler({ id: "zzz" }, c);
  assert.equal(r.isError, true);
  assert.match(r.content[0].text, /Not found/);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: FAIL — cannot find module `./tools.ts`.

- [ ] **Step 3: Write `src/tools.ts` (read tools)**

```ts
import { z } from "zod";
import type { GoquestClient, GqResult } from "./client.ts";

export type ToolDef = {
  name: string;
  description: string;
  inputSchema: Record<string, z.ZodTypeAny>;
  handler: (args: any, client: GoquestClient) => Promise<{ content: { type: "text"; text: string }[]; isError?: boolean }>;
};

export function textResult(value: unknown, isError = false) {
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  return { content: [{ type: "text" as const, text }], ...(isError ? { isError: true } : {}) };
}

// Shape a GqResult into an MCP tool result: data on success, hint on error.
function fromResult(r: GqResult) {
  if (!r.ok) return textResult(r.error ?? `HTTP ${r.status}`, true);
  return textResult(r.replayed ? { replayed: true, ticket: r.data } : r.data);
}

function qs(params: Record<string, string | string[] | undefined>): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined) continue;
    if (Array.isArray(v)) v.forEach((item) => sp.append(k, item));
    else sp.set(k, v);
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

export const readTools: ToolDef[] = [
  {
    name: "get_ticket",
    description: "Fetch a single ticket by its UUID.",
    inputSchema: { id: z.string().describe("Ticket UUID") },
    handler: async ({ id }, client) => fromResult(await client.request("GET", `/api/v1/tickets/${id}`)),
  },
  {
    name: "get_ticket_by_name",
    description: "Fetch a ticket by its human title_name within a project named by workspace + project (no UUIDs).",
    inputSchema: {
      title_name: z.string().describe("Ticket title_name, e.g. goq-vault-portforward"),
      workspace: z.string().optional().describe("Workspace name"),
      workspace_slug: z.string().optional().describe("Workspace slug (alternative to workspace)"),
      project: z.string().optional().describe("Project name"),
    },
    handler: async ({ title_name, workspace, workspace_slug, project }, client) =>
      fromResult(
        await client.request(
          "GET",
          `/api/v1/tickets/by-name/${encodeURIComponent(title_name)}${qs({ workspace, workspace_slug, project })}`,
        ),
      ),
  },
  {
    name: "list_tickets",
    description: "List tickets with optional filters: title_name (exact), repeatable tag names, status, domain, project_id.",
    inputSchema: {
      project_id: z.string().optional().describe("Project UUID (required when filtering by tag)"),
      title_name: z.string().optional().describe("Exact title_name match"),
      tag: z.array(z.string()).optional().describe("Tag names (repeatable); requires project_id"),
      status: z.string().optional().describe("pending|assigned|in_progress|pending_approval|completed|failed|cancelled"),
      domain: z.string().optional().describe("infra|data|code|monitor|knowledge|pm"),
      limit: z.number().int().optional(),
      offset: z.number().int().optional(),
    },
    handler: async (a, client) =>
      fromResult(
        await client.request(
          "GET",
          `/api/v1/tickets${qs({
            project_id: a.project_id,
            title_name: a.title_name,
            tag: a.tag,
            status: a.status,
            domain: a.domain,
            limit: a.limit !== undefined ? String(a.limit) : undefined,
            offset: a.offset !== undefined ? String(a.offset) : undefined,
          })}`,
        ),
      ),
  },
];
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /workspace/repos/goquest-mcp
git add src/tools.ts src/tools.test.ts && git commit -q -m "feat: read tools (get_ticket, get_ticket_by_name, list_tickets)"
```

---

## Task 4: Write tools (`create_ticket`, `update_ticket`, `comment`)

**Files:**
- Modify: `/workspace/repos/goquest-mcp/src/tools.ts` (add `writeTools`)
- Modify: `/workspace/repos/goquest-mcp/src/tools.test.ts` (add tests)

**Interfaces:**
- Consumes: `ToolDef`, `textResult`, `GoquestClient` (Task 3).
- Produces: `const writeTools: ToolDef[]` containing `create_ticket`, `update_ticket`, `comment`.

- [ ] **Step 1: Write the failing test (append to `src/tools.test.ts`)**

```ts
import { writeTools } from "./tools.ts";

function wByName(name: string) {
  const t = writeTools.find((x) => x.name === name);
  if (!t) throw new Error(`no tool ${name}`);
  return t;
}

test("create_ticket POSTs mapped body with name-based fields", async () => {
  let body: any;
  const c: GoquestClient = { async request(_m, _p, b) { body = b; return { ok: true, status: 201, data: { id: "n1" } }; } };
  await wByName("create_ticket").handler(
    { title: "Do X", title_name: "goq-do-x", workspace: "toji", project: "goquest", tag_names: ["infra"], domain: "code" },
    c,
  );
  assert.equal(body.title, "Do X");
  assert.equal(body.title_name, "goq-do-x");
  assert.equal(body.workspace_name, "toji");
  assert.equal(body.project_name, "goquest");
  assert.deepEqual(body.tag_names, ["infra"]);
  assert.equal(body.domain, "code");
});

test("create_ticket surfaces replayed flag", async () => {
  const c: GoquestClient = { async request() { return { ok: true, status: 200, data: { id: "e1" }, replayed: true }; } };
  const r = await wByName("create_ticket").handler({ title: "t", project_id: "p", title_name: "goq-t" }, c);
  assert.match(r.content[0].text, /"replayed": true/);
});

test("update_ticket PATCHes /tickets/{id} with the given fields", async () => {
  let seen = ""; let body: any;
  const c: GoquestClient = { async request(m, p, b) { seen = `${m} ${p}`; body = b; return { ok: true, status: 200, data: {} }; } };
  await wByName("update_ticket").handler({ id: "t9", status: "in_progress" }, c);
  assert.equal(seen, "PATCH /api/v1/tickets/t9");
  assert.equal(body.status, "in_progress");
});

test("comment POSTs body to /tickets/{id}/comments", async () => {
  let seen = ""; let body: any;
  const c: GoquestClient = { async request(m, p, b) { seen = `${m} ${p}`; body = b; return { ok: true, status: 201, data: {} }; } };
  await wByName("comment").handler({ id: "t9", body: "looks good" }, c);
  assert.equal(seen, "POST /api/v1/tickets/t9/comments");
  assert.equal(body.body, "looks good");
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: FAIL — `writeTools` not exported.

- [ ] **Step 3: Add `writeTools` to `src/tools.ts`**

Append:
```ts
export const writeTools: ToolDef[] = [
  {
    name: "create_ticket",
    description:
      "Create a ticket. Idempotent: a duplicate title_name in the same project returns the existing ticket (result includes \"replayed\": true). Prefer name-based fields (workspace/project/tag_names) over UUIDs. title_name must match ^[a-z][a-z0-9]*(-[a-z0-9]+)+$ (lowercase, >=1 hyphen). Call ticket_guide for full conventions.",
    inputSchema: {
      title: z.string().describe("Human-readable title (required)"),
      title_name: z.string().optional().describe("Stable handle, e.g. goq-vault-portforward (lowercase, >=1 hyphen)"),
      workspace: z.string().optional().describe("Workspace name"),
      workspace_slug: z.string().optional().describe("Workspace slug"),
      project: z.string().optional().describe("Project name"),
      project_id: z.string().optional().describe("Project UUID (alternative to project name)"),
      tag_names: z.array(z.string()).optional().describe("Tag names; missing tags are auto-created"),
      type: z.enum(["task", "bug", "feature", "incident", "request"]).optional(),
      priority: z.enum(["urgent", "high", "normal", "low"]).optional(),
      domain: z.enum(["infra", "data", "code", "monitor", "knowledge", "pm"]).optional(),
      parent_id: z.string().optional().describe("Parent ticket UUID for sub-tickets"),
      description: z.string().optional(),
    },
    handler: async (a, client) => {
      const body: Record<string, unknown> = { title: a.title };
      if (a.title_name) body.title_name = a.title_name;
      if (a.workspace) body.workspace_name = a.workspace;
      if (a.workspace_slug) body.workspace_slug = a.workspace_slug;
      if (a.project) body.project_name = a.project;
      if (a.project_id) body.project_id = a.project_id;
      if (a.tag_names) body.tag_names = a.tag_names;
      if (a.type) body.type = a.type;
      if (a.priority) body.priority = a.priority;
      if (a.domain) body.domain = a.domain;
      if (a.parent_id) body.parent_id = a.parent_id;
      if (a.description) body.description = a.description;
      const r = await client.request("POST", "/api/v1/tickets", body);
      if (!r.ok) return textResult(r.error ?? `HTTP ${r.status}`, true);
      return textResult({ replayed: r.replayed ?? false, ticket: r.data });
    },
  },
  {
    name: "update_ticket",
    description:
      "Update a ticket by UUID. Supports status transitions (pending→assigned→in_progress→completed; pending→in_progress is rejected — go via assigned).",
    inputSchema: {
      id: z.string().describe("Ticket UUID"),
      status: z.string().optional().describe("New status"),
      title: z.string().optional(),
      title_name: z.string().optional(),
      description: z.string().optional(),
      priority: z.string().optional(),
      domain: z.string().optional(),
      assignee_id: z.string().optional(),
    },
    handler: async (a, client) => {
      const { id, ...rest } = a;
      const body: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(rest)) if (v !== undefined) body[k] = v;
      const r = await client.request("PATCH", `/api/v1/tickets/${id}`, body);
      return r.ok ? textResult(r.data) : textResult(r.error ?? `HTTP ${r.status}`, true);
    },
  },
  {
    name: "comment",
    description: "Add a comment to a ticket.",
    inputSchema: {
      id: z.string().describe("Ticket UUID"),
      body: z.string().describe("Comment text"),
    },
    handler: async ({ id, body }, client) => {
      const r = await client.request("POST", `/api/v1/tickets/${id}/comments`, { body });
      return r.ok ? textResult(r.data) : textResult(r.error ?? `HTTP ${r.status}`, true);
    },
  },
];
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /workspace/repos/goquest-mcp
git add src/tools.ts src/tools.test.ts && git commit -q -m "feat: write tools (create_ticket idempotent, update_ticket, comment)"
```

---

## Task 5: Discovery tool (`list_api`)

**Files:**
- Create: `/workspace/repos/goquest-mcp/src/api-tools.ts`
- Test: `/workspace/repos/goquest-mcp/src/api-tools.test.ts`

**Interfaces:**
- Consumes: `ToolDef`, `textResult`, `GoquestClient`.
- Produces: `const apiTools: ToolDef[]` with `list_api`; `function condenseOpenAPI(spec: any): string[]`.

- [ ] **Step 1: Write the failing test**

`src/api-tools.test.ts`:
```ts
import { test } from "node:test";
import assert from "node:assert/strict";
import type { GoquestClient } from "./client.ts";
import { apiTools, condenseOpenAPI } from "./api-tools.ts";

const SPEC = {
  openapi: "3.1.0",
  paths: {
    "/tickets": { post: { summary: "Create a ticket" }, get: { summary: "List tickets" } },
    "/tickets/by-name/{title_name}": { get: { summary: "Look up by title_name" } },
  },
};

test("condenseOpenAPI yields one line per operation", () => {
  const lines = condenseOpenAPI(SPEC);
  assert.ok(lines.includes("POST /tickets — Create a ticket"));
  assert.ok(lines.includes("GET /tickets — List tickets"));
  assert.ok(lines.includes("GET /tickets/by-name/{title_name} — Look up by title_name"));
});

test("list_api returns condensed list by default", async () => {
  const c: GoquestClient = { async request() { return { ok: true, status: 200, data: SPEC }; } };
  const t = apiTools.find((x) => x.name === "list_api")!;
  const r = await t.handler({}, c);
  assert.match(r.content[0].text, /POST \/tickets — Create a ticket/);
});

test("list_api full=true returns raw spec", async () => {
  const c: GoquestClient = { async request() { return { ok: true, status: 200, data: SPEC }; } };
  const t = apiTools.find((x) => x.name === "list_api")!;
  const r = await t.handler({ full: true }, c);
  assert.match(r.content[0].text, /"openapi": "3.1.0"/);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: FAIL — cannot find module `./api-tools.ts`.

- [ ] **Step 3: Write `src/api-tools.ts`**

```ts
import { z } from "zod";
import type { ToolDef } from "./tools.ts";
import { textResult } from "./tools.ts";

const METHODS = ["get", "post", "put", "patch", "delete"] as const;

// condenseOpenAPI turns an OpenAPI doc into "METHOD /path — summary" lines.
export function condenseOpenAPI(spec: any): string[] {
  const out: string[] = [];
  const paths = (spec && spec.paths) || {};
  for (const [path, item] of Object.entries<any>(paths)) {
    for (const m of METHODS) {
      const op = item?.[m];
      if (!op) continue;
      out.push(`${m.toUpperCase()} ${path} — ${op.summary ?? op.operationId ?? ""}`.trimEnd());
    }
  }
  return out;
}

export const apiTools: ToolDef[] = [
  {
    name: "list_api",
    description:
      "List the goquest ticket API operations (from the served OpenAPI spec). Returns a condensed 'METHOD /path — summary' list by default; pass full=true for the raw OpenAPI JSON.",
    inputSchema: {
      full: z.boolean().optional().describe("Return the raw OpenAPI spec instead of the condensed list"),
    },
    handler: async ({ full }, client) => {
      const r = await client.request("GET", "/api/v1/openapi.json");
      if (!r.ok) return textResult(r.error ?? `HTTP ${r.status}`, true);
      if (full) return textResult(r.data);
      return textResult(condenseOpenAPI(r.data).join("\n"));
    },
  },
];
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /workspace/repos/goquest-mcp
git add src/api-tools.ts src/api-tools.test.ts && git commit -q -m "feat: list_api discovery tool (condensed + full OpenAPI)"
```

---

## Task 6: Ticket guide (geneso source + embedded copy + `ticket_guide` tool)

**Files:**
- Create: `/workspace/repos/geneso/goquest/guide/ticket-authoring-guide.md` (source of truth)
- Create: `/workspace/repos/goquest-mcp/ticket-guide.md` (synced copy)
- Create: `/workspace/repos/goquest-mcp/src/guide.ts`
- Create: `/workspace/repos/goquest-mcp/src/guide.test.ts`

**Interfaces:**
- Consumes: `ToolDef`, `textResult`.
- Produces: `const GUIDE_TEXT: string` (loaded from `ticket-guide.md`); `const guideTools: ToolDef[]` with `ticket_guide`.

- [ ] **Step 1: Write the geneso source guide `/workspace/repos/geneso/goquest/guide/ticket-authoring-guide.md`**

```markdown
---
id: "GQT-GUIDE-20260701"
title: "goquest 티켓 작성 가이드 (에이전트/MCP용)"
type: "guide"
project: "goquest"
status: "active"
author: "lyckabc"
created: "2026-07-01T00:00:00Z"
entities: ["goquest"]
---
# goquest 티켓 작성 가이드 (에이전트/MCP용)

goquest-mcp 도구로 티켓을 만들 때 따르는 규약. 정본은 이 문서이며, goquest-mcp 레포의 `ticket-guide.md`는 이 문서의 동기화 복사본이다.

## title_name 규약
- 정규식 `^[a-z][a-z0-9]*(-[a-z0-9]+)+$` — 소문자만, 하이픈 1개 이상 필수.
- 대문자(`GOQ-…`)나 하이픈 없는 단어는 422 거부.
- 형태: `<prefix>-<slug>`. prefix: goquest=`goq`, whoami=`who`, gopedia=`gop`, neunexus=`neunex`, infra=`infra`.
- 예: `goq-vault-portforward`, `who-render-contract`.

## 이름 기반 필드 우선
- `workspace`/`project`/`tag_names`로 지정한다 (UUID 불필요). 미존재 태그는 자동 생성(get-or-create).

## 멱등
- 같은 project 안에서 동일 `title_name`으로 create하면 기존 티켓이 반환된다(`replayed: true`). 재시도는 안전하다.

## dedup / parent 절차
1. 생성 전 `list_tickets`로 열린 티켓(status `pending`|`in_progress`) 조회.
2. 같은 태그 + 같은 `domain`의 티켓이 있으면 `parent_id`로 지정해 sub-ticket으로 만든다.
3. 없으면 `parent_id` 없이 new ticket.

## 상태머신
- `pending → assigned → in_progress → (pending_approval) → completed`.
- terminal: `completed`, `failed`, `cancelled` (추가 전이 불가).
- `pending → in_progress` 직행 불가 — 반드시 `assigned` 경유.

## 필드
- 필수: `title`. 권장: `title_name`, `project`(또는 `project_id`), `domain`.
- domain enum: `infra|data|code|monitor|knowledge|pm`.
- type enum: `task|bug|feature|incident|request`. priority: `urgent|high|normal|low`.
```

- [ ] **Step 2: Copy the guide into the repo (verbatim body, drop front-matter)**

Create `/workspace/repos/goquest-mcp/ticket-guide.md` — copy the geneso file's markdown **from the `# goquest 티켓 작성 가이드` heading onward** (omit the YAML front-matter). Add this first line above the heading:

```markdown
<!-- Synced from geneso goquest/guide/ticket-authoring-guide.md — edit there (source of truth), then re-sync. -->
```

- [ ] **Step 3: Write the failing test**

`src/guide.test.ts`:
```ts
import { test } from "node:test";
import assert from "node:assert/strict";
import { GUIDE_TEXT, guideTools } from "./guide.ts";

test("GUIDE_TEXT includes the title_name regex and state machine", () => {
  assert.match(GUIDE_TEXT, /\^\[a-z\]\[a-z0-9\]\*\(-\[a-z0-9\]\+\)\+\$/);
  assert.match(GUIDE_TEXT, /assigned/);
});

test("ticket_guide tool returns the guide text", async () => {
  const t = guideTools.find((x) => x.name === "ticket_guide")!;
  const r = await t.handler({}, { async request() { return { ok: true, status: 200 }; } });
  assert.match(r.content[0].text, /title_name/);
});
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: FAIL — cannot find module `./guide.ts`.

- [ ] **Step 5: Write `src/guide.ts`**

```ts
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { z } from "zod";
import type { ToolDef } from "./tools.ts";
import { textResult } from "./tools.ts";

const here = dirname(fileURLToPath(import.meta.url));
// ticket-guide.md lives at the repo root, one level up from src/.
export const GUIDE_TEXT = readFileSync(join(here, "..", "ticket-guide.md"), "utf8");

export const guideTools: ToolDef[] = [
  {
    name: "ticket_guide",
    description:
      "Return the goquest ticket authoring guide: title_name format, name-based fields, idempotency, dedup/parent procedure, and the status state machine. Read this before creating tickets.",
    inputSchema: {},
    handler: async () => textResult(GUIDE_TEXT),
  },
];
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: PASS.

- [ ] **Step 7: Commit (both repos)**

```bash
cd /workspace/repos/geneso
git add goquest/guide/ticket-authoring-guide.md
git commit -q -m "docs(goquest): agent ticket authoring guide (goquest-mcp source of truth)"
cd /workspace/repos/goquest-mcp
git add ticket-guide.md src/guide.ts src/guide.test.ts
git commit -q -m "feat: ticket_guide tool + embedded guide (synced from geneso)"
```

> After committing, ingest the geneso guide: `mcp__gp__gopedia_ingest_content(title, content, source="goquest/guide/ticket-authoring-guide.md")`. (Search verification may fail if Qdrant is down — ingest success is sufficient.)

---

## Task 7: Server wiring + transport + smoke test

**Files:**
- Create: `/workspace/repos/goquest-mcp/src/goquest-mcp-server.ts`
- Create: `/workspace/repos/goquest-mcp/scripts/mcp-smoke-test.ts`

**Interfaces:**
- Consumes: `readTools`, `writeTools` (`./tools.ts`), `apiTools` (`./api-tools.ts`), `guideTools`, `GUIDE_TEXT` (`./guide.ts`), `createClient` (`./client.ts`), `SERVER_NAME`, `SERVER_VERSION` (`./version.ts`).
- Produces: `function buildServer(client): McpServer` (exported for the smoke test); a runnable entrypoint.

- [ ] **Step 1: Write `src/goquest-mcp-server.ts`**

```ts
import "dotenv/config";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { createServer } from "node:http";
import { createClient, type GoquestClient } from "./client.ts";
import { readTools, writeTools } from "./tools.ts";
import { apiTools } from "./api-tools.ts";
import { guideTools, GUIDE_TEXT } from "./guide.ts";
import { SERVER_NAME, SERVER_VERSION } from "./version.ts";

const BASE_URL = (process.env["GOQUEST_API_URL"]?.trim() || "http://goquest-service.goquest.svc.cluster.local:8080").replace(/\/$/, "");
const API_KEY = process.env["GOQUEST_API_KEY"]?.trim() ?? "";

export function buildServer(client: GoquestClient): McpServer {
  const server = new McpServer({ name: SERVER_NAME, version: SERVER_VERSION });
  for (const t of [...readTools, ...writeTools, ...apiTools, ...guideTools]) {
    server.registerTool(t.name, { description: t.description, inputSchema: t.inputSchema }, (args: any) => t.handler(args, client));
  }
  // Also expose the guide as a readable resource.
  server.registerResource(
    "ticket-guide",
    "goquest://ticket-guide",
    { title: "goquest ticket authoring guide", mimeType: "text/markdown" },
    async (uri) => ({ contents: [{ uri: uri.href, mimeType: "text/markdown", text: GUIDE_TEXT }] }),
  );
  return server;
}

async function main() {
  if (!API_KEY) process.stderr.write("warning: GOQUEST_API_KEY is empty; goquest calls will 401\n");
  const client = createClient({ baseUrl: BASE_URL, apiKey: API_KEY });
  const server = buildServer(client);
  const httpPort = process.env["MCP_HTTP_PORT"] ? parseInt(process.env["MCP_HTTP_PORT"], 10) : undefined;
  if (httpPort) {
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
    createServer((req, res) => transport.handleRequest(req, res)).listen(httpPort, () =>
      process.stderr.write(`goquest-mcp HTTP listening on :${httpPort}\n`),
    );
    await server.connect(transport);
  } else {
    await server.connect(new StdioServerTransport());
  }
}

// Only run the server when executed directly — NOT when imported (the smoke
// test imports buildServer, and running main() on import would spin up a real
// stdio server that hangs under a kept-open stdin.
import { realpathSync } from "node:fs";
const entry = process.argv[1] ? realpathSync(process.argv[1]) : "";
if (entry && import.meta.url === `file://${entry}`) {
  main().catch((e) => {
    process.stderr.write(`fatal: ${String(e)}\n`);
    process.exit(1);
  });
}
```

> The entrypoint guard is the Node/ESM equivalent of `require.main === module`. `realpathSync` normalises symlinks so the comparison holds when tsx runs the `.ts` file directly.

- [ ] **Step 2: Write the smoke test `scripts/mcp-smoke-test.ts`**

```ts
// In-process smoke test: builds the server against a stub client and exercises
// each tool handler, then verifies buildServer wires without throwing.
import assert from "node:assert/strict";
import { buildServer } from "../src/goquest-mcp-server.ts";
import { readTools, writeTools } from "../src/tools.ts";
import { apiTools } from "../src/api-tools.ts";
import { guideTools } from "../src/guide.ts";
import type { GoquestClient } from "../src/client.ts";

const stub: GoquestClient = {
  async request(method, path) {
    if (path.includes("/openapi.json")) return { ok: true, status: 200, data: { openapi: "3.1.0", paths: { "/tickets": { get: { summary: "List" } } } } };
    return { ok: true, status: 200, data: { method, path } };
  },
};

const all = [...readTools, ...writeTools, ...apiTools, ...guideTools];
assert.equal(all.length, 8, `expected 8 tools, got ${all.length}`);

for (const t of all) {
  const sampleArgs: Record<string, any> = {
    get_ticket: { id: "x" },
    get_ticket_by_name: { title_name: "goq-x", workspace: "w", project: "p" },
    list_tickets: {},
    create_ticket: { title: "t", title_name: "goq-t", project_id: "p" },
    update_ticket: { id: "x", status: "assigned" },
    comment: { id: "x", body: "hi" },
    list_api: {},
    ticket_guide: {},
  }[t.name] ?? {};
  const r = await t.handler(sampleArgs, stub);
  assert.ok(r.content[0].text.length > 0, `${t.name} returned empty`);
}

// buildServer must wire all tools + the resource without throwing.
buildServer(stub);
process.stdout.write(`smoke ok: ${all.length} tools\n`);
```

- [ ] **Step 3: Typecheck + run smoke test**

Run:
```bash
cd /workspace/repos/goquest-mcp && npm run typecheck && npm run smoke
```
Expected: typecheck clean; smoke prints `smoke ok: 8 tools`.

- [ ] **Step 4: Run the full unit suite**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: PASS (client, tools, api-tools, guide).

- [ ] **Step 5: Commit**

```bash
cd /workspace/repos/goquest-mcp
git add src/goquest-mcp-server.ts scripts/mcp-smoke-test.ts && git commit -q -m "feat: server wiring (8 tools + guide resource) + smoke test"
```

---

## Task 8: Dockerfile, deploy, CI, README

**Files:**
- Create: `/workspace/repos/goquest-mcp/Dockerfile`
- Create: `/workspace/repos/goquest-mcp/deploy/deployment.yaml`
- Create: `/workspace/repos/goquest-mcp/deploy/service.yaml`
- Create: `/workspace/repos/goquest-mcp/ci.yaml`
- Create: `/workspace/repos/goquest-mcp/README.md`

**Interfaces:**
- Consumes: the runnable server from Task 7.
- Produces: a container image spec and k8s manifests; no code interfaces.

- [ ] **Step 1: Write `Dockerfile`**

```dockerfile
FROM node:20-slim
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --omit=dev && npm install tsx
COPY . .
ENV MCP_HTTP_PORT=8080
EXPOSE 8080
CMD ["npx", "tsx", "src/goquest-mcp-server.ts"]
```

- [ ] **Step 2: Write `deploy/deployment.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: goquest-mcp
  namespace: goquest
spec:
  replicas: 1
  selector:
    matchLabels: { app: goquest-mcp }
  template:
    metadata:
      labels: { app: goquest-mcp }
    spec:
      containers:
        - name: goquest-mcp
          image: artifacts.toji.homes/neunexus/goquest-mcp:latest
          ports: [{ containerPort: 8080 }]
          env:
            - name: GOQUEST_API_URL
              value: http://goquest-service.goquest.svc.cluster.local:8080
            - name: MCP_HTTP_PORT
              value: "8080"
            - name: GOQUEST_API_KEY
              valueFrom:
                secretKeyRef: { name: goquest-mcp, key: api-key }
```

- [ ] **Step 3: Write `deploy/service.yaml`**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: goquest-mcp
  namespace: goquest
spec:
  selector: { app: goquest-mcp }
  ports:
    - port: 8080
      targetPort: 8080
```

- [ ] **Step 4: Write `ci.yaml`**

```yaml
# Woodpecker CI — build + test on push.
steps:
  test:
    image: node:20-slim
    commands:
      - npm install
      - npm run typecheck
      - npm test
      - npm run smoke
```

- [ ] **Step 5: Write `README.md`**

````markdown
# goquest-mcp

MCP server exposing goquest ticket operations to LLM agents. Proxies the goquest
REST/OpenAPI contract (Phase 1).

## Tools
- `create_ticket` (idempotent on title_name), `get_ticket`, `get_ticket_by_name`,
  `list_tickets`, `update_ticket`, `comment`
- `list_api` — discover goquest API operations (condensed or `full`)
- `ticket_guide` — ticket authoring conventions (also resource `goquest://ticket-guide`)

## Config
- `GOQUEST_API_URL` (default in-cluster goquest-service)
- `GOQUEST_API_KEY` (from Vault `secret/neunexus/workspace/goquest#api_key`)
- `MCP_HTTP_PORT` (set → StreamableHTTP; unset → stdio)

## Run
```bash
npm install
GOQUEST_API_KEY=... npm start        # stdio
GOQUEST_API_KEY=... MCP_HTTP_PORT=8080 npm start   # HTTP
```

## Register (~/.mcp.json)
```json
{ "mcpServers": { "goquest": { "command": "npx", "args": ["tsx", "src/goquest-mcp-server.ts"], "env": { "GOQUEST_API_KEY": "..." } } } }
```

## Guide sync
`ticket-guide.md` is a synced copy of geneso `goquest/guide/ticket-authoring-guide.md`
(source of truth). Edit geneso, then re-copy.
````

- [ ] **Step 6: Commit**

```bash
cd /workspace/repos/goquest-mcp
git add Dockerfile deploy/ ci.yaml README.md && git commit -q -m "chore: Dockerfile, k8s deploy, CI, README"
```

---

## Final verification

- [ ] **Run everything**

Run:
```bash
cd /workspace/repos/goquest-mcp && npm run typecheck && npm test && npm run smoke
```
Expected: typecheck clean, all unit tests pass, `smoke ok: 8 tools`.

- [ ] **Confirm tool count and names**

Run: `cd /workspace/repos/goquest-mcp && node --import tsx -e "import('./src/tools.ts').then(async t=>{const a=import('./src/api-tools.ts'),g=import('./src/guide.ts');const [ap,gu]=await Promise.all([a,g]);console.log([...t.readTools,...t.writeTools,...ap.apiTools,...gu.guideTools].map(x=>x.name).join(', '))})"`
Expected: `get_ticket, get_ticket_by_name, list_tickets, create_ticket, update_ticket, comment, list_api, ticket_guide`

## Deployment follow-ups (out of plan scope, tracked separately)

- Create the GitHub repo `tojiuni/goquest-mcp` and push.
- Create k8s secret `goquest-mcp/api-key` from Vault.
- Build/publish image via CI; register in `~/.mcp.json`.
