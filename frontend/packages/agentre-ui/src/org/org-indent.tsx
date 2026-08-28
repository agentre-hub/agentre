/**
 * 索引的缩进度量与缩进导引线 —— 组头与行的**唯一**来源。
 *
 * 索引里只有一种层级：部门套部门。组头按部门链缩进，行缩在自己的部门头下一级
 * （`indent = group.depth + 1`）；下属 Agent 与主管仍然一律同级，从属只写在行内的
 * `↳ 主管`（规格 2026-08-18 决策 3 不变）。
 *
 * 两个基准值差 2px 是刻意的：组头左边是收放三角、行左边是头像，把两者的**图形**
 * 左缘对齐，而不是把盒子左缘对齐。之所以收在这一个文件里，是因为竖线的位置必须和
 * 内容用同一个步长算 —— 一旦分成两份同名常量，改了步长就会看到线和内容错开。
 */
export const ORG_INDENT_STEP = 15;
export const ORG_INDENT_BASE_HEADER = 6;
export const ORG_INDENT_BASE_ROW = 8;
/** 竖线摆在该层级左缘往里 5px 的空槽里 —— 比同层内容的左缘还左，不会压到字。 */
export const ORG_RAIL_OFFSET = ORG_INDENT_BASE_HEADER + 5;

export function orgHeaderPaddingLeft(depth: number): number {
  return ORG_INDENT_BASE_HEADER + depth * ORG_INDENT_STEP;
}

export function orgRowPaddingLeft(indent: number): number {
  return ORG_INDENT_BASE_ROW + indent * ORG_INDENT_STEP;
}

/**
 * 祖先层级的缩进导引线：深度 n 的组头 / 行画 n 条，第 i 条对应第 i 层祖先。
 *
 * 相邻行画在同一个 x 上的线自然接成一条竖线，中断只发生在「没有那一层祖先」的地方
 * —— 也就是下一个顶层部门头那里，这正是我们要的分段。线是纯装饰，`aria-hidden`：
 * 层级对读屏是 `aria-level` / 组头文本的事，不是一根 1px 的 div。
 */
export function OrgIndentRails({ depth }: { depth: number }) {
  if (depth <= 0) return null;
  return (
    <>
      {Array.from({ length: depth }, (_, i) => (
        <span
          key={i}
          data-slot="org-indent-rail"
          aria-hidden="true"
          className="pointer-events-none absolute inset-y-0 w-px bg-border-strong"
          style={{ left: ORG_RAIL_OFFSET + i * ORG_INDENT_STEP }}
        />
      ))}
    </>
  );
}
