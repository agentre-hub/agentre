import { execFileSync } from "node:child_process";
import { join } from "node:path";

/** pid(或它所在的进程组)是否还在。verify.mjs 与 target.mjs 的存活判定都用这一份。 */
export function alive(pid) {
  for (const target of [-pid, pid]) {
    try {
      process.kill(target, 0);
      return true;
    } catch {
      // 换个目标再试(不是进程组首领时 -pid 会失败),或者它已经不在了。
    }
  }
  return false;
}

/**
 * 等 pid(连同它所在的进程组)真正退出。返回 false 表示在预算内没等到 —— 调用方仍然
 * 继续收尾,不挂在一个赖着不走的进程上。
 *
 * `verify-down --wipe` 要在删掉 browserDir / dataDir 之前用它:SIGTERM 交出去到进程
 * 最后写完用户数据目录之间还有一拍,删得太早会偶发 ENOTEMPTY(浏览器目录正在被收尾的
 * Chromium 写)。stopPid 杀的是整个进程组,所以这里也等整个组。
 */
export async function waitForExit(pid, { timeoutMs = 10_000, everyMs = 50 } = {}) {
  if (!pid) return true;
  const deadline = Date.now() + timeoutMs;
  while (alive(pid)) {
    if (Date.now() >= deadline) return false;
    await new Promise((done) => setTimeout(done, everyMs));
  }
  return true;
}

/**
 * `wails dev` orphans its vite child on shutdown (a separate process group on Unix), which a
 * group-kill misses. Reap by command line, scoped to THIS repo's frontend so a sibling
 * checkout's vite (e.g. agentre-server) is never touched. Best-effort.
 */
export function reapOrphanVite(repoRoot) {
  const frontend = join(repoRoot, "frontend");
  try {
    if (process.platform === "win32") {
      // No pkill on Windows; match via CIM and force-kill. `-ne $PID` excludes THIS PowerShell
      // (its own command line contains the pattern), or we'd recreate the self-kill we avoid.
      const ps =
        "Get-CimInstance Win32_Process | Where-Object { " +
        `$_.ProcessId -ne $PID -and $_.CommandLine -like '*${frontend}*vite*' } | ` +
        "ForEach-Object { Stop-Process -Id $_.ProcessId -Force }";
      execFileSync("powershell", ["-NoProfile", "-NonInteractive", "-Command", ps], {
        stdio: "ignore",
      });
    } else {
      execFileSync("pkill", ["-f", `${frontend}.*vite`], { stdio: "ignore" });
    }
  } catch {
    // best-effort hygiene; nothing to reap.
  }
}
