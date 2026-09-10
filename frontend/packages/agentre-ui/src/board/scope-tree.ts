import type { ScopeProjectNode } from "./query-types";

/** 扁平前序列表里的一行，外加从 depth 栈推出来的两件事。 */
export interface ScopeRow {
  node: ScopeProjectNode;
  /** 从根到父的名字；触发器放不下时先掉这一段。 */
  path: string[];
  /** 挂在它下面的项目数（整棵子树，不只直接子级）—— 触发器上那枚 `+N`。 */
  descendantCount: number;
  /** 搜索命中的是它的后代、它自己没命中：留下来让路径看得见，压暗但仍可选。 */
  ancestorOnly: boolean;
}

/**
 * 把 `ProjectFlat` 形状的扁平前序列表补成可渲染的行。
 *
 * 父子关系从 `depth` 栈推：前序 + 深度已经把树完整编码了，再要求宿主传一份
 * parentId 只是让两处各说一遍、且可能互相矛盾。
 */
export function buildScopeRows(nodes: ScopeProjectNode[]): ScopeRow[] {
  const stack: string[] = [];

  const rows = nodes.map((node) => {
    stack.length = Math.min(stack.length, Math.max(node.depth, 0));
    const path = [...stack];
    stack[node.depth] = node.name;
    stack.length = node.depth + 1;

    return { node, path, descendantCount: 0, ancestorOnly: false };
  });

  // 后代 = 紧跟其后、depth 一直大于它的那一段。
  rows.forEach((row, index) => {
    let count = 0;
    for (let i = index + 1; i < rows.length; i += 1) {
      if (rows[i].node.depth <= row.node.depth) break;
      count += 1;
    }
    row.descendantCount = count;
  });

  return rows;
}

/**
 * 按关键词收窄，但**保留命中项的祖先** —— 否则第三层的命中会孤零零地浮在树上，
 * 看不出挂在谁下面。与 `/chat` 项目树现行规则同一条（桌面端
 * `session-index/index-page.tsx` 的 `projectVisible`：自身命中或子树命中即留下）。
 */
export function filterScopeRows(rows: ScopeRow[], needle: string): ScopeRow[] {
  const trimmed = needle.trim().toLowerCase();
  if (!trimmed) return rows;

  const hit = rows.map((row) => row.node.name.toLowerCase().includes(trimmed));

  return rows
    .map((row, index) => {
      const descendantHit = rows
        .slice(index + 1, index + 1 + row.descendantCount)
        .some((_, offset) => hit[index + 1 + offset]);

      return { row, visible: hit[index] || descendantHit, own: hit[index] };
    })
    .filter((entry) => entry.visible)
    .map((entry) => ({ ...entry.row, ancestorOnly: !entry.own }));
}

/** 一段文本被关键词切成的片段；`match` 的那些要高亮。 */
export interface MatchSegment {
  text: string;
  match: boolean;
}

/** 大小写不敏感地把 `text` 按 `needle` 切段；`needle` 为空时整段不高亮。 */
export function splitMatch(text: string, needle: string): MatchSegment[] {
  const trimmed = needle.trim();
  if (!trimmed) return [{ text, match: false }];

  const lower = text.toLowerCase();
  const target = trimmed.toLowerCase();
  const segments: MatchSegment[] = [];
  let cursor = 0;

  for (;;) {
    const at = lower.indexOf(target, cursor);
    if (at < 0) break;
    if (at > cursor)
      segments.push({ text: text.slice(cursor, at), match: false });
    segments.push({ text: text.slice(at, at + target.length), match: true });
    cursor = at + target.length;
  }

  if (cursor < text.length)
    segments.push({ text: text.slice(cursor), match: false });

  return segments;
}
