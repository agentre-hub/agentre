import { execFileSync } from "node:child_process";
import { basename, isAbsolute, join, resolve, sep } from "node:path";

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

function insideDir(path, dir) {
  if (!path || !dir) return false;
  return path === dir || path.startsWith(dir.endsWith(sep) ? dir : dir + sep);
}

function signalPid(pid, signal = "SIGTERM") {
  try {
    process.kill(pid, signal);
    return true;
  } catch {
    // 探到它和发信号之间它自己走了。
    return false;
  }
}

// 只有这几个名字是 vite 这个可执行文件本身。`vitest` / `vitest.mjs` / `vite-node.mjs`
// 都不在里面 —— 它们是**别的**程序,名字里恰好含着 `vite` 而已。
const VITE_BIN_NAMES = new Set(["vite", "vite.js", "vite.mjs", "vite.cjs"]);

/**
 * 一个进程是不是**本 checkout 的** vite dev server。
 *
 * 判据是「命令行里有一个 token,解析之后是落在 `<repoRoot>/frontend/` 里的 vite 可执行
 * 文件」,而不是「命令行里出现了 vite 这三个字母」:后者(旧判据
 * `pkill -f "<repoRoot>/frontend.*vite"`)必然命中同一个 checkout 里正在跑的 `vitest`,
 * 因为 vitest 的命令行长成
 * `node <repoRoot>/frontend/node_modules/.bin/../vitest/vitest.mjs`,于是每一次
 * `verify-down` 都会顺手打死别人的 `pnpm test`。
 *
 * 相对路径(`node ./node_modules/.bin/../vite/bin/vite.js`,pnpm 常见形状)要靠进程的
 * 工作目录才解析得出归属;工作目录问不出来时宁可放过,也不猜。
 */
export function runsCheckoutVite({ command = "", cwd = "" } = {}, repoRoot) {
  const frontend = join(repoRoot, "frontend");
  for (const token of String(command).split(/\s+/)) {
    if (!VITE_BIN_NAMES.has(basename(token))) continue;
    const path = isAbsolute(token) ? resolve(token) : cwd ? resolve(cwd, token) : "";
    if (insideDir(path, frontend)) return true;
  }
  return false;
}

/** 本机所有进程的 pid + 完整命令行,尽力而为;读不出来就当机器上什么都没有。 */
export function listProcesses(exec = execFileSync) {
  try {
    if (process.platform === "win32") {
      // `-ne $PID` 排掉这条 PowerShell 自己,免得把执行枚举的进程也算成候选。
      const script =
        "Get-CimInstance Win32_Process | Where-Object { $_.ProcessId -ne $PID } | " +
        'ForEach-Object { "$($_.ProcessId)`t$($_.CommandLine)" }';
      const out = exec("powershell", ["-NoProfile", "-NonInteractive", "-Command", script], {
        encoding: "utf8",
        maxBuffer: 16 * 1024 * 1024,
      });
      return out
        .split(/\r?\n/)
        .map((line) => {
          const tab = line.indexOf("\t");
          if (tab < 0) return null;
          const pid = Number(line.slice(0, tab).trim());
          return Number.isInteger(pid) && pid > 0 ? { pid, command: line.slice(tab + 1).trim() } : null;
        })
        .filter(Boolean);
    }
    const out = exec("ps", ["-A", "-o", "pid=,command="], {
      encoding: "utf8",
      maxBuffer: 16 * 1024 * 1024,
    });
    return out
      .split("\n")
      .map((line) => /^\s*(\d+)\s+(.*)$/.exec(line))
      .filter(Boolean)
      .map((match) => ({ pid: Number(match[1]), command: match[2].trim() }));
  } catch {
    return [];
  }
}

/**
 * `wails dev` 关停时会把它的 vite 子进程留下(Unix 上还在另一个进程组里,按组杀漏得掉)。
 * 这里逐个枚举、逐个判定归属、逐个发信号 —— 不用 `pkill -f`:那是把模式交给别人去杀一片,
 * 谁被杀了都不知道,而模式一宽就会误伤同一个 checkout 里的 `vitest`。
 *
 * 返回真正被发了信号的 pid;尽力而为,判不准的一律放过。
 */
export function reapOrphanVite(
  repoRoot,
  { list = listProcesses, describe = describePid, kill = signalPid } = {},
) {
  const reaped = [];
  for (const entry of list()) {
    if (entry.pid === process.pid) continue;
    if (!String(entry.command).split(/\s+/).some((token) => VITE_BIN_NAMES.has(basename(token)))) {
      continue;
    }
    // 命令行里是绝对路径就够判了;相对路径才值得为它多问一次工作目录。
    const candidate = runsCheckoutVite(entry, repoRoot)
      ? entry
      : { ...entry, cwd: describe(entry.pid)?.cwd ?? "" };
    if (!runsCheckoutVite(candidate, repoRoot)) continue;
    kill(entry.pid);
    reaped.push(entry.pid);
  }
  return reaped;
}

/**
 * 正在监听 `port` 的 pid,尽力而为。Windows 上没有 lsof,而调用方把「问不出来」与
 * 「不是我们的」同等对待,所以那里维持拒绝。
 */
export function listeningPids(port, exec = execFileSync) {
  if (process.platform === "win32") return [];
  try {
    const out = exec("lsof", ["-nP", `-iTCP:${port}`, "-sTCP:LISTEN", "-t"], { encoding: "utf8" });
    const pids = out
      .split("\n")
      .map((line) => Number(line.trim()))
      .filter((pid) => Number.isInteger(pid) && pid > 0);
    return [...new Set(pids)];
  } catch {
    // 没人在监听,或者这台机器压根没有 lsof。
    return [];
  }
}

/** 一个 pid 的完整命令行与工作目录;操作系统不肯说就返回 null。 */
export function describePid(pid, exec = execFileSync) {
  if (process.platform === "win32") return null;
  try {
    const command = exec("ps", ["-o", "command=", "-p", String(pid)], { encoding: "utf8" }).trim();
    const cwdLine = exec("lsof", ["-a", "-d", "cwd", "-p", String(pid), "-Fn"], { encoding: "utf8" })
      .split("\n")
      .find((line) => line.startsWith("n"));
    const cwd = cwdLine ? cwdLine.slice(1).trim() : "";
    if (!command || !cwd) return null;
    return { pid, command, cwd };
  } catch {
    return null;
  }
}

function ownedByCheckout({ command, cwd }, repoRoot) {
  return Boolean(command) && command.includes(repoRoot + sep) && insideDir(cwd, repoRoot);
}

/**
 * 占着 `port` 的所有进程 —— 但仅当**每一个**都能被证明属于本 checkout:命令行里带着本
 * checkout 的绝对路径,且工作目录也在本 checkout 里面。
 *
 * `wails dev` 崩溃后留下的正是这种孤儿:被 `kill -9` 的只是 `wails dev` 自己,它编出来
 * 的 `build/bin/…/Agentre` 二进制还在 LISTEN。没有这道判定,启动器只能拒绝,逼人先插一条
 * `verify-down`,而那恰好毁掉「崩溃后重启」这类正要验的东西。
 *
 * 只要有一处证不出来(包括「操作系统不肯说」),就返回 null —— 另一种选择是对陌生进程
 * 发信号。
 */
export function orphanProcessesOnPort(
  port,
  repoRoot,
  { listeners = listeningPids, describe = describePid } = {},
) {
  const pids = listeners(port);
  if (pids.length === 0) return null;
  const holders = [];
  for (const pid of pids) {
    const holder = describe(pid);
    if (!holder || !ownedByCheckout(holder, repoRoot)) return null;
    holders.push(holder);
  }
  return holders;
}

/**
 * 端口的持有者全都是本 checkout 的孤儿时,把它们停掉;返回被收走的持有者,否则返回 null
 * 并且**一个信号都不发**。只对逐个核实过的 pid 发信号,不按进程组 —— 组里可能有没核实过的。
 */
export async function reapOwnPortHolders(port, repoRoot, { kill = signalPid, ...probes } = {}) {
  const holders = orphanProcessesOnPort(port, repoRoot, probes);
  if (!holders) return null;
  for (const holder of holders) kill(holder.pid);
  for (const holder of holders) await waitForExit(holder.pid);
  return holders;
}
