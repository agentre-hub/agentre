import { mkdirSync, realpathSync } from "node:fs";
import { join } from "node:path";

import { expect, test, type Page } from "@playwright/test";

import {
  agrctlOverlayCountOfKind,
  assistantMessageCountContaining,
  e2eDataDir,
  errorSessionCountContaining,
  mainDatabaseFileFromPragma,
  projectByName,
  runningSessionCount,
  seededLocalAgentID,
  sessionByPrompt,
  sessionMessageCounts,
  userMessageCountContaining,
} from "../fixtures/db";

const runID = process.env.AGENTRE_E2E_RUN_ID;
if (!runID) throw new Error("desktop smoke must be started by the E2E runner");
const SUCCESS_PROMPT = `desktop-smoke-persist-${runID}`;
const SECOND_TURN_PROMPT = `desktop-smoke-second-turn-${runID}`;
const PROJECT_NAME = `desktop-project-${runID}`;
const FAILURE_REASON = `desktop-smoke-boundary-${runID}`;
const FAILURE_PROMPT = `e2e-runtime-fail:${FAILURE_REASON}`;
const ReplyPattern = "e2e-fake-reply:";
const FAILURE_TEXT = `e2e-runtime-failure: ${FAILURE_REASON}`;

async function createChat(page: Page) {
  await page.getByTestId("new-chat-button").click();
  await page.getByTestId("new-agent-chat-item").click();
  await page.locator('[data-testid^="agent-picker-item-"]').first().click();
  await expect(page.locator('[role="tab"][data-active="true"]')).toBeVisible();
}

async function send(page: Page, text: string) {
  const editor = page.locator(".ProseMirror");
  await expect(editor).toBeVisible();
  await editor.click();
  await editor.pressSequentially(text);
  await page.getByRole("main").locator('button[type="submit"]').click();
}

async function createProject(page: Page, name: string): Promise<string> {
  const path = join(e2eDataDir(), "projects", name);
  mkdirSync(path, { recursive: true });
  await page.getByTestId("new-chat-button").click();
  await page.getByTestId("project-create-trigger").click();
  const dialog = page.getByRole("dialog");
  await dialog.getByTestId("project-create-path").fill(path);
  await dialog.getByTestId("project-create-name").fill(name);
  await dialog.getByTestId("project-create-submit").click();
  await expect(dialog).toBeHidden();
  return path;
}

test.describe.serial("desktop smoke", () => {
  test("Given a fresh isolated app, when chat completes and the page reloads, then UI and SQLite retain the deterministic turn", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByTestId("new-chat-button")).toBeVisible();
    await createChat(page);
    await send(page, SUCCESS_PROMPT);

    await expect(page.getByText(`e2e-fake-reply: ${SUCCESS_PROMPT}`)).toBeVisible();
    await expect(page.getByTestId("tab-spinner")).toHaveCount(0);
    await expect.poll(() => runningSessionCount()).toBe(0);
    expect(userMessageCountContaining(SUCCESS_PROMPT)).toBe(1);
    expect(assistantMessageCountContaining(`e2e-fake-reply: ${SUCCESS_PROMPT}`)).toBe(1);
    expect(mainDatabaseFileFromPragma()).toBe(realpathSync(join(e2eDataDir(), "agentre.db")));

    const firstTurnSession = sessionByPrompt(SUCCESS_PROMPT);
    expect(firstTurnSession).toMatchObject({
      agent_status: "idle",
      provider_session_id: `e2e-fake-${firstTurnSession?.id}`,
    });

    await send(page, SECOND_TURN_PROMPT);
    await expect(page.getByText(`e2e-fake-reply: ${SECOND_TURN_PROMPT}`)).toBeVisible();
    await expect(page.getByTestId("tab-spinner")).toHaveCount(0);
    await expect.poll(() => runningSessionCount()).toBe(0);

    const secondTurnSession = sessionByPrompt(SECOND_TURN_PROMPT);
    expect(secondTurnSession?.id).toBe(firstTurnSession?.id);
    expect(secondTurnSession?.provider_session_id).toBe(firstTurnSession?.provider_session_id);
    expect(sessionMessageCounts(firstTurnSession!.id)).toEqual({ user: 2, assistant: 2 });

    await page.reload();
    await expect(page.getByText(SUCCESS_PROMPT, { exact: true }).first()).toBeVisible();
    await expect(page.getByText(`e2e-fake-reply: ${SUCCESS_PROMPT}`)).toBeVisible();
    await expect(page.getByText(SECOND_TURN_PROMPT, { exact: true }).first()).toBeVisible();
    await expect(page.getByText(`e2e-fake-reply: ${SECOND_TURN_PROMPT}`)).toBeVisible();
    await expect(page.getByTestId("tab-spinner")).toHaveCount(0);
    expect(sessionMessageCounts(firstTurnSession!.id)).toEqual({ user: 2, assistant: 2 });
  });

  test("Given the deterministic runtime failure directive, when a turn fails, then the UI and SQLite reach an error terminal state instead of running forever", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByTestId("new-chat-button")).toBeVisible();
    await createChat(page);
    await send(page, FAILURE_PROMPT);

    await expect(page.getByText(new RegExp(`Agent call failed: ${FAILURE_TEXT}`))).toBeVisible();
    await expect(page.getByTestId("tab-spinner")).toHaveCount(0);
    await expect.poll(() => runningSessionCount()).toBe(0);
    await expect.poll(() => errorSessionCountContaining(FAILURE_TEXT)).toBe(1);
  });

  test("Given a project with a local member, when chat starts from its group, then the session executes in that project path and remains grouped after reload", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByTestId("new-chat-button")).toBeVisible();
    const projectPath = await createProject(page, PROJECT_NAME);
    const project = await expect.poll(() => projectByName(PROJECT_NAME)).not.toBeUndefined();
    void project;
    const projectID = projectByName(PROJECT_NAME)!.id;

    await page.evaluate(
      ({ projectID: id, agentID }) =>
        (window as never as ProjectWailsWindow).go.app.App.ProjectAddMember(id, agentID),
      { projectID, agentID: seededLocalAgentID() },
    );
    await page.getByTestId(`project-add-${projectID}`).click();
    await expect(page.locator('[role="tab"][data-active="true"]')).toBeVisible();
    const cwdPrompt = `e2e-assert-cwd:${projectPath}`;
    await send(page, cwdPrompt);

    await expect(page.getByText(`e2e-cwd-ok:${projectPath}`, { exact: false })).toBeVisible();
    await expect.poll(() => runningSessionCount()).toBe(0);
    expect(sessionByPrompt(cwdPrompt)).toMatchObject({
      project_id: projectID,
      agent_status: "idle",
    });

    await page.reload();
    await expect(page.getByText(PROJECT_NAME, { exact: true }).first()).toBeVisible();
    await expect(page.getByText(cwdPrompt, { exact: true }).first()).toBeVisible();
    await expect(page.getByText(`e2e-cwd-ok:${projectPath}`, { exact: false })).toBeVisible();
  });

  // late:Codex 的顺序 —— send 先到、子会话已经出了一步,前台 shell 调用才登记进父轮。
  const agrctlSendShapes = {
    fg: "a foreground Bash call running agrctl send --wait",
    bg: "a background Bash call running agrctl send --wait",
    late: "an agrctl send --wait that reaches the desktop before its foreground Bash call is registered",
  } as const;
  for (const mode of ["fg", "bg", "late"] as const) {
    test(`Given ${agrctlSendShapes[mode]}, when the child session works and finishes, then the shell call is upgraded in place into one subagent card with live steps and the final text, and it survives a reload`, async ({ page }) => {
      const label = `ctl-${mode}-${runID}`;
      // 库里的卡片计数跨用例累计(同一 kind 的前一个用例已留下一张):只看这一轮新增的。
      const expectedKind = mode === "bg" ? "local_bash" : "agrctl_send";
      const overlaysBefore = agrctlOverlayCountOfKind(expectedKind);
      await page.goto("/");
      await expect(page.getByTestId("new-chat-button")).toBeVisible();
      await createChat(page);
      await send(page, `e2e-agrctl-send:${mode}:${seededLocalAgentID()}:${label}`);

      // 升级:父轮的 shell 调用原地变成一张子代理卡(且只有一张)。
      const card = page.getByTestId("agent-spawn-card");
      await expect(card).toHaveCount(1);
      await card.locator('[aria-expanded]').first().click();

      // 实时步骤:子会话的嵌套 Bash 步骤在卡片仍在运行时出现。
      await expect(card.getByText("echo e2e-step-1").first()).toBeVisible();
      await expect(card.getByTestId("agent-spawn-status-pill")).toBeVisible();

      // 收尾:子会话最终文本进入 SUMMARY,轮结束。
      await expect(card.getByText(new RegExp(`${ReplyPattern}.*${label}`))).toBeVisible();
      await expect(card.getByText("echo e2e-step-2").first()).toBeVisible();
      await expect(page.getByTestId("tab-spinner")).toHaveCount(0);
      await expect.poll(() => runningSessionCount()).toBe(0);

      // 落库形态:前台是 agrctl 专属 kind;后台保留 local_bash(后台面板语义)。
      await expect.poll(() => agrctlOverlayCountOfKind(expectedKind)).toBe(overlaysBefore + 1);

      // 重载:卡片仍是同一张,步骤与最终文本都在。
      await page.reload();
      const reloaded = page.getByTestId("agent-spawn-card");
      await expect(reloaded).toHaveCount(1);
      await reloaded.locator('[aria-expanded]').first().click();
      await expect(reloaded.getByText("echo e2e-step-2").first()).toBeVisible();
      await expect(reloaded.getByText(new RegExp(`${ReplyPattern}.*${label}`))).toBeVisible();
    });
  }
});

type ProjectWailsWindow = {
  go: { app: { App: { ProjectAddMember(projectID: number, agentID: number): Promise<void> } } };
};
