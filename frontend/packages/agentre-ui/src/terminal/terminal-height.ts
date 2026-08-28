// 只读输出终端的内容自适应高度。行数在 [MIN_ROWS, MAX_ROWS] 间夹取后 × 行高 + 内边距;
// 超过 MAX_ROWS 则封顶(视口固定,xterm 自身滚动)。MAX_ROWS×FALLBACK+PADDING ≈ 旧 h-44(176px)。
export const MIN_ROWS = 3;
export const MAX_ROWS = 9;
export const PADDING_PX = 12; // py-1.5 上下各 6px
export const FALLBACK_CELL_PX = 18; // fontSize 13 下的兜底行高(拿不到真实度量时用)

export function computeTerminalHeight(args: {
  contentRows: number;
  cellHeight: number;
  minRows: number;
  maxRows: number;
  paddingPx: number;
}): number {
  const rows = Math.min(Math.max(args.contentRows, args.minRows), args.maxRows);
  return rows * args.cellHeight + args.paddingPx;
}
