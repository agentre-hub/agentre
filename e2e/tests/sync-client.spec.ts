import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

import {
  outboundQueueCountForSyncID,
  projectByName,
} from "../fixtures/db";
import {
  configureSyncFaults,
  fakeSyncRequests,
  projectPushItems,
  queueRemoteSyncItems,
} from "../fixtures/sync-fake";

const runID = process.env.AGENTRE_E2E_RUN_ID;
if (!runID) throw new Error("sync client smoke must be started by the E2E runner");
const localName = `sync-local-${runID}`;
const remoteName = `sync-remote-${runID}`;
const rejectedName = `sync-rejected-${runID}`;
const invalidName = `sync-invalid-${runID}`;

async function createProject(page: Page, name: string) {
  const path = join(process.env.AGENTRE_DATA_DIR!, "projects", name);
  mkdirSync(path, { recursive: true });
  // 「项目」不再是一个导航项 —— 它是会话索引的一个分组维度（规格决策 1）。
  // 新建项目降为侧栏 ＋ 下拉里的次级项（决策 11）。
  await page.getByTestId("new-chat-button").click();
  await page.getByTestId("project-create-trigger").click();
  const dialog = page.getByRole("dialog");
  await dialog.getByTestId("project-create-path").fill(path);
  await dialog.getByTestId("project-create-name").fill(name);
  await dialog.getByTestId("project-create-submit").click();
  await expect(dialog).toBeHidden();
  await expect(page.getByText(name, { exact: true }).first()).toBeVisible();
}

async function syncNow(page: Page) {
  await page.evaluate(() => (window as never as WailsWindow).go.app.App.SyncNow());
}

async function syncStatus(page: Page) {
  return page.evaluate(() => (window as never as WailsWindow).go.app.App.SyncStatus());
}

type WailsWindow = {
  go: {
    app: {
      App: {
        SyncNow(): Promise<void>;
        SyncStatus(): Promise<{
          Enabled: boolean;
          AccountID: number;
          Cursor: number;
          PendingCount: number;
          LastError: string;
        }>;
      };
    };
  };
};

test.describe.serial("sync client smoke", () => {
  test("Given a logged-in isolated identity, when a project is created and a peer project is queued, then real push/pull carries identity, version/cursor, payload and converges in UI plus SQLite", async ({ page }) => {
    await page.goto("/");
    const initialStatus = await syncStatus(page);
    expect(initialStatus.Enabled).toBe(true);
    expect(initialStatus.AccountID).toBeGreaterThan(0);

    await createProject(page, localName);
    const local = await expect.poll(() => projectByName(localName)).not.toBeUndefined();
    void local;

    const pushed = await expect.poll(async () => {
      const matches = projectPushItems(await fakeSyncRequests(), localName);
      return matches.length > 0 ? matches[0] : undefined;
    }).not.toBeUndefined();
    void pushed;
    const localPush = projectPushItems(await fakeSyncRequests(), localName)[0];
    expect(localPush.request.identity).toMatchObject({
      userId: Number(process.env.AGENTRE_E2E_SYNC_USER_ID),
      deviceId: Number(process.env.AGENTRE_E2E_SYNC_DEVICE_ID),
      fingerprint: process.env.AGENTRE_E2E_SYNC_DEVICE_FINGERPRINT,
    });
    expect(localPush.item.sync_id).not.toBe("");
    expect(localPush.item.base_version).toBe(0);
    expect(localPush.item.updated_at).toBeGreaterThan(0);
    expect(localPush.item.payload).toMatchObject({ name: localName, sort_order: expect.any(Number) });
    expect(localPush.item.payload).not.toHaveProperty("path");

    const remoteSyncID = `remote-project-${runID}`;
    await queueRemoteSyncItems([
      {
        kind: "project",
        sync_id: remoteSyncID,
        origin_fingerprint: process.env.AGENTRE_E2E_SYNC_PEER_FINGERPRINT ?? "",
        payload: {
          name: remoteName,
          icon: "folder",
          color: "agent-2",
          description: "remote peer",
          sort_order: 9,
        },
      },
    ]);
    await syncNow(page);

    await expect.poll(() => projectByName(remoteName)?.sync_id).toBe(remoteSyncID);
    await page.bringToFront();
    await expect(page.getByText(remoteName, { exact: true }).first()).toBeVisible();
    expect(projectByName(remoteName)).toMatchObject({
      sync_id: remoteSyncID,
      sync_version: expect.any(Number),
      local_path_missing: 1,
    });
    const pulls = (await fakeSyncRequests()).filter((request) => request.path === "/v1/sync/pull");
    expect(pulls.some((request) => Number(request.query.cursor) >= initialStatus.Cursor)).toBe(true);
  });

  test("Given auth rejection or invalid sync responses, when local projects are created, then the existing object remains, queued work is retained, and the existing sync error contract is observable", async ({ page }) => {
    await page.goto("/");

    await configureSyncFaults({ push401: 2 });
    await createProject(page, rejectedName);
    const rejected = await expect.poll(() => projectByName(rejectedName)).not.toBeUndefined();
    void rejected;
    const rejectedRow = projectByName(rejectedName)!;
    await expect.poll(() => outboundQueueCountForSyncID(rejectedRow.sync_id)).toBeGreaterThan(0);
    await expect.poll(async () => (await syncStatus(page)).LastError).not.toBe("");
    expect(projectByName(rejectedName)?.name).toBe(rejectedName);

    await configureSyncFaults({ invalidPush: 1 });
    await createProject(page, invalidName);
    const invalidRow = await expect.poll(() => projectByName(invalidName)).not.toBeUndefined();
    void invalidRow;
    const invalid = projectByName(invalidName)!;
    await expect.poll(() => outboundQueueCountForSyncID(invalid.sync_id)).toBeGreaterThan(0);
    const failedStatus = await expect.poll(async () => syncStatus(page)).toMatchObject({
      Enabled: true,
      PendingCount: expect.any(Number),
    });
    void failedStatus;
    expect((await syncStatus(page)).PendingCount).toBeGreaterThan(0);
    expect((await syncStatus(page)).LastError).not.toBe("");
    expect(projectByName(invalidName)?.name).toBe(invalidName);

    await page.getByTestId("nav-settings").click();
    await page.getByTestId("settings-nav-sync").click();
    await expect(page.locator('[data-slot="sync-status-card"]')).toContainText(
      (await syncStatus(page)).LastError,
    );
  });
});
