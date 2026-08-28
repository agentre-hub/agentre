import {
  ANY_TIME,
  DEFAULT_DONE_RETENTION,
  type BoardQuery,
  type DoneRetention,
  type ProjectScope,
  type TimeRange,
} from "./query-types";

/** 六个条件的名字。计数与 chip 都以它为准。 */
export type ConditionKey =
  | "keyword"
  | "scope"
  | "labels"
  | "updated"
  | "created"
  | "doneRetention";

/** 此刻真正在收窄看板的那几条。 */
export function activeConditions(query: BoardQuery): ConditionKey[] {
  const keys: ConditionKey[] = [];
  if (query.keyword.trim()) keys.push("keyword");
  if (query.scope.kind !== "all") keys.push("scope");
  if (query.labelIds.length > 0 || query.noLabelOnly) keys.push("labels");
  if (query.updated.preset !== "any") keys.push("updated");
  if (query.created.preset !== "any") keys.push("created");
  if (query.doneRetention !== DEFAULT_DONE_RETENTION)
    keys.push("doneRetention");
  return keys;
}

/**
 * 「筛选」按钮上那个数字：**生效的条件个数**，不是标签个数。选三枚标签是**一条**
 * 条件；数成 3 会让人以为看板被收窄了三次。
 */
export function activeConditionCount(query: BoardQuery): number {
  return activeConditions(query).length;
}

/**
 * chip 行里的一枚。标签那一条条件会摊成**每枚标签一枚 chip** —— 计数按条件、摘除
 * 按标签，这正是「数字不是标签个数」那句话成立的前提：chip 可以比条件多。
 */
export type FilterChip =
  | { kind: "keyword"; key: "keyword"; keyword: string }
  | { kind: "scope"; key: "scope"; scope: ProjectScope }
  | { kind: "label"; key: string; labelId: number }
  | { kind: "noLabel"; key: "noLabel" }
  | {
      kind: "time";
      key: "updated" | "created";
      field: "updated" | "created";
      range: TimeRange;
    }
  | {
      kind: "doneRetention";
      key: "doneRetention";
      retention: DoneRetention;
    };

export function buildFilterChips(query: BoardQuery): FilterChip[] {
  const chips: FilterChip[] = [];

  if (query.keyword.trim()) {
    chips.push({ kind: "keyword", key: "keyword", keyword: query.keyword });
  }
  if (query.scope.kind !== "all") {
    chips.push({ kind: "scope", key: "scope", scope: query.scope });
  }
  for (const labelId of query.labelIds) {
    chips.push({ kind: "label", key: `label:${labelId}`, labelId });
  }
  if (query.noLabelOnly) chips.push({ kind: "noLabel", key: "noLabel" });
  if (query.updated.preset !== "any") {
    chips.push({
      kind: "time",
      key: "updated",
      field: "updated",
      range: query.updated,
    });
  }
  if (query.created.preset !== "any") {
    chips.push({
      kind: "time",
      key: "created",
      field: "created",
      range: query.created,
    });
  }
  if (query.doneRetention !== DEFAULT_DONE_RETENTION) {
    chips.push({
      kind: "doneRetention",
      key: "doneRetention",
      retention: query.doneRetention,
    });
  }

  return chips;
}

/** 摘掉一枚 chip：那一条（或那一枚标签）回到默认，其余原样不动。 */
export function dropChip(query: BoardQuery, chip: FilterChip): BoardQuery {
  switch (chip.kind) {
    case "keyword":
      return { ...query, keyword: "" };
    case "scope":
      return { ...query, scope: { kind: "all" } };
    case "label":
      return {
        ...query,
        labelIds: query.labelIds.filter((id) => id !== chip.labelId),
      };
    case "noLabel":
      return { ...query, noLabelOnly: false };
    case "time":
      return { ...query, [chip.field]: ANY_TIME };
    case "doneRetention":
      return { ...query, doneRetention: DEFAULT_DONE_RETENTION };
  }
}
