// Slash trigger 检测 —— 纯函数,单测覆盖。
//
// 触发条件:输入"/"且其左侧紧邻字符是行首或空白。`foo/bar` 这种词内 / 不触发。
// query 是 / 与光标之间的文本;若中间出现任何空白则视为已结束(用户已经在打
// 命令之后的参数),返回 null 让 popover 关闭。
//
// 用法:从 TipTap selection 拿当前段落光标前文本(`$from.parent.textBetween(0,
// $from.parentOffset)`),传给本函数。返回的 startOffset 是 / 在该段落内的偏移,
// 调用方加上段落起始绝对位置即可得 ProseMirror 范围。

export type SlashTriggerHit = {
  // / 字符在 textBeforeCursor 中的偏移(0-based)。
  startOffset: number;
  // / 之后到光标位置的文本,不含 / 本身。
  query: string;
  // 命中的触发字符;Codex skill 为 $,slash command / Claude skill 为 /。
  trigger: "/" | "$";
};

export function detectSlashTrigger(
  textBeforeCursor: string,
  enabledTriggers: readonly ("/" | "$")[] = ["/"],
): SlashTriggerHit | null {
  for (let i = textBeforeCursor.length - 1; i >= 0; i--) {
    const ch = textBeforeCursor[i];
    if ((ch === "/" || ch === "$") && enabledTriggers.includes(ch)) {
      // / 之前必须是行首(i==0) 或空白字符。
      if (i === 0 || isSpace(textBeforeCursor[i - 1])) {
        const query = textBeforeCursor.slice(i + 1);
        // query 包含空白 → 触发已被结束(用户已经在打参数)。
        if (containsSpace(query)) return null;
        return { startOffset: i, query, trigger: ch };
      }
      // / 前不是空白(例如 foo/bar) → 不当触发。
      return null;
    }
    // 还没找到 / 就先撞上空白 → 没有触发。
    if (isSpace(ch)) return null;
  }
  return null;
}

function isSpace(ch: string | undefined): boolean {
  if (!ch) return false;
  return /\s/.test(ch);
}

function containsSpace(s: string): boolean {
  return /\s/.test(s);
}
