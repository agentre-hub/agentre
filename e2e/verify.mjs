// Local real-verification launcher: start the formal desktop main with isolated state,
// leave an attached browser running, and drive it with e2e/drive.mjs.
import { execFileSync, spawn } from "node:child_process";
import { existsSync, mkdirSync, openSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  IsolationError,
  REPO_ROOT,
  assertIsolatedDataDir,
  assertRecordedSession,
  clearSession,
  isRecordedTargetLive,
  launchEnv,
  portListening,
  prepareDirs,
  readSession,
  resolveTarget,
  writeSession,
} from "./lib/target.mjs";
import { verificationBrowserArgs } from "./lib/browser.mjs";
import { reapOrphanVite, reapOwnPortHolders, waitForExit } from "./lib/procs.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, "..");

function parseArgs(argv) {
  const args = { command: argv[0], flags: {} };
  for (let i = 1; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--keep") args.flags.keep = true;
    else if (arg === "--wipe") args.flags.wipe = true;
    else if (arg === "--headed") args.flags.headed = true;
    else if (arg === "--no-browser") args.flags.noBrowser = true;
    else throw new Error(`unknown argument: ${arg}`);
  }
  return args;
}

function wailsBin() {
  if (process.env.WAILS) return process.env.WAILS;
  try {
    execFileSync(process.platform === "win32" ? "where" : "which", ["wails"], { stdio: "ignore" });
    return "wails";
  } catch {
    const gopath = execFileSync("go", ["env", "GOPATH"], { encoding: "utf8" }).trim();
    return join(gopath, "bin", "wails");
  }
}

async function urlReady(url) {
  try {
    const response = await fetch(url, { signal: AbortSignal.timeout(2000) });
    return response.ok || response.status === 404;
  } catch {
    return false;
  }
}

async function waitFor(check, { timeoutMs, everyMs = 500, onWait }) {
  const deadline = Date.now() + timeoutMs;
  let waited = 0;
  while (Date.now() < deadline) {
    if (await check()) return true;
    await new Promise((done) => setTimeout(done, everyMs));
    waited += everyMs;
    if (onWait && waited % 10_000 === 0) onWait(waited / 1000);
  }
  return false;
}

function tailLog(logFile, lines = 25) {
  if (!existsSync(logFile)) return "(no log yet)";
  return readFileSync(logFile, "utf8").split("\n").slice(-lines).join("\n");
}

function alive(pid) {
  if (!pid) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

async function up(flags) {
  const target = resolveTarget();
  const recorded = readSession();
  const portTaken = await portListening(target.devserverPort);
  let existing = null;
  if (recorded) {
    try {
      existing = assertRecordedSession(target, recorded);
    } catch (error) {
      if (portTaken) {
        console.error(`${error.message}. Refusing to adopt the process on :${target.devserverPort}.`);
        return 1;
      }
      clearSession();
    }
  }

  if (existing && isRecordedTargetLive(target, existing, portTaken, alive)) {
    console.log(`already up: ${target.baseURL}`);
    if (!existing.browserPid || !(await portListening(target.cdpPort))) {
      await attachBrowser(target, flags, existing);
    }
    printNext(target);
    return 0;
  }

  if (portTaken && !(await reclaimDevserverPort(target))) return 1;

  if (existing) {
    stopPid(existing.browserPid);
    stopPid(existing.appPid);
    clearSession();
    reapOrphanVite(repoRoot);
  }
  assertIsolatedDataDir(target.dataDir);
  prepareDirs(target, { wipe: !flags.keep });

  const distDir = join(repoRoot, "frontend", "dist");
  mkdirSync(distDir, { recursive: true });
  writeFileSync(join(distDir, ".keep"), "");

  const wailsArgs = ["dev", "-devserver", `localhost:${target.devserverPort}`];
  rmSync(target.logFile, { force: true });
  const logFd = openSync(target.logFile, "a");
  const app = spawn(wailsBin(), wailsArgs, {
    cwd: repoRoot,
    detached: true,
    stdio: ["ignore", logFd, logFd],
    env: launchEnv(target),
  });
  app.unref();

  console.log(
    `starting formal desktop (${["wails", ...wailsArgs].join(" ")}) — first run compiles Go + Vite`,
  );
  const ready = await waitFor(() => urlReady(target.baseURL), {
    timeoutMs: 300_000,
    onWait: (seconds) => console.log(`  … still building (${seconds}s)`),
  });
  if (!ready) {
    console.error(`app did not come up on ${target.baseURL} within 300s.\n${tailLog(target.logFile)}`);
    try {
      process.kill(-app.pid, "SIGTERM");
    } catch {
      // Already gone.
    }
    return 1;
  }

  const session = newSession(target, app.pid, !flags.headed);
  writeSession(session);
  try {
    await attachBrowser(target, flags, session);
  } catch (error) {
    stopPid(app.pid);
    clearSession();
    reapOrphanVite(repoRoot);
    throw error;
  }
  console.log(`up: ${target.baseURL}  data=${target.dataDir}`);
  printNext(target);
  return 0;
}

// 应用进程死掉不等于端口跟着放开:`kill -9` 掉 `wails dev`(比如桌面端崩溃)之后,它编出来
// 的应用二进制还在同一个进程组里活着、继续持有 devserver 端口,vite 则在另一个组里。此前这里
// 一律拒绝,于是必须先插一条 `verify-down` 才能重起 —— 而那正好毁掉「崩溃后重启」这类要验的
// 现场。只收走能被证明属于本 checkout 的孤儿:证不出来就还是拒绝,而且那条路上一个信号都不发。
async function reclaimDevserverPort(target) {
  const holders = await reapOwnPortHolders(target.devserverPort, repoRoot);
  if (!holders) {
    console.error(
      `:${target.devserverPort} is already serving an unrecorded process. ` +
        "Refusing to adopt or stop it; free the port and retry.",
    );
    return false;
  }
  console.log(
    `:${target.devserverPort} was still held by this checkout's orphaned ` +
      `${holders.map((holder) => `pid ${holder.pid}`).join(", ")}; reaped it`,
  );
  // 同一场崩溃留下的 vite 不占这个端口,但会占着它自己那个,顺带一起收走。
  reapOrphanVite(repoRoot);
  const freed = await waitFor(async () => !(await portListening(target.devserverPort)), {
    timeoutMs: 10_000,
    everyMs: 200,
  });
  if (!freed) {
    console.error(
      `:${target.devserverPort} is still held after reaping this checkout's orphans; ` +
        "free the port and retry.",
    );
    return false;
  }
  return true;
}

async function attachBrowser(target, flags, session) {
  session.headless = !flags.headed;
  session.browserPid = flags.noBrowser ? null : await startBrowser(target, flags);
  writeSession(session);
  return session;
}

function newSession(target, appPid, headless) {
  return {
    appPid,
    browserPid: null,
    headless,
    cdpPort: target.cdpPort,
    cdpURL: `http://127.0.0.1:${target.cdpPort}`,
    devserverPort: target.devserverPort,
    baseURL: target.baseURL,
    dataDir: target.dataDir,
    keychainDir: target.keychainDir,
    dbPath: target.dbPath,
    logFile: target.logFile,
    startedAt: new Date().toISOString(),
  };
}

async function startBrowser(target, flags) {
  const { chromium } = await import("@playwright/test");
  const executable = chromium.executablePath();
  if (!existsSync(executable)) {
    throw new Error(`Chromium is missing at ${executable}: run \`cd e2e && pnpm run setup\``);
  }
  const args = verificationBrowserArgs({
    cdpPort: target.cdpPort,
    browserDir: target.browserDir,
    headless: !flags.headed,
  });
  args.push(target.baseURL);

  const browser = spawn(executable, args, { detached: true, stdio: "ignore" });
  browser.unref();
  const ready = await waitFor(() => urlReady(`http://127.0.0.1:${target.cdpPort}/json/version`), {
    timeoutMs: 30_000,
  });
  if (!ready) {
    stopPid(browser.pid);
    throw new Error(`Chromium did not expose CDP on :${target.cdpPort}`);
  }
  return browser.pid;
}

function printNext(target) {
  console.log(
    [
      "",
      "drive it (one action per call, every call recorded):",
      "  node e2e/drive.mjs snapshot --scenario <slug>",
      '  node e2e/drive.mjs click "testid=new-chat-button" --scenario <slug>',
      "  node e2e/drive.mjs shot 01-after-click --scenario <slug>",
      "",
      `checkout: ${target.instanceId}`,
      `logs:     ${target.logFile}`,
      `db:       ${target.dbPath}`,
      "stop:     make verify-down",
    ].join("\n"),
  );
}

function stopPid(pid) {
  if (!pid) return;
  for (const candidate of [-pid, pid]) {
    try {
      process.kill(candidate, "SIGTERM");
      return;
    } catch {
      // Try the non-group PID next, or it has already exited.
    }
  }
}

function down(flags) {
  const target = resolveTarget();
  const recorded = readSession();
  let session = null;
  if (!recorded) {
    console.log("no verification session recorded; reaping leftovers anyway");
  } else {
    try {
      session = assertRecordedSession(target, recorded);
    } catch (error) {
      console.error(`${error.message}. Refusing to signal untrusted recorded PIDs.`);
      return 1;
    }
  }
  stopPid(session?.browserPid);
  stopPid(session?.appPid);
  clearSession();
  reapOrphanVite(repoRoot);
  if (flags.wipe) {
    assertIsolatedDataDir(target.dataDir);
    return wipe(target, session);
  }
  console.log(`down: formal desktop stopped (state kept at ${target.dataDir}; add --wipe to remove)`);
  return 0;
}

// 等被 SIGTERM 的进程真正退出再删目录。信号交出去到 Chromium 最后写完用户数据目录之间
// 还有一拍,`rmSync` 抢在前面就会偶发 ENOTEMPTY(browserDir 里还在生成文件)。进程组等
// 不到就在预算内放弃、照删不误——不能为了一个赖着不走的进程把 verify-down 挂死。
async function wipe(target, session) {
  await waitForExit(session?.browserPid);
  await waitForExit(session?.appPid);
  rmSync(target.dataDir, { recursive: true, force: true });
  rmSync(target.keychainDir, { recursive: true, force: true });
  rmSync(target.browserDir, { recursive: true, force: true });
  console.log("down: formal desktop stopped, isolated state wiped");
  return 0;
}

async function status() {
  const target = resolveTarget();
  const recorded = readSession();
  let session = null;
  let sessionError = "";
  if (recorded) {
    try {
      session = assertRecordedSession(target, recorded);
    } catch (error) {
      sessionError = error.message;
    }
  }
  const bridgeUp = await portListening(target.devserverPort);
  const cdpUp = await portListening(target.cdpPort);
  const dbSize = existsSync(target.dbPath) ? statSync(target.dbPath).size : 0;
  console.log(
    [
      `checkout: ${target.instanceId} — ${REPO_ROOT}`,
      "entry:    formal desktop main",
      `session:  ${session ? `started ${session.startedAt}` : sessionError ? `invalid (${sessionError})` : "none"}`,
      `app:      ${bridgeUp ? "up" : "down"} ${target.baseURL}${session?.appPid ? ` pid=${session.appPid}${alive(session.appPid) ? "" : " (gone)"}` : ""}`,
      `browser:  ${cdpUp ? "up" : "down"} cdp=:${target.cdpPort}${session?.browserPid ? ` pid=${session.browserPid}` : ""}${session ? (session.headless ? " headless" : " headed") : ""}`,
      `data:     ${target.dataDir}`,
      `db:       ${target.dbPath} (${dbSize} bytes)`,
      `keychain: ${target.keychainDir}`,
      `log:      ${target.logFile}`,
    ].join("\n"),
  );
  return session && bridgeUp && alive(session.appPid) ? 0 : 1;
}

async function main() {
  const { command, flags } = parseArgs(process.argv.slice(2));
  switch (command) {
    case "up":
      return up(flags);
    case "down":
      return down(flags);
    case "status":
      return status();
    default:
      console.error("usage: node verify.mjs up|down|status [--keep|--wipe] [--headed] [--no-browser]");
      return 2;
  }
}

main()
  .then((code) => process.exit(code))
  .catch((error) => {
    console.error(error instanceof IsolationError ? `${error.name}: ${error.message}` : error);
    process.exit(1);
  });
