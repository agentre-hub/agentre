// @ mention 触发检测 —— 纯函数。触发条件:输入 "@" 且其左侧紧邻字符是行首或
// 空白 (foo@bar 邮箱形式不触发);query 是 @ 与光标之间的文本,含空白视为已结束。

export type AtTriggerHit = { startOffset: number; query: string };

export function detectAtTrigger(textBeforeCursor: string): AtTriggerHit | null {
  for (let i = textBeforeCursor.length - 1; i >= 0; i--) {
    const ch = textBeforeCursor[i];
    if (ch === "@") {
      if (i === 0 || /\s/.test(textBeforeCursor[i - 1] ?? "")) {
        const query = textBeforeCursor.slice(i + 1);
        if (/\s/.test(query)) return null;
        return { startOffset: i, query };
      }
      return null;
    }
    if (/\s/.test(ch)) return null;
  }
  return null;
}
