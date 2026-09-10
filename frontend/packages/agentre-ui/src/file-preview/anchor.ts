/**
 * 预览面板的「定位目标」：转录里点一条带行号的链接时，编辑器要滚到哪一段。
 *
 * 两个形状是刻意分开的：`PreviewAnchor` 是**链接里读出来的事实**（起始行 +
 * 可选终止行，1-based，与链接文本一致），由共享包解析、交给宿主；
 * `PreviewRevealTarget` 多一个 `nonce`，是**宿主铸的**——同一条链接被重复点击
 * 时 path 与行号都不变，没有 nonce 就没有任何东西能让编辑器再滚一次，表现是
 * 「点了没反应」。宿主每记一次定位目标就自增一次。
 */
export type PreviewAnchor = {
  /** 起始行，1-based。 */
  line: number;
  /** 终止行，1-based；缺席表示只定位到起始行这一行。 */
  endLine?: number;
};

export type PreviewRevealTarget = PreviewAnchor & {
  /** 每记一次定位目标自增一次；变了就重新定位一次。 */
  nonce: number;
};

/**
 * 把一条定位目标钳进 `[1, lineCount]`。
 *
 * 行号来自 AI 写出来的引用，文件在那之后可能已经短了（甚至空了），越界是常态而
 * 不是异常：一律钳到能落脚的位置，不报错、不产生失败态（规格「owned failure
 * paths」）。终止行早于起始行的畸形链接同样不当错误处理，收敛成起始行一行。
 */
export function clampAnchor(
  target: PreviewAnchor,
  lineCount: number,
): { startLine: number; endLine: number } {
  const last = Math.max(1, lineCount);
  const startLine = Math.min(Math.max(1, target.line), last);
  const endLine =
    target.endLine === undefined
      ? startLine
      : Math.min(Math.max(startLine, target.endLine), last);
  return { startLine, endLine };
}
