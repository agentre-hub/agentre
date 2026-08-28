import type {
  OrgDragSubject,
  OrgDropTarget,
  OrgIndexGroup,
  OrgIndexRow,
} from "@agentre-hub/agentre-ui";

export type DragState = {
  subject: OrgDragSubject;
  mode: "pointer" | "keyboard";
  /** 键盘当前停在第几个候选落点；-1 = 刚拾起，还没选。 */
  index: number;
};

/** 渲染单元与键盘候选落点同源：两者一旦各走一套，方向键就会指到画面之外。 */
export type IndexUnit =
  | { key: string; kind: "insert"; target: OrgDropTarget }
  | {
      key: string;
      kind: "header";
      group: OrgIndexGroup;
      target?: OrgDropTarget;
    }
  | {
      key: string;
      kind: "row";
      row: OrgIndexRow;
      /**
       * 缩进只由所在部门决定 —— 下属与主管同级，见 org-index-model.ts。
       * 部门里的行是 `group.depth + 1`；顶部那批不属于任何部门的行是 0。
       */
      indent: number;
      target?: OrgDropTarget;
    };

export function buildUnits(
  topRows: OrgIndexRow[],
  groups: OrgIndexGroup[],
  drag: DragState | null,
): IndexUnit[] {
  const subject = drag?.subject ?? null;
  const draggingAgent = subject?.kind === "agent" ? subject.id : 0;
  const draggingDepartment = subject?.kind === "department" ? subject.id : 0;

  const allRows = [...topRows, ...groups.flatMap((g) => g.rows)];
  const subjectRow = draggingAgent
    ? allRows.find((r) => r.agent.id === draggingAgent)
    : undefined;
  // 排序细线只在被拖那一行自己的桶里出现（同部门、同上级）。
  const bucketRows = subjectRow
    ? allRows.filter(
        (r) =>
          r.departmentId === subjectRow.departmentId &&
          r.parentAgentId === subjectRow.parentAgentId,
      )
    : [];
  const lastBucketRowId = bucketRows.at(-1)?.agent.id ?? 0;

  const insertUnit = (row: OrgIndexRow, index: number): IndexUnit => ({
    key: `insert-${row.departmentId}-${row.parentAgentId}-${index}`,
    kind: "insert",
    target: {
      kind: "reorder",
      departmentId: row.departmentId,
      parentAgentId: row.parentAgentId,
      index,
      orderedIds: row.bucketOrderedIds,
    },
  });

  const rowUnits = (rows: OrgIndexRow[], indent: number): IndexUnit[] =>
    rows.flatMap((row) => {
      const inBucket =
        subjectRow !== undefined &&
        row.departmentId === subjectRow.departmentId &&
        row.parentAgentId === subjectRow.parentAgentId;
      const units: IndexUnit[] = [];
      if (inBucket) units.push(insertUnit(row, row.bucketIndex));
      units.push({
        key: `row-${row.agent.id}`,
        kind: "row",
        row,
        indent,
        // 拖部门时行不是落点；拖 Agent 时自己那一行也不是。
        target:
          draggingAgent && row.agent.id !== draggingAgent
            ? { kind: "agent", agentId: row.agent.id }
            : undefined,
      });
      if (inBucket && row.agent.id === lastBucketRowId) {
        units.push(insertUnit(row, row.bucketOrderedIds.length));
      }
      return units;
    });

  return [
    ...rowUnits(topRows, 0),
    ...groups.flatMap((group) => [
      {
        key: `header-${group.department.id}`,
        kind: "header" as const,
        group,
        target:
          subject && group.department.id !== draggingDepartment
            ? ({
                kind: "department",
                departmentId: group.department.id,
              } as OrgDropTarget)
            : undefined,
      },
      // 行缩在自己的部门头下一级：层级只剩「部门套部门」这一种，而部门里的行
      // 得看得出是这个部门的（与组头同级的话，两者会挤在同一条左缘）。
      ...rowUnits(group.rows, group.depth + 1),
    ]),
  ];
}
