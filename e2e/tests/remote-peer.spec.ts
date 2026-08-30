import { expect, test, type Page } from "@playwright/test";

import {
  assistantMessageCountContaining,
  remoteSessionByPrompt,
  runningSessionCount,
} from "../fixtures/db";

const runID = process.env.AGENTRE_E2E_RUN_ID;
if (!runID) throw new Error("remote peer smoke must be started by the E2E runner");
const SUCCESS_PROMPT = `remote-peer-stream-${runID}`;
const FAILURE_PROMPT = `remote-peer-disconnect-${runID}`;
const RECOVERY_PROMPT = `remote-peer-recover-${runID}`;
const SUCCESS_REPLY = `remote-peer-reply: ${SUCCESS_PROMPT}`;
const RECOVERY_REPLY = `remote-peer-reply: ${RECOVERY_PROMPT}`;
const REMOTE_AGENT = "E2E Remote Agent";

async function createRemoteChat(page: Page) {
  await page.getByTestId("new-chat-button").click();
  await page.getByTestId("new-agent-chat-item").click();
  await page.locator('[data-testid^="agent-picker-item-"]', { hasText: REMOTE_AGENT }).click();
  await expect(page.locator('[role="tab"][data-active="true"]')).toBeVisible();
}

async function send(page: Page, text: string) {
  const editor = page.locator(".ProseMirror");
  await expect(editor).toBeVisible();
  await editor.click();
  await editor.pressSequentially(text);
  await page.getByRole("main").locator('button[type="submit"]').click();
}

function remoteControl() {
  const url = process.env.AGENTRE_E2E_REMOTE_PEER_CONTROL_URL;
  const token = process.env.AGENTRE_E2E_CONTROL_TOKEN;
  if (!url || !token) throw new Error("remote fake control is not configured");
  return { url, token };
}

async function configureNextFault(fault: "disconnect" | "recoverable-disconnect" | "protocol") {
  const { url, token } = remoteControl();
  const response = await fetch(`${url}/fault`, {
    method: "POST",
    headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
    body: JSON.stringify({ fault }),
  });
  if (!response.ok) throw new Error(`remote fake fault control returned HTTP ${response.status}`);
}

async function remoteRequests(): Promise<Array<{
  connectionId: number;
  method: string;
  params?: Record<string, unknown>;
}>> {
  const { url, token } = remoteControl();
  const response = await fetch(`${url}/snapshot`, {
    headers: { authorization: `Bearer ${token}` },
  });
  if (!response.ok) throw new Error(`remote fake recorder returned HTTP ${response.status}`);
  return ((await response.json()) as { requests: Array<{ connectionId: number; method: string; params?: Record<string, unknown> }> }).requests;
}

async function remoteHighWater(sessionID: number): Promise<number | undefined> {
  const { url, token } = remoteControl();
  const response = await fetch(`${url}/snapshot`, {
    headers: { authorization: `Bearer ${token}` },
  });
  if (!response.ok) throw new Error(`remote fake recorder returned HTTP ${response.status}`);
  const snapshot = (await response.json()) as {
    sessions: Array<{ sessionId: number; latestSeq: number }>;
  };
  return snapshot.sessions.find((session) => session.sessionId === sessionID)?.latestSeq;
}

test.describe.serial("remote peer smoke", () => {
  test("Given a paired loopback remote target, when its agent streams a reply, then real desktop transport persists reply and remote execution identity", async ({ page }) => {
    await page.goto("/");
    await createRemoteChat(page);
    await send(page, SUCCESS_PROMPT);

    await expect(page.getByText(SUCCESS_REPLY)).toBeVisible();
    await expect(page.getByTestId("tab-spinner")).toHaveCount(0);
    await expect.poll(() => runningSessionCount()).toBe(0);
    await expect.poll(() => assistantMessageCountContaining(SUCCESS_REPLY)).toBe(1);
    const session = await expect.poll(() => remoteSessionByPrompt(SUCCESS_PROMPT)).not.toBeUndefined();
    void session;
    expect(remoteSessionByPrompt(SUCCESS_PROMPT)).toMatchObject({
      agent_status: "idle",
      provider_session_id: expect.stringContaining("e2e-remote-session-"),
      exec_device_fingerprint: process.env.AGENTRE_E2E_REMOTE_DAEMON_FINGERPRINT,
      event_cursor: expect.any(Number),
    });
    expect(remoteSessionByPrompt(SUCCESS_PROMPT)!.exec_device_id).toBeGreaterThan(0);
    expect(remoteSessionByPrompt(SUCCESS_PROMPT)!.event_cursor).toBeGreaterThan(0);

    await expect.poll(async () => (await remoteRequests()).some((request) => request.method === "health.ping")).toBe(true);
    const requests = await remoteRequests();
    for (const method of ["auth.connect", "health.ping", "runtime.capabilities", "runtime.session.list", "runtime.run"]) {
      expect(requests.some((request) => request.method === method)).toBe(true);
    }
    const authenticatedConnections = new Set(
      requests.filter((request) => request.method === "auth.connect").map((request) => request.connectionId),
    );
    expect(authenticatedConnections.size).toBeGreaterThanOrEqual(2);
    for (const auth of requests.filter((request) => request.method === "auth.connect")) {
      expect(Object.values(auth.params ?? {})).toContain("[REDACTED]");
    }
    expect(JSON.stringify(requests)).not.toContain("e2e-peer-token-");
  });

  test("Given a mid-stream peer disconnect, when the real reconnect path cannot resume that generation, then UI and SQLite reach a visible terminal state instead of running forever", async ({ page }) => {
    await configureNextFault("disconnect");
    await page.goto("/");
    await createRemoteChat(page);
    await send(page, FAILURE_PROMPT);

    await expect(page.getByText(/interrupted|中断/i).last()).toBeVisible();
    await expect(page.getByTestId("tab-spinner")).toHaveCount(0, { timeout: 30_000 });
    await expect.poll(() => runningSessionCount(), { timeout: 30_000 }).toBe(0);
    await expect.poll(() => remoteSessionByPrompt(FAILURE_PROMPT)?.agent_status, { timeout: 30_000 }).toBe("error");
    expect(remoteSessionByPrompt(FAILURE_PROMPT)?.error_text).not.toBe("");
  });

  test("Given a recoverable mid-stream disconnect, when the peer finishes journaling while offline, then desktop reconnects, attaches, pulls, and persists one complete idle reply", async ({ page }) => {
    await configureNextFault("recoverable-disconnect");
    await page.goto("/");
    await createRemoteChat(page);
    await send(page, RECOVERY_PROMPT);

    await expect(page.getByText(RECOVERY_REPLY)).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("tab-spinner")).toHaveCount(0, { timeout: 30_000 });
    await expect.poll(() => runningSessionCount(), { timeout: 30_000 }).toBe(0);
    await expect.poll(() => assistantMessageCountContaining(RECOVERY_REPLY), { timeout: 30_000 }).toBe(1);
    const session = await expect.poll(() => remoteSessionByPrompt(RECOVERY_PROMPT), { timeout: 30_000 }).not.toBeUndefined();
    void session;
    const recovered = remoteSessionByPrompt(RECOVERY_PROMPT)!;
    expect(recovered.agent_status).toBe("idle");
    expect(recovered.error_text).toBe("");
    await expect.poll(() => remoteHighWater(recovered.id)).toBe(recovered.event_cursor);

    const requests = await remoteRequests();
    const run = requests.find((request) => request.method === "runtime.run" && request.params?.user_text === RECOVERY_PROMPT);
    expect(run).toBeDefined();
    const reconnectRequests = requests.filter((request) => request.connectionId !== run!.connectionId);
    for (const method of ["auth.connect", "runtime.session.list", "runtime.session.attach", "runtime.session.pull"]) {
      expect(reconnectRequests.some((request) => request.method === method)).toBe(true);
    }
  });

  test("Given an unknown remote event followed by the protocol terminal result, when the desktop decodes the stream, then it surfaces the failure and does not remain running", async ({ page }) => {
    const prompt = `remote-peer-protocol-${runID}`;
    await configureNextFault("protocol");
    await page.goto("/");
    await createRemoteChat(page);
    await send(page, prompt);

    await expect(page.getByText(/e2e remote protocol failure/i)).toBeVisible();
    await expect(page.getByTestId("tab-spinner")).toHaveCount(0);
    await expect.poll(() => runningSessionCount()).toBe(0);
    await expect.poll(() => remoteSessionByPrompt(prompt)?.agent_status).toBe("error");
    expect(remoteSessionByPrompt(prompt)?.error_text).toContain("e2e remote protocol failure");
  });
});
