import { pinyin } from "pinyin-pro";

export type SuggestionScoreInput = {
  query: string;
  title: string;
  subtitle?: string;
  // Only expands the in-order tier; false keeps Command Palette and directories unchanged.
  allowWhitespaceInOrder?: boolean;
};

// 评分阶梯（先命中先得，不叠加）：
//   100  query 完全等于 title
//    80  title 以 query 开头
//    60  title substring 包含 query
//    50  query 全 ASCII + 是 title 拼音全拼连写的 substring（年度报告 → niandubaogao）
//    40  query 全 ASCII + 是 title 拼音首字母连写的 substring（年度报告 → ndbg）
//    30  query 全 ASCII + 字符按序出现在 title 拼音全拼里（nbg → niandubaogao）
//    20  subtitle substring 包含 query
//     0  不命中
// query 为空 → 1（保持原序，全部命中）
//
// 注：pinyin-pro 的 match() 在当前版本对中文返回 null 不可用，
// 改用 pinyin() 原语自行拼接 full / initials 字符串，再做 includes / fuzzy。
// 这样依赖面更窄，bundle 与运行时也更稳定。
const ASCII_ONLY_RE = /^[A-Za-z0-9]+$/;
const ASCII_WITH_WHITESPACE_RE = /^[A-Za-z0-9\s]+$/;

function fullPinyin(title: string): string {
  return pinyin(title, { toneType: "none", type: "array" })
    .join("")
    .toLowerCase();
}

function initialsPinyin(title: string): string {
  return pinyin(title, {
    pattern: "first",
    toneType: "none",
    type: "array",
  })
    .join("")
    .toLowerCase();
}

function fuzzyInOrder(query: string, hay: string): boolean {
  let i = 0;
  for (const c of hay) {
    if (c === query[i]) {
      i += 1;
      if (i === query.length) return true;
    }
  }
  return false;
}

export function normalizeSuggestionQuery(query: string): string {
  return query.trim().toLowerCase();
}

export function scoreSuggestion({
  query,
  title,
  subtitle,
  allowWhitespaceInOrder = false,
}: SuggestionScoreInput): number {
  const q = query.trim();
  if (!q) return 1;

  const ql = normalizeSuggestionQuery(query);
  const tl = title.toLowerCase();

  if (ql === tl) return 100;
  if (tl.startsWith(ql)) return 80;
  if (tl.includes(ql)) return 60;

  const asciiOnly = ASCII_ONLY_RE.test(q);
  const whitespaceFuzzy =
    allowWhitespaceInOrder && ASCII_WITH_WHITESPACE_RE.test(q);
  if (asciiOnly || whitespaceFuzzy) {
    const full = fullPinyin(title);
    if (asciiOnly && full.includes(ql)) return 50;
    const inits = initialsPinyin(title);
    if (asciiOnly && inits.includes(ql)) return 40;
    if (fuzzyInOrder(ql, full)) return 30;
  }

  if (subtitle && subtitle.toLowerCase().includes(ql)) return 20;

  return 0;
}
