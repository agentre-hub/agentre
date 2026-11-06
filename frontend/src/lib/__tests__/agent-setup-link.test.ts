import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  ORG_SELECTED_STORAGE_KEY,
  openAgentGaps,
  openAgentSetup,
  readOrgLinkState,
} from "../agent-setup-link";

describe("agent-setup-link", () => {
  beforeEach(() => localStorage.clear());

  it("openAgentSetup 选中那个 Agent：先写组织页记住的选中键，再带 orgSelection 去 /org", () => {
    const navigate = vi.fn();

    openAgentSetup(navigate, 42);

    expect(localStorage.getItem(ORG_SELECTED_STORAGE_KEY)).toBe(
      JSON.stringify({ kind: "agent", id: 42 }),
    );
    expect(navigate).toHaveBeenCalledWith("/org", {
      state: { orgSelection: { kind: "agent", id: 42 } },
    });
    expect(readOrgLinkState(navigate.mock.calls[0][1].state)).toEqual({
      selection: { kind: "agent", id: 42 },
      needsSetup: false,
    });
  });

  it("openAgentGaps 只有一个缺口时直达那个 Agent", () => {
    const navigate = vi.fn();

    openAgentGaps(navigate, [7]);

    expect(navigate).toHaveBeenCalledTimes(1);
    expect(readOrgLinkState(navigate.mock.calls[0][1].state)).toEqual({
      selection: { kind: "agent", id: 7 },
      needsSetup: false,
    });
  });

  it("openAgentGaps 多个缺口时进组织架构并预选「待配置」，不替用户挑一个", () => {
    const navigate = vi.fn();

    openAgentGaps(navigate, [7, 9]);

    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate.mock.calls[0][0]).toBe("/org");
    expect(readOrgLinkState(navigate.mock.calls[0][1].state)).toEqual({
      selection: null,
      needsSetup: true,
    });
    expect(localStorage.getItem(ORG_SELECTED_STORAGE_KEY)).toBeNull();
  });

  it("openAgentGaps 没有缺口时什么也不做", () => {
    const navigate = vi.fn();

    openAgentGaps(navigate, []);

    expect(navigate).not.toHaveBeenCalled();
  });

  it("readOrgLinkState 对缺省 / 残缺的路由 state 一律视为没有意图", () => {
    const none = { selection: null, needsSetup: false };
    expect(readOrgLinkState(null)).toEqual(none);
    expect(readOrgLinkState(undefined)).toEqual(none);
    expect(readOrgLinkState("x")).toEqual(none);
    expect(
      readOrgLinkState({ orgSelection: { kind: "agent", id: 0 } }),
    ).toEqual(none);
    expect(readOrgLinkState({ orgFilter: "something-else" })).toEqual(none);
  });

  it("readOrgLinkState 认得部门选中（与组织页既有的 orgSelection 契约一致）", () => {
    expect(
      readOrgLinkState({ orgSelection: { kind: "department", id: 3 } }),
    ).toEqual({ selection: { kind: "department", id: 3 }, needsSetup: false });
  });
});
