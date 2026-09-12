import * as React from "react";

import type { OrgSelection } from "./types";

// 索引的视图状态只剩一件：选中了哪一行。缩放 / 平移 / 折叠随画布一起没了，
// `agentre.orgView.mode`（tree|list）随两个视图一并作废 —— 这里**不读它**，
// 存量的任何旧值因此都只是 localStorage 里一条无人问津的记录，既不会报错，
// 也不会连累其他键（不主动删除：忽略就是忽略）。
//
// 选中态的键沿用 `agentre.orgTree.selected`：not-chattable 对话框会先写这个键再跳到
// /org（见 ../not-chattable/mapping.ts 的 ORG_SELECTED_STORAGE_KEY），改名等于把那条
// 跳转打断。
const KEY_SELECTED = "agentre.orgTree.selected";

function safeParse<T>(raw: string | null, fallback: T): T {
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

export function useOrgIndexView() {
  const [selected, setSelectedRaw] = React.useState<OrgSelection>(() =>
    safeParse<OrgSelection>(localStorage.getItem(KEY_SELECTED), null),
  );

  React.useEffect(() => {
    localStorage.setItem(KEY_SELECTED, JSON.stringify(selected));
  }, [selected]);

  const setSelected = React.useCallback(
    (sel: OrgSelection) => setSelectedRaw(sel),
    [],
  );

  return { selected, setSelected };
}

export const ORG_INDEX_VIEW_TEST_KEYS = {
  selected: KEY_SELECTED,
};
