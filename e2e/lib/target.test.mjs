import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { existsSync, rmSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import {
  VERIFY_VIEWPORT,
  applyVerificationViewport,
  verificationBrowserArgs,
} from "./browser.mjs";
import {
  INSTANCE_ID,
  INSTANCE_ROOT,
  IsolationError,
  REPO_ROOT,
  assertIsolatedDataDir,
  assertRecordedSession,
  assertSanctionedURL,
  isRecordedTargetLive,
  productionRoots,
  launchEnv,
  prepareDirs,
  resolveTarget,
  sessionPath,
} from "./target.mjs";
import { waitForExit } from "./procs.mjs";

test("Given formal verification, when its target is resolved, then storage and ports belong only to this checkout", () => {
  const target = resolveTarget();
  for (const dir of [target.dataDir, target.keychainDir, target.browserDir]) {
    assert.ok(dir.startsWith(INSTANCE_ROOT), `${dir} must live under ${INSTANCE_ROOT}`);
  }
  assert.ok(INSTANCE_ROOT.startsWith(tmpdir()));
  assert.doesNotThrow(() => assertIsolatedDataDir(target.dataDir));
  assert.notEqual(target.devserverPort, 34115);
  assert.notEqual(target.cdpPort, 34115);
  assert.notEqual(target.devserverPort, target.cdpPort);
});

test("Given different worktrees, when target identities are derived, then they cannot share state", () => {
  assert.equal(INSTANCE_ID, createHash("sha1").update(REPO_ROOT).digest("hex").slice(0, 8));
  const other = createHash("sha1").update(`${REPO_ROOT}-worktree`).digest("hex").slice(0, 8);
  assert.notEqual(other, INSTANCE_ID);
  assert.ok(sessionPath().startsWith(INSTANCE_ROOT));
});

test("Given protected app roots, when verification resolves a data directory, then production, development, and arbitrary paths are rejected", () => {
  for (const root of productionRoots()) {
    assert.throws(() => assertIsolatedDataDir(root), IsolationError);
    assert.throws(() => assertIsolatedDataDir(join(root, "agentre.db")), IsolationError);
    assert.throws(() => assertIsolatedDataDir(`${root}-dev`), IsolationError);
  }
  assert.throws(() => assertIsolatedDataDir(join(homedir(), "somewhere-else")), IsolationError);
  assert.throws(() => assertIsolatedDataDir(""), IsolationError);
});

test("Given a recorded verification session, when ownership is checked, then every path/port must match this checkout and a stale PID cannot adopt a listening bridge", () => {
  const target = resolveTarget();
  const session = {
    appPid: 123,
    browserPid: 456,
    cdpPort: target.cdpPort,
    cdpURL: `http://127.0.0.1:${target.cdpPort}`,
    devserverPort: target.devserverPort,
    baseURL: target.baseURL,
    dataDir: target.dataDir,
    keychainDir: target.keychainDir,
    dbPath: target.dbPath,
    logFile: target.logFile,
    startedAt: new Date().toISOString(),
    headless: true,
  };

  assert.equal(assertRecordedSession(target, session), session);
  assert.equal(isRecordedTargetLive(target, session, true, () => true), true);
  assert.equal(isRecordedTargetLive(target, session, true, () => false), false);
  assert.equal(isRecordedTargetLive(target, session, false, () => true), false);

  for (const mutation of [
    { dataDir: join(homedir(), "real-data") },
    { dbPath: join(homedir(), "real.db") },
    { baseURL: "http://127.0.0.1:34115" },
    { appPid: 0 },
    { startedAt: null },
  ]) {
    assert.throws(() => assertRecordedSession(target, { ...session, ...mutation }), IsolationError);
  }
});

test("Given a live verification target, when a URL is checked, then only its own loopback bridge is accepted", () => {
  const target = resolveTarget();
  assert.doesNotThrow(() => assertSanctionedURL(target, `${target.baseURL}/`));
  assert.doesNotThrow(() =>
    assertSanctionedURL(target, `http://127.0.0.1:${target.devserverPort}/settings`),
  );
  assert.throws(() => assertSanctionedURL(target, "http://localhost:34115/"), IsolationError);
  assert.throws(() => assertSanctionedURL(target, "https://agentre.ai/"), IsolationError);
  assert.throws(() => assertSanctionedURL(target, "file:///etc/passwd"), IsolationError);
});

test("Given formal verification with stale E2E variables and missing isolated storage, when launch input is prepared, then private directories are created and E2E composition variables are absent", () => {
  const target = resolveTarget();
  for (const path of [target.dataDir, target.keychainDir, target.browserDir]) {
    rmSync(path, { recursive: true, force: true });
  }

  prepareDirs(target, { wipe: true });
  const env = launchEnv(target, {
    PATH: "/test/bin",
    AGENTRE_E2E_MANIFEST: "/tmp/stale-manifest",
    AGENTRE_E2E_REFRESH_TOKEN: "developer-secret",
  });

  assert.equal(env.PATH, "/test/bin");
  assert.equal(env.AGENTRE_DATA_DIR, target.dataDir);
  assert.equal(env.AGENTRE_KEYCHAIN_DIR, target.keychainDir);
  assert.equal(env.AGENTRE_E2E_MANIFEST, undefined);
  assert.equal(env.AGENTRE_E2E_REFRESH_TOKEN, undefined);
  for (const path of [target.dataDir, target.keychainDir, target.browserDir]) {
    assert.equal(existsSync(path), true);
  }
});

test("Given formal verification, when its browser and driven page are prepared, then evidence uses the established 1440x900 viewport in headless and headed modes", async () => {
  const calls = [];
  await applyVerificationViewport({
    setViewportSize(size) {
      calls.push(size);
    },
  });

  assert.deepEqual(VERIFY_VIEWPORT, { width: 1440, height: 900 });
  assert.deepEqual(calls, [{ width: 1440, height: 900 }]);

  const headless = verificationBrowserArgs({
    cdpPort: 34301,
    browserDir: "/tmp/agentre-browser",
    headless: true,
  });
  assert.ok(headless.includes("--window-size=1440,900"));
  assert.ok(headless.includes("--headless=new"));

  const headed = verificationBrowserArgs({
    cdpPort: 34301,
    browserDir: "/tmp/agentre-browser",
    headless: false,
  });
  assert.ok(headed.includes("--window-size=1440,900"));
  assert.ok(!headed.includes("--headless=new"));
});

// 一个装好 SIGTERM handler 之后才自报「ready」的子进程。用例必须先等到 ready 再发信号:
// 信号比 handler 先到,进程会当场按默认动作退出,用例就测不到「信号到了还要忙一拍」。
function spawnSignaledNode(handlerScript) {
  const child = spawn(
    process.execPath,
    [
      "-e",
      `${handlerScript}; process.stdout.write("ready\\n"); setInterval(() => {}, 1000);`,
    ],
    { detached: true, stdio: ["ignore", "pipe", "ignore"] },
  );
  return child;
}

async function ready(child) {
  await once(child.stdout, "data");
}

function killGroup(pid) {
  try {
    process.kill(-pid, "SIGKILL");
  } catch {
    // 已经退干净了。
  }
}

// Given 一个「信号到了还要忙一拍才退出」的进程, When `verify-down --wipe` 在删目录前等它,
// Then 只有它真的退出之后才交回 —— 不等就删正是 ENOTEMPTY 竞态的来源。
test("Given a signaled process, when the wipe waits for it, then it resolves only after the process is gone", async () => {
  const child = spawnSignaledNode('process.on("SIGTERM", () => setTimeout(() => process.exit(0), 300))');
  try {
    await ready(child);
    child.kill("SIGTERM");
    const started = Date.now();
    const exited = await waitForExit(child.pid, { timeoutMs: 5000, everyMs: 20 });
    assert.equal(exited, true, "进程退出后 waitForExit 必须交回 true");
    assert.ok(Date.now() - started >= 250, "waitForExit 不该在进程还没退出时就交回");
    assert.throws(() => process.kill(child.pid, 0), /ESRCH/);
  } finally {
    killGroup(child.pid);
  }
});

// Given 一个赖着不走(忽略 SIGTERM)的进程, When 等待有预算, Then 预算到了就交回 false,
// 而不是把 verify-down 挂死。
test("Given a process that ignores SIGTERM, when the wait runs out of budget, then it gives up instead of hanging", async () => {
  const child = spawnSignaledNode('process.on("SIGTERM", () => {})');
  try {
    await ready(child);
    child.kill("SIGTERM");
    const started = Date.now();
    const exited = await waitForExit(child.pid, { timeoutMs: 200, everyMs: 20 });
    assert.equal(exited, false);
    assert.ok(Date.now() - started >= 180, "预算没到就交回 false 等于没等");
  } finally {
    killGroup(child.pid);
  }
});
