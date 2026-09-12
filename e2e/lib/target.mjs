// The local-verification target contract shared by verify.mjs and drive.mjs.
// Verification always launches the formal desktop main; this module only supplies
// checkout-scoped storage, ports, the session handshake, and isolation guards.
import { createHash } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { connect, createServer } from "node:net";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

import { alive } from "./procs.mjs";

export class IsolationError extends Error {
  constructor(message) {
    super(message);
    this.name = "IsolationError";
  }
}

export const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..", "..");
export const INSTANCE_ID = createHash("sha1").update(REPO_ROOT).digest("hex").slice(0, 8);
export const INSTANCE_ROOT = join(tmpdir(), "agentre-verify", INSTANCE_ID);

const PORT_BLOCK_BASE = 34300;
const PORT_BLOCKS = 600;
const PORTS_PER_BLOCK = 2;
const blockStart =
  PORT_BLOCK_BASE + (parseInt(INSTANCE_ID.slice(0, 4), 16) % PORT_BLOCKS) * PORTS_PER_BLOCK;

const TARGET = {
  devserverPort: blockStart,
  cdpPort: blockStart + 1,
  dataDir: join(INSTANCE_ROOT, "data"),
  keychainDir: join(INSTANCE_ROOT, "keychain"),
  browserDir: join(INSTANCE_ROOT, "browser"),
  logFile: join(INSTANCE_ROOT, "webserver.log"),
};

export function productionRoots() {
  const roots = [];
  if (process.platform === "darwin") {
    roots.push(join(homedir(), "Library", "Application Support", "agentre"));
  } else if (process.platform === "win32") {
    for (const base of [process.env.APPDATA, process.env.LOCALAPPDATA]) {
      if (base) roots.push(join(base, "agentre"));
    }
  } else {
    roots.push(join(process.env.XDG_CONFIG_HOME || join(homedir(), ".config"), "agentre"));
  }
  return roots;
}

function within(dir, root) {
  return dir === root || dir.startsWith(root + sep);
}

export function assertIsolatedDataDir(dir) {
  const candidate = typeof dir === "string" ? resolve(dir.trim() || ".") : "";
  if (candidate === TARGET.dataDir) return candidate;

  for (const root of productionRoots()) {
    if (within(candidate, root)) {
      throw new IsolationError(
        `refusing to touch the installed app's data dir: ${candidate}\n` +
          "Verification only runs against its checkout-scoped throwaway directory.",
      );
    }
    if (within(candidate, `${root}-dev`)) {
      throw new IsolationError(
        `refusing to touch your own dev data dir: ${candidate}\n` +
          "`make dev` holds your sessions and projects; verification gets its own directory.",
      );
    }
  }
  throw new IsolationError(
    `not the sanctioned verification data dir for this checkout: ${candidate || "(empty)"}\n` +
      `Allowed: ${TARGET.dataDir}`,
  );
}

export function assertSanctionedURL(target, url) {
  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    throw new IsolationError(`not a URL: ${url}`);
  }
  const hostOK = parsed.hostname === "localhost" || parsed.hostname === "127.0.0.1";
  if (parsed.protocol !== "http:" || !hostOK || parsed.port !== String(target.devserverPort)) {
    throw new IsolationError(`refusing to drive ${url}: the verification target is only ${target.baseURL}`);
  }
  return parsed.toString();
}

export function resolveTarget() {
  return {
    ...TARGET,
    instanceId: INSTANCE_ID,
    baseURL: `http://localhost:${TARGET.devserverPort}`,
    dbPath: join(TARGET.dataDir, "agentre.db"),
  };
}

export function sessionPath() {
  return join(INSTANCE_ROOT, "session.json");
}

export function readSession() {
  const path = sessionPath();
  if (!existsSync(path)) return null;
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch {
    return null;
  }
}

export function assertRecordedSession(target, session) {
  if (!session || typeof session !== "object") {
    throw new IsolationError("verification session is missing or invalid");
  }
  const expected = {
    cdpPort: target.cdpPort,
    cdpURL: `http://127.0.0.1:${target.cdpPort}`,
    devserverPort: target.devserverPort,
    baseURL: target.baseURL,
    dataDir: target.dataDir,
    keychainDir: target.keychainDir,
    dbPath: target.dbPath,
    logFile: target.logFile,
  };
  for (const [name, value] of Object.entries(expected)) {
    if (session[name] !== value) {
      throw new IsolationError(`verification session ${name} does not belong to this checkout`);
    }
  }
  if (!Number.isInteger(session.appPid) || session.appPid <= 0) {
    throw new IsolationError("verification session app PID is invalid");
  }
  if (
    session.browserPid !== null &&
    (!Number.isInteger(session.browserPid) || session.browserPid <= 0)
  ) {
    throw new IsolationError("verification session browser PID is invalid");
  }
  if (typeof session.headless !== "boolean" || Number.isNaN(Date.parse(session.startedAt))) {
    throw new IsolationError("verification session metadata is invalid");
  }
  return session;
}

export function isRecordedTargetLive(target, session, bridgeUp, isAlive) {
  assertRecordedSession(target, session);
  return Boolean(bridgeUp && isAlive(session.appPid));
}

export function writeSession(session) {
  assertRecordedSession(resolveTarget(), session);
  mkdirSync(INSTANCE_ROOT, { recursive: true });
  writeFileSync(sessionPath(), `${JSON.stringify(session, null, 2)}\n`, { mode: 0o600 });
}

export function clearSession() {
  rmSync(sessionPath(), { force: true });
}

/**
 * wipe 是整条 verify 工具链里唯一会删数据的动作,而 target 是按 checkout 派生的:
 * 同一个 checkout 上任何一处调用 wipe,删的都是那台机器上**正在跑**的那份联调数据。
 * 所以删之前必须问一句「它还活着吗」——记着的会话里 appPid 还在,就拒绝,让人自己
 * 决定是先 `make verify-down` 还是换 `--keep`。
 *
 * 没有会话记录 / 记着的进程已经不在了,才是 wipe 的正常前提。
 */
export function assertWipeAllowed(session, isAlive = alive) {
  if (!session || typeof session !== "object") return;
  const pid = session.appPid;
  if (!Number.isInteger(pid) || pid <= 0) return;
  if (!isAlive(pid)) return;
  throw new IsolationError(
    `refusing to wipe a live verification target: app pid ${pid} is still running\n` +
      "Stop it first (`make verify-down`), or keep its state (`make verify-up VERIFY_FLAGS=--keep`).",
  );
}

export function prepareDirs(target, { wipe }) {
  assertIsolatedDataDir(target.dataDir);
  if (wipe) {
    assertWipeAllowed(readSession());
    rmSync(target.dataDir, { recursive: true, force: true });
    rmSync(target.keychainDir, { recursive: true, force: true });
    rmSync(target.browserDir, { recursive: true, force: true });
  }
  for (const dir of [target.dataDir, target.keychainDir, target.browserDir]) {
    mkdirSync(dir, { recursive: true, mode: 0o700 });
    if (process.platform !== "win32") chmodSync(dir, 0o700);
  }
}

export function launchEnv(target, parentEnv = process.env) {
  const env = { ...parentEnv };
  for (const name of Object.keys(env)) {
    if (name.startsWith("AGENTRE_E2E_") || name === "AGENTRE_DATA_DIR" || name === "AGENTRE_KEYCHAIN_DIR") {
      delete env[name];
    }
  }
  return {
    ...env,
    AGENTRE_DATA_DIR: target.dataDir,
    AGENTRE_ENV: "test",
    AGENTRE_KEYCHAIN_DIR: target.keychainDir,
    AGENTRE_PROXY_PORT: "0",
  };
}

export function portListening(port, host = "127.0.0.1", timeoutMs = 1500) {
  return new Promise((done) => {
    const socket = connect({ host, port });
    const finish = (ok) => {
      socket.destroy();
      done(ok);
    };
    socket.setTimeout(timeoutMs, () => finish(false));
    socket.once("connect", () => finish(true));
    socket.once("error", () => finish(false));
  });
}

export function portFree(port) {
  return new Promise((done) => {
    const server = createServer();
    server.once("error", () => done(false));
    server.listen(port, "127.0.0.1", () => server.close(() => done(true)));
  });
}

export function requireLiveSession() {
  const session = readSession();
  if (!session) {
    throw new IsolationError("no verification app is up in this checkout. Start one: make verify-up");
  }
  return assertRecordedSession(resolveTarget(), session);
}
