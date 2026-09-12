import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { createAppOverlay } from "./lib/app-overlay.mjs";
import { startFakeSyncServer } from "./lib/fake-sync-server.mjs";
import { runNodeGuards } from "./lib/guard-suite.mjs";
import {
  ProcessSupervisor,
  REPO_ROOT,
  appEnvironment,
  assertRemoteDeviceCredentialPersisted,
  createRunContext,
  fakePeerEnvironment,
  generateWailsBindings,
  playwrightEnvironment,
  preserveFailureArtifacts,
  spawnLogged,
  waitForURL,
} from "./lib/run-context.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const require = createRequire(import.meta.url);
const playwrightCli = require.resolve("@playwright/test/cli");
const artifactRoot = join(here, "artifacts");

function childResult(child) {
  return new Promise((resolve) => {
    child.once("error", (error) => resolve({ code: 1, signal: null, error }));
    child.once("exit", (code, signal) => resolve({ code: code ?? 1, signal, error: null }));
  });
}

function viteCommand(run) {
  return [
    "--dir",
    "frontend",
    "exec",
    "vite",
    "--host",
    "127.0.0.1",
    "--port",
    String(run.vitePort),
  ];
}

function fakePeerCommand(run) {
  return ["run", "./e2e/cmd/fake-peer", "-ready-file", run.remoteReadyPath];
}

async function waitForFile(path, { timeoutMs = 120_000, intervalMs = 100 } = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (existsSync(path)) return;
    await new Promise((resolveWait) => setTimeout(resolveWait, intervalMs));
  }
  throw new Error(`timed out waiting for ${path}`);
}

function appCommand(run) {
  return [
    "dev",
    "-m",
    "-nosyncgomod",
    "-skipbindings",
    "-nogorebuild",
    "-frontenddevserverurl",
    run.viteURL,
    "-devserver",
    `127.0.0.1:${run.port}`,
  ];
}

async function main() {
  await runNodeGuards();

  const run = await createRunContext();
  run.remoteRecorderPath = join(run.logsDir, "fake-remote-recorder.json");
  run.remoteReadyPath = join(run.runRoot, "fake-remote-ready.json");
  const supervisor = new ProcessSupervisor();
  let fakeSync;
  let exitCode = 1;
  let failure;
  let interrupted = false;
  let artifactDir;
  let supervisorStopError;

  const stopSupervisedProcesses = async () => {
    try {
      await supervisor.stopAll();
    } catch (error) {
      supervisorStopError ??= error;
    }
  };
  const stopForSignal = async (signal) => {
    interrupted = true;
    failure = new Error(`runner received ${signal}`);
    await stopSupervisedProcesses();
  };
  const handlers = new Map(
    ["SIGINT", "SIGTERM"].map((signal) => {
      const handler = () => void stopForSignal(signal);
      process.once(signal, handler);
      return [signal, handler];
    }),
  );

  try {
    await generateWailsBindings(run);

    const syncIdentity = {
      serverURL: "",
      userID: 7001,
      deviceID: 7101,
      // 「来源机器」在契约上是**指纹**，不是 server 的数值设备主键（规格
      // 2026-08-27-schema-overhaul 决策 14）。
      peerFingerprint: "sha256:e2e-peer-7202",
      deviceFingerprint: `sha256:${run.token}`,
      refreshToken: `e2e-refresh-${run.token.slice(16, 48)}`,
    };
    fakeSync = await startFakeSyncServer({
      controlToken: run.controlToken,
      identity: {
        userId: syncIdentity.userID,
        deviceId: syncIdentity.deviceID,
        fingerprint: syncIdentity.deviceFingerprint,
        refreshToken: syncIdentity.refreshToken,
      },
    });
    syncIdentity.serverURL = fakeSync.url;
    run.syncIdentity = syncIdentity;
    run.env.AGENTRE_E2E_SERVER_URL = syncIdentity.serverURL;
    run.env.AGENTRE_E2E_SERVER_USER_ID = String(syncIdentity.userID);
    run.env.AGENTRE_E2E_DEVICE_ID = String(syncIdentity.deviceID);
    run.env.AGENTRE_E2E_DEVICE_FINGERPRINT = syncIdentity.deviceFingerprint;
    run.env.AGENTRE_E2E_REFRESH_TOKEN = syncIdentity.refreshToken;

    const remoteIdentity = {
      instanceUUID: `e2e-fake-peer-${run.token.slice(0, 24)}`,
      deviceToken: `e2e-peer-token-${run.token.slice(8, 48)}`,
      url: "",
      controlURL: "",
      daemonFingerprint: "",
    };
    run.env.AGENTRE_E2E_REMOTE_INSTANCE_UUID = remoteIdentity.instanceUUID;
    run.env.AGENTRE_E2E_REMOTE_DEVICE_TOKEN = remoteIdentity.deviceToken;
    const fakePeer = supervisor.track(
      spawnLogged("go", fakePeerCommand(run), {
        cwd: REPO_ROOT,
        env: fakePeerEnvironment(run),
        logPath: join(run.logsDir, "fake-peer.log"),
      }),
    );
    const fakePeerExit = childResult(fakePeer);
    await Promise.race([
      waitForFile(run.remoteReadyPath),
      fakePeerExit.then((result) => {
        throw new Error(`fake remote peer exited before readiness (code ${result.code})`);
      }),
    ]);
    const remoteReady = JSON.parse(readFileSync(run.remoteReadyPath, "utf8"));
    if (!String(remoteReady.url ?? "").startsWith("ws://127.0.0.1:") ||
        !String(remoteReady.controlUrl ?? "").startsWith("http://127.0.0.1:") ||
        !String(remoteReady.daemonFingerprint ?? "").startsWith("sha256:")) {
      throw new Error("fake remote peer returned unsafe readiness data");
    }
    Object.assign(remoteIdentity, {
      url: remoteReady.url,
      controlURL: remoteReady.controlUrl,
      daemonFingerprint: remoteReady.daemonFingerprint,
    });
    run.remoteIdentity = remoteIdentity;
    run.env.AGENTRE_E2E_REMOTE_PEER_URL = remoteIdentity.url;
    run.env.AGENTRE_E2E_REMOTE_DAEMON_FINGERPRINT = remoteIdentity.daemonFingerprint;

    const vite = supervisor.track(
      spawnLogged("pnpm", viteCommand(run), {
        cwd: REPO_ROOT,
        env: process.env,
        logPath: join(run.logsDir, "vite.log"),
      }),
    );
    const viteExit = childResult(vite);
    await Promise.race([
      waitForURL(run.viteURL),
      viteExit.then((result) => {
        throw new Error(`Vite exited before readiness (code ${result.code})`);
      }),
    ]);

    const overlayPath = createAppOverlay(REPO_ROOT, run.runRoot);
    const app = supervisor.track(
      spawnLogged("wails", appCommand(run), {
        cwd: join(REPO_ROOT, "e2e", "app"),
        env: {
          ...appEnvironment(run),
          GOFLAGS: `${process.env.GOFLAGS ?? ""} -overlay=${overlayPath}`.trim(),
        },
        logPath: run.appLog,
      }),
    );
    const appExit = childResult(app);
    await Promise.race([
      waitForURL(run.baseURL),
      appExit.then((result) => {
        throw new Error(`dedicated E2E app exited before readiness (code ${result.code})`);
      }),
    ]);
    if (interrupted) throw failure;

    const playwright = supervisor.track(
      spawn(
        process.execPath,
        [playwrightCli, "test", ...process.argv.slice(2)],
        {
          cwd: here,
          stdio: "inherit",
          env: {
            ...playwrightEnvironment(run),
            AGENTRE_E2E_REMOTE_PEER_CONTROL_URL: remoteIdentity.controlURL,
            AGENTRE_E2E_REMOTE_DAEMON_FINGERPRINT: remoteIdentity.daemonFingerprint,
          },
          detached: process.platform !== "win32",
        },
      ),
    );
    const result = await childResult(playwright);
    if (result.code !== 0) {
      throw new Error(
        result.signal
          ? `Playwright terminated by ${result.signal}`
          : `Playwright failed with exit ${result.code}`,
      );
    }
    await assertRemoteDeviceCredentialPersisted(run);
    exitCode = 0;
  } catch (error) {
    failure = error;
  } finally {
    if (run.remoteIdentity) {
      try {
        const response = await fetch(`${run.remoteIdentity.controlURL}/snapshot`, {
          headers: { authorization: `Bearer ${run.controlToken}` },
        });
        if (response.ok) {
          writeFileSync(run.remoteRecorderPath, `${JSON.stringify(await response.json(), null, 2)}\n`, {
            mode: 0o600,
          });
        }
      } catch {
        // The peer may already have been terminated by a signal/failure path.
      }
    }
    await stopSupervisedProcesses();
    if (supervisorStopError) {
      exitCode = 1;
      failure = failure
        ? new Error(`${failure.message}\n${supervisorStopError.message}`)
        : supervisorStopError;
    }
    if (fakeSync) {
      writeFileSync(run.syncRecorderPath, `${JSON.stringify(fakeSync.snapshot(), null, 2)}\n`, {
        mode: 0o600,
      });
      await fakeSync.close();
    }
    for (const [signal, handler] of handlers) process.removeListener(signal, handler);

    if (exitCode === 0) {
      await run.remove();
    } else {
      artifactDir = await preserveFailureArtifacts(run, artifactRoot);
    }
  }

  if (failure) console.error(failure.message);
  if (artifactDir) console.error(`sanitized E2E artifacts preserved at ${artifactDir}`);
  process.exit(exitCode);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
