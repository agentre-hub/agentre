import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { existsSync, statSync } from "node:fs";
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
  assertWipeAllowed,
  isRecordedTargetLive,
  portListening,
  productionRoots,
  launchEnv,
  prepareDirs,
  resolveTarget,
  sessionPath,
} from "./target.mjs";
import {
  describePid,
  orphanProcessesOnPort,
  reapOrphanVite,
  reapOwnPortHolders,
  runsCheckoutVite,
  waitForExit,
} from "./procs.mjs";

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

// 这条用例**不许碰 wipe**。它验的是 `launchEnv` 的环境变量清洗与 `prepareDirs` 的
// 目录落地，两者都不需要先删数据；而 `resolveTarget()` 给的是这个 checkout 的**真**
// target，`prepareDirs(…, { wipe: true })` 会把另一个会话正在跑的联调数据目录删掉
// (真发生过：agentre.db 被清空,app 还活着,之后全表 `no such table`)。
// wipe 分支自己的拒绝语义由下一条用例覆盖,那条是纯函数,不落盘。
test("Given formal verification with stale E2E variables, when launch input is prepared, then private directories exist and E2E composition variables are absent", () => {
  const target = resolveTarget();

  prepareDirs(target, { wipe: false });
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
    assert.equal(statSync(path).mode & 0o777, 0o700);
  }
});

// Given 这个 checkout 的 target 上还记着一个活着的会话, When 有人要求 wipe,
// Then 拒绝而不是把它的数据删掉 —— `prepareDirs` 的 wipe 分支此前完全不查存活,
// 于是 `pnpm run test:guards` 一跑就清空了另一个会话正在用的 agentre.db。
test("Given a live recorded verification session, when a wipe is requested, then it is refused instead of deleting the running target's state", () => {
  const target = resolveTarget();
  const session = {
    appPid: 4242,
    browserPid: 4243,
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

  assert.throws(() => assertWipeAllowed(session, () => true), IsolationError);
  assert.throws(
    () => assertWipeAllowed(session, () => true),
    /verify-down/,
    "拒绝要告诉人怎么往下走",
  );
  // 记着的进程已经不在了 / 压根没有会话 —— 这才是 wipe 的正常前提。
  assert.doesNotThrow(() => assertWipeAllowed(session, () => false));
  assert.doesNotThrow(() => assertWipeAllowed(null, () => true));
  assert.doesNotThrow(() =>
    assertWipeAllowed({ ...session, appPid: 0 }, () => true),
  );
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

// ── 孤儿 vite 的回收边界 ─────────────────────────────────────────────────────
//
// `vite` 是 `vitest` 的子串。旧判据 `pkill -f "<repo>/frontend.*vite"` 因此必然命中同一个
// checkout 里正在跑的 `pnpm test`(实测:一个命令行为
// `node <repo>/frontend/node_modules/.bin/../vitest/vitest.mjs run` 的进程会被 `pgrep -f`
// 那条模式列出来),于是每一次 `make verify-down` 都会当场打死别人的测试。
//
// 新判据只认「解析之后落在本 checkout frontend 里的 vite 可执行文件」:先按可执行名筛
// (vitest / vite-node / vite.config.ts 都不是 vite),再按路径确认归属。

const FRONTEND = join(REPO_ROOT, "frontend");

test("Given processes whose command lines merely contain \"vite\", when the orphan reaper decides, then only this checkout's real vite dev server matches", () => {
  const reap = {
    "pnpm 的 .bin 跳板(线上实测形状)": {
      command: `node ${FRONTEND}/node_modules/.bin/../vite/bin/vite.js`,
      cwd: FRONTEND,
    },
    "pnpm 虚拟 store 里的真身": {
      command: `node ${FRONTEND}/node_modules/.pnpm/vite@7.1.0/node_modules/vite/bin/vite.js --port 5173`,
      cwd: FRONTEND,
    },
    "相对路径起的 vite(只有靠 cwd 才解析得出归属)": {
      command: "node ./node_modules/.bin/../vite/bin/vite.js --config vite.config.ts",
      cwd: FRONTEND,
    },
  };
  for (const [shape, proc] of Object.entries(reap)) {
    assert.equal(runsCheckoutVite(proc, REPO_ROOT), true, `${shape}: 真 vite 漏掉就会一直占着端口`);
  }

  const spare = {
    "vitest 的 .bin 跳板(线上实测形状)": {
      command: `node ${FRONTEND}/node_modules/.bin/vitest run`,
      cwd: FRONTEND,
    },
    "vitest 的 mjs 入口": {
      command: `node ${FRONTEND}/node_modules/.bin/../vitest/vitest.mjs run`,
      cwd: FRONTEND,
    },
    "vitest 的 worker(线上实测形状)": {
      command: `node --require ${FRONTEND}/node_modules/vitest/suppress-warnings.cjs ${FRONTEND}/node_modules/vitest/dist/workers/forks.js`,
      cwd: FRONTEND,
    },
    "vitest --ui": {
      command: `node ${FRONTEND}/node_modules/vitest/vitest.mjs --ui`,
      cwd: FRONTEND,
    },
    "vite-node": {
      command: `node ${FRONTEND}/node_modules/vite-node/vite-node.mjs`,
      cwd: FRONTEND,
    },
    "只是命令行里提到了 vite 配置": {
      command: `node ${FRONTEND}/node_modules/foo/bar.js --config ${FRONTEND}/vite.config.ts`,
      cwd: FRONTEND,
    },
    "路径只是以本 checkout 开头的隔壁 worktree": {
      command: `node ${REPO_ROOT}-worktree/frontend/node_modules/vite/bin/vite.js`,
      cwd: `${REPO_ROOT}-worktree/frontend`,
    },
    "别人仓库的 vite": {
      command: "node /Users/somebody/other/frontend/node_modules/vite/bin/vite.js",
      cwd: "/Users/somebody/other/frontend",
    },
    "相对路径,但 cwd 不在本 checkout 里": {
      command: "node ./node_modules/vite/bin/vite.js",
      cwd: tmpdir(),
    },
    "连 cwd 都问不出来的相对路径": {
      command: "node ./node_modules/vite/bin/vite.js",
      cwd: "",
    },
  };
  for (const [shape, proc] of Object.entries(spare)) {
    assert.equal(runsCheckoutVite(proc, REPO_ROOT), false, `${shape}: 不是本 checkout 的 vite,一根汗毛都不能动`);
  }
});

// Given 本 checkout 的 vite dev server 与 vitest 同时在跑, When 回收孤儿 vite,
// Then 信号只发给 vite —— 这就是被 `make verify-down` 打死过的那个 `pnpm test`。
test("Given this checkout's vite beside a running vitest, when leftovers are reaped, then the signal reaches vite alone", () => {
  const processes = [
    { pid: 11, command: `node ${FRONTEND}/node_modules/.bin/../vite/bin/vite.js` },
    { pid: 12, command: `node ${FRONTEND}/node_modules/.bin/vitest run` },
    { pid: 13, command: `node ${FRONTEND}/node_modules/vitest/dist/workers/forks.js` },
    { pid: 14, command: "node /Users/somebody/other/frontend/node_modules/vite/bin/vite.js" },
  ];
  const cwds = { 11: FRONTEND, 12: FRONTEND, 13: FRONTEND, 14: "/Users/somebody/other/frontend" };
  const killed = [];

  const reaped = reapOrphanVite(REPO_ROOT, {
    list: () => processes,
    describe: (pid) => ({ pid, command: "", cwd: cwds[pid] }),
    kill: (pid) => killed.push(pid),
  });

  assert.deepEqual(killed, [11]);
  assert.deepEqual(reaped, [11]);
});

// 上面用注入的进程表钉判据,这里用**真** `ps` 枚举钉探针:判据再对,枚举读不出完整命令行
// 也白搭。动手那一下仍然是假的 —— 守卫用例不许对本机真进程发信号。
function spawnDecoy(t, marker) {
  const child = spawn(
    process.execPath,
    ["-e", 'process.stdout.write("ready\\n"); setInterval(() => {}, 1000);', marker],
    { cwd: REPO_ROOT, detached: true, stdio: ["ignore", "pipe", "ignore"] },
  );
  t.after(() => killGroup(child.pid));
  return child;
}

test("Given a real vitest of this checkout beside a real vite, when the reaper enumerates this machine, then vitest is never selected", async (t) => {
  const vite = spawnDecoy(t, join(FRONTEND, "node_modules", ".bin", "..", "vite", "bin", "vite.js"));
  const vitest = spawnDecoy(t, join(FRONTEND, "node_modules", ".bin", "..", "vitest", "vitest.mjs"));
  await ready(vite);
  await ready(vitest);

  const killed = [];
  const reaped = reapOrphanVite(REPO_ROOT, { kill: (pid) => killed.push(pid) });

  assert.equal(killed.includes(vite.pid), true, "真 vite 必须被枚举出来");
  assert.equal(killed.includes(vitest.pid), false, "vitest 被当成 vite 打死过一次,不许再有第二次");
  assert.deepEqual(reaped, killed);
  assert.doesNotThrow(() => process.kill(vitest.pid, 0), "vitest 必须还活着");
});

// ── devserver 端口上的孤儿 ──────────────────────────────────────────────────
//
// `kill -9` 掉 `make verify-status` 记的 app pid(模拟桌面端崩溃)之后端口并不放开:实测
// 占着它的是编译出来的 `build/bin/Agentre.app/Contents/MacOS/Agentre`,`wails dev` 只是它的
// 父进程。于是 `verify-up` 撞上「端口被没记录在案的进程占着」直接拒绝,必须先插一条
// `verify-down` 才能重起 —— 而那恰好毁掉要验的「崩溃后重启」。
//
// 判据只认证据确凿:命令行里有本 checkout 的绝对路径,且工作目录也在本 checkout 里面。
// 任何一条判不准(拿不到进程信息、混着陌生进程、平台上没有 lsof)就维持拒绝,一个信号都不发。
function heldBy(entries) {
  return {
    listeners: () => entries.map((entry) => entry.pid),
    describe: (pid) => {
      const entry = entries.find((candidate) => candidate.pid === pid);
      // 命令行/工作目录读不出来的进程,操作系统给的就是「说不上来」。
      return entry && entry.command && entry.cwd ? entry : null;
    },
  };
}

const OWN_APP_BINARY = {
  pid: 73367,
  command: join(REPO_ROOT, "build", "bin", "Agentre.app", "Contents", "MacOS", "Agentre"),
  cwd: REPO_ROOT,
};
const OWN_VITE = {
  pid: 73263,
  command: `node ${join(REPO_ROOT, "frontend", "node_modules", ".bin", "..", "vite", "bin", "vite.js")}`,
  cwd: join(REPO_ROOT, "frontend"),
};

test("Given the devserver port held by this checkout's own orphans, when ownership is probed, then every holder is named", () => {
  assert.deepEqual(orphanProcessesOnPort(34616, REPO_ROOT, heldBy([OWN_APP_BINARY])), [OWN_APP_BINARY]);
  assert.deepEqual(orphanProcessesOnPort(34616, REPO_ROOT, heldBy([OWN_APP_BINARY, OWN_VITE])), [
    OWN_APP_BINARY,
    OWN_VITE,
  ]);
});

test("Given a port holder this checkout cannot prove it owns, when ownership is probed, then it refuses instead of guessing", () => {
  const cases = {
    "没人在监听": [],
    "路径只是以本 checkout 开头的隔壁 worktree": [
      { pid: 1, command: `${REPO_ROOT}-worktree/build/bin/Agentre`, cwd: `${REPO_ROOT}-worktree` },
    ],
    "完全是另一个仓库": [
      {
        pid: 2,
        command: "node /Users/somebody/other-repo/frontend/node_modules/vite/bin/vite.js",
        cwd: "/Users/somebody/other-repo/frontend",
      },
    ],
    "命令行里有我们的路径,但工作目录是别人的": [
      { pid: 3, command: `node ${FRONTEND}/vite.js`, cwd: tmpdir() },
    ],
    "工作目录是我们的,但命令行里没有我们的任何东西": [
      { pid: 4, command: "nc -l 34616", cwd: REPO_ROOT },
    ],
    "操作系统不肯描述的进程": [{ pid: 5, command: "", cwd: "" }],
    "我们的一个,外加一个陌生进程": [
      OWN_VITE,
      { pid: 6, command: "node /Users/somebody/other-repo/server.js", cwd: "/Users/somebody/other-repo" },
    ],
  };
  for (const [why, entries] of Object.entries(cases)) {
    const probes = heldBy(entries);
    assert.equal(orphanProcessesOnPort(34616, REPO_ROOT, probes), null, `${why}: 判不准就必须拒绝`);
  }
});

function spawnPortHolder(t, { cwd, marker }) {
  const code =
    'require("net").createServer().listen(0, "127.0.0.1", function () {' +
    ' process.stdout.write(String(this.address().port) + "\\n"); });';
  const child = spawn(process.execPath, ["-e", code, marker], {
    cwd,
    detached: true,
    stdio: ["ignore", "pipe", "ignore"],
  });
  t.after(() => killGroup(child.pid));
  return child;
}

async function listeningPortOf(child) {
  const [chunk] = await once(child.stdout, "data");
  return Number(String(chunk).trim());
}

function probeAvailable() {
  const self = describePid(process.pid);
  return Boolean(self && self.command && self.cwd);
}

test("Given a real orphan of this checkout holding a loopback port, when the launcher reclaims it, then the OS probe names it and the port comes back", async (t) => {
  if (!probeAvailable()) {
    t.skip("lsof/ps 探针在本机不可用,端口归属判据无法验证");
    return;
  }
  const child = spawnPortHolder(t, {
    cwd: REPO_ROOT,
    marker: join(REPO_ROOT, "build", "bin", "Agentre.app", "Contents", "MacOS", "Agentre"),
  });
  const port = await listeningPortOf(child);
  assert.equal(await portListening(port), true);

  assert.deepEqual(
    orphanProcessesOnPort(port, REPO_ROOT)?.map((holder) => holder.pid),
    [child.pid],
  );

  const reaped = await reapOwnPortHolders(port, REPO_ROOT);
  assert.deepEqual(reaped?.map((holder) => holder.pid), [child.pid]);
  assert.throws(() => process.kill(child.pid, 0), /ESRCH/);
  assert.equal(await portListening(port), false);
});

test("Given a real process from outside this checkout holding the port, when the launcher looks at it, then it is left running", async (t) => {
  if (!probeAvailable()) {
    t.skip("lsof/ps 探针在本机不可用,端口归属判据无法验证");
    return;
  }
  const child = spawnPortHolder(t, { cwd: tmpdir(), marker: "somebody-elses-server" });
  const port = await listeningPortOf(child);

  assert.equal(orphanProcessesOnPort(port, REPO_ROOT), null);
  assert.equal(await reapOwnPortHolders(port, REPO_ROOT), null);
  assert.doesNotThrow(() => process.kill(child.pid, 0), "不属于本 checkout 的进程一根汗毛都不能动");
  assert.equal(await portListening(port), true);
});
