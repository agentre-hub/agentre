// 本地命令耗时的紧凑格式化:ms / s(一位小数) / m s。秒用 floor 防止进位到 60。
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const m = Math.floor(ms / 60000);
  const s = Math.floor((ms % 60000) / 1000);
  return `${m}m ${s}s`;
}

// formatTurnDuration 是消息脚注的耗时：最小单位秒、最大单位分钟，不用 ms / h。
export function formatTurnDuration(ms: number): string {
  const safe = Number.isFinite(ms) && ms > 0 ? ms : 0;
  if (safe < 60000) return `${(safe / 1000).toFixed(1)}s`;
  const m = Math.floor(safe / 60000);
  const s = Math.floor((safe % 60000) / 1000);
  return `${m}m ${s}s`;
}

// formatCompactTurnDuration 是统计行紧凑档的用时：不带小数、分秒之间不留空格、
// 秒补两位（49s / 1m32s / 12m05s）。不到一分钟四舍五入到整秒，进位到 60 时写成 1m00s；
// 一分钟以上秒数与 formatTurnDuration 一样取整（floor），明细里的数对得上。
export function formatCompactTurnDuration(ms: number): string {
  const safe = Number.isFinite(ms) && ms > 0 ? ms : 0;
  const total =
    safe < 60000 ? Math.round(safe / 1000) : Math.floor(safe / 1000);
  if (total < 60) return `${total}s`;
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}m${String(s).padStart(2, "0")}s`;
}

export function formatTokensPerSec(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  if (n < 10) return n.toFixed(1);
  return String(Math.round(n));
}
