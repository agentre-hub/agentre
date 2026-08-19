/**
 * 把 `draggedId` 拖到 `insertIndex` 之后的新次序。`orderedIds` 是该桶的**完整现序**。
 *
 * `insertIndex` 是整组上的一个槽位（0..n，"放在现在第 i 个之前"，i === n 即"放到
 * 最后"）—— 索引里的静态细线发的就是它，因为细线并不知道正在拖的是谁。被拖的那
 * 一项摘出来之后，它右边的槽位都会左移一格；落在拾起点两侧任一槽位都是空操作。
 */
export function computeOrgReorder(
  orderedIds: number[],
  draggedId: number,
  insertIndex: number,
): number[] {
  const from = orderedIds.indexOf(draggedId);
  if (from < 0) return orderedIds.slice();
  const without = orderedIds.filter((id) => id !== draggedId);
  const adjusted = insertIndex > from ? insertIndex - 1 : insertIndex;
  const clamped = Math.max(0, Math.min(adjusted, without.length));
  without.splice(clamped, 0, draggedId);
  return without;
}
