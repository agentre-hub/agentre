// shell-command.ts —— 剥掉 codex 等后端给命令套的 `zsh -lc '…'` 外壳，只剩真正执行的命令。
//
// 摘要行（raw/summary.ts）与记忆写入判据（memory-write/parse.ts）都要看「里面那条命令」，
// 外壳的识别只留这一份，免得两处各抄一份正则后漂移。

const SHELL_WRAPPER =
  /^(?:\/[^\s]+\/)?(?:zsh|bash|sh)\s+-lc\s+(['"])([\s\S]*)\1$/;

/** 去掉 `(zsh|bash|sh) -lc '…'|"…"` 外壳（含绝对路径的 shell），并还原内层转义的引号；非外壳命令仅 trim。 */
export function unwrapShellCommand(command: string): string {
  const trimmed = command.trim();
  const wrapped = SHELL_WRAPPER.exec(trimmed);
  if (!wrapped) return trimmed;
  const [, quote, inner] = wrapped;
  return inner.replaceAll(`\\${quote}`, quote).trim();
}
