// 三档，k 与 M 同构（商小于阈值保 1 位小数、否则取整）：
//   - < 1000        → 原样
//   - [1e3, 1e6)    → k：商 >= 100 取整，否则 1 位小数
//   - >= 1e6        → M：商 >= 10 取整，否则 1 位小数；整数时省掉 ".0"（1e6 → "1M"）
// 额外一条：k 档取整后要是凑够 1000（999_999 → "1000k"），改按 M 档渲染 ——「1000k」
// 这个字符串在任何输入下都不该出现。
export function formatTokens(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) {
    const v = n / 1000;
    if (v < 100) return `${v.toFixed(1)}k`;
    const rounded = Math.round(v);
    if (rounded < 1000) return `${rounded}k`;
    // 落到这里说明四舍五入把它顶进了 M 档，交给下面统一渲染。
  }
  const v = n / 1_000_000;
  if (v >= 10) return `${Math.round(v)}M`;
  const s = v.toFixed(1);
  return `${s.endsWith(".0") ? s.slice(0, -2) : s}M`;
}
