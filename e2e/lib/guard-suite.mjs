import { spawn } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const e2eRoot = resolve(here, "..");

export const AUTOMATED_GUARD_TESTS = [
  "lib/run-context.test.mjs",
  "lib/app-overlay.test.mjs",
  "lib/fake-sync-server.test.mjs",
  "lib/current-contract.test.mjs",
  "lib/browser.test.mjs",
  // 最后这条守的是清单本身：磁盘上的 *.test.mjs 与清单必须互相包含。
  // `lib/browser.test.mjs` 就是漏登记的那种情形 —— 文件在、断言是绿的、从没跑过。
  "lib/guard-suite.test.mjs",
];

export const FULL_GUARD_TESTS = [
  ...AUTOMATED_GUARD_TESTS,
  "lib/target.test.mjs",
];

export function runNodeGuards({
  tests = AUTOMATED_GUARD_TESTS,
  cwd = e2eRoot,
  spawnProcess = spawn,
} = {}) {
  return new Promise((resolveRun, reject) => {
    const child = spawnProcess(process.execPath, ["--test", ...tests], {
      cwd,
      stdio: "inherit",
    });
    child.once("error", reject);
    child.once("exit", (code, signal) => {
      if (code === 0) {
        resolveRun();
        return;
      }
      reject(
        new Error(
          signal
            ? `runner isolation tests terminated by ${signal}`
            : `runner isolation tests failed with exit ${code ?? 1}`,
        ),
      );
    });
  });
}
