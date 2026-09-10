import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  ORG_INDEX_VIEW_TEST_KEYS,
  useOrgIndexView,
} from "../use-org-index-view";

const LEGACY_VIEW_MODE_KEY = "agentre.orgView.mode";

describe("useOrgIndexView", () => {
  it("恢复上次选中的行，并把选中态写回 not-chattable 约定的那个键", () => {
    localStorage.setItem(
      ORG_INDEX_VIEW_TEST_KEYS.selected,
      JSON.stringify({ kind: "agent", id: 42 }),
    );

    const { result } = renderHook(() => useOrgIndexView());
    expect(result.current.selected).toEqual({ kind: "agent", id: 42 });

    act(() => result.current.setSelected({ kind: "department", id: 7 }));

    expect(result.current.selected).toEqual({ kind: "department", id: 7 });
    expect(
      JSON.parse(localStorage.getItem(ORG_INDEX_VIEW_TEST_KEYS.selected) ?? ""),
    ).toEqual({ kind: "department", id: 7 });
  });

  it("遗留的 agentre.orgView.mode 值被忽略：不报错、不清空其他状态", () => {
    localStorage.setItem(LEGACY_VIEW_MODE_KEY, "list");
    localStorage.setItem(
      ORG_INDEX_VIEW_TEST_KEYS.selected,
      JSON.stringify({ kind: "agent", id: 42 }),
    );

    const { result } = renderHook(() => useOrgIndexView());

    expect(result.current.selected).toEqual({ kind: "agent", id: 42 });
    // 既不读它，也不顺手把它抹掉 —— 忽略就是忽略
    expect(localStorage.getItem(LEGACY_VIEW_MODE_KEY)).toBe("list");
    expect("viewMode" in result.current).toBe(false);
  });

  it("选中态存的是坏 JSON 时退回未选中，而不是抛出来", () => {
    localStorage.setItem(ORG_INDEX_VIEW_TEST_KEYS.selected, "{not json");

    const { result } = renderHook(() => useOrgIndexView());

    expect(result.current.selected).toBeNull();
  });
});
