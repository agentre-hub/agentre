import { splitMatch, type MatchSegment } from "./scope-tree";

/** 摘录窗口：命中片段前后各留这么多字符，够看出它落在哪句话里。 */
const LEAD = 24;
const TRAIL = 72;

/**
 * 关键词命中描述时，从描述里摘**一行**出来。
 *
 * 卡片正文只有标题，描述从不上卡（Problem：卡片被正文撑开就不再是一眼扫得完的
 * 一张卡）。但搜出来的那一条如果命中落在描述里，卡片上就没有一处解释「它为什么
 * 在这儿」——所以只在这一种情况下多出一行，且只有一行。
 *
 * 命中在标题里时返回 `null`：标题自己已经高亮过了，再摘一行是同一件事说两遍。
 */
export function matchExcerpt(
  description: string | undefined,
  keyword: string | undefined,
): MatchSegment[] | null {
  const needle = keyword?.trim().toLowerCase();
  if (!description || !needle) return null;

  const at = description.toLowerCase().indexOf(needle);
  if (at < 0) return null;

  const start = Math.max(0, at - LEAD);
  const end = Math.min(description.length, at + needle.length + TRAIL);
  const window = description.slice(start, end);
  const segments = splitMatch(window, needle);

  if (start > 0) segments.unshift({ text: "…", match: false });
  if (end < description.length) segments.push({ text: "…", match: false });
  return segments;
}
