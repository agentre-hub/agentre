import { describe, expect, it } from "vitest";

import type { ChatTab, TabKind } from "@/stores/chat-tabs-store";
import type { app } from "../../../wailsjs/go/models";

import {
  newTabInsertIndex,
  projectOfTab,
  rootProjectResolver,
} from "../new-tab-placement";

// 虚构项目树：1 agentre ─┬─ 11 ios ── 111 widgets
//                       └─ 12 web
//              2 katch
function node(
  id: number,
  children: app.ProjectTreeNode[] = [],
): app.ProjectTreeNode {
  return {
    project: { id } as app.ProjectItem,
    children,
  } as app.ProjectTreeNode;
}
const TREE = [node(1, [node(11, [node(111)]), node(12)]), node(2)];

function tab(id: string, meta: TabKind, isPinned = false): ChatTab {
  return { id, meta, isPreview: false, isPinned, pinAt: 0, openedAt: 0 };
}
const newTab = (id: string, projectId: number, pinned = false) =>
  tab(id, { kind: "new", projectId, agentId: 1, workMode: "" }, pinned);
const sessionTab = (id: string, sessionId: number, pinned = false) =>
  tab(id, { kind: "session", sessionId }, pinned);

describe("rootProjectResolver", () => {
  it("Given 项目树 When 取各级项目的顶层 Then 都回到顶层项目", () => {
    const rootOf = rootProjectResolver(TREE);
    expect(rootOf(1)).toBe(1);
    expect(rootOf(11)).toBe(1);
    expect(rootOf(111)).toBe(1);
    expect(rootOf(12)).toBe(1);
    expect(rootOf(2)).toBe(2);
  });

  it("Given 树里找不到 / 树为空 When 取顶层 Then 回落为项目自身", () => {
    expect(rootProjectResolver(TREE)(999)).toBe(999);
    expect(rootProjectResolver([])(11)).toBe(11);
  });
});

describe("projectOfTab", () => {
  const metas = new Map<number, { projectId?: number }>([
    [100, { projectId: 11 }],
    [101, {}],
  ]);
  const of = (t: ChatTab) => projectOfTab(t, (sid) => metas.get(sid));

  it("会话标签页取会话所属项目；项目未知时不算", () => {
    expect(of(sessionTab("a", 100))).toBe(11);
    expect(of(sessionTab("b", 101))).toBeNull();
    expect(of(sessionTab("c", 999))).toBeNull();
  });

  it("新对话与终端标签页取自带的项目", () => {
    expect(of(newTab("a", 2))).toBe(2);
    expect(
      of(
        tab("t", {
          kind: "terminal",
          projectId: 12,
          deviceId: "d",
          terminalId: "x",
        }),
      ),
    ).toBe(12);
  });

  it("附着终端（无项目）与远端设备标签页不算", () => {
    expect(
      of(
        tab("t", {
          kind: "terminal",
          projectId: 0,
          deviceId: "",
          terminalId: "x",
          attach: true,
        }),
      ),
    ).toBeNull();
    expect(
      of(
        tab("p", {
          kind: "peer",
          fingerprint: "f",
          conversationId: "c",
          deviceName: "mac",
        }),
      ),
    ).toBeNull();
  });
});

describe("newTabInsertIndex", () => {
  const rootOf = rootProjectResolver(TREE);
  const ctx = {
    projectOfTab: (t: ChatTab) =>
      t.meta.kind === "new" ? t.meta.projectId || null : null,
    rootOf,
  };

  it("Given 同项目标签页分散 When 新建 Then 插到最后一个同项目标签页右边", () => {
    const tabs = [
      newTab("a", 1),
      newTab("b", 2),
      newTab("c", 1),
      newTab("d", 2),
    ];
    expect(newTabInsertIndex(tabs, 1, ctx)).toBe(3);
  });

  it("Given 栏上没有同项目族 When 新建 Then 追加到最右", () => {
    const tabs = [newTab("a", 1), newTab("b", 12)];
    expect(newTabInsertIndex(tabs, 2, ctx)).toBe(2);
  });

  it("Given 项目为空(0) When 新建 Then 追加到最右", () => {
    const tabs = [newTab("a", 1), newTab("b", 2)];
    expect(newTabInsertIndex(tabs, 0, ctx)).toBe(2);
  });

  it("Given 子项目 When 在父项目新建 Then 子项目标签页也算同组", () => {
    const tabs = [
      newTab("a", 1),
      newTab("b", 2),
      newTab("c", 111),
      newTab("d", 2),
    ];
    expect(newTabInsertIndex(tabs, 1, ctx)).toBe(3);
  });

  it("Given 父项目与兄弟子项目 When 在子项目新建 Then 父/兄弟都算同组", () => {
    const tabs = [newTab("a", 12), newTab("b", 2), newTab("c", 2)];
    expect(newTabInsertIndex(tabs, 11, ctx)).toBe(1);
  });

  it("Given 同组只在固定区 When 新建 Then 放到固定区之后的第一个位置", () => {
    const tabs = [
      newTab("a", 1, true),
      newTab("b", 2, true),
      newTab("c", 2),
      newTab("d", 2),
    ];
    expect(newTabInsertIndex(tabs, 1, ctx)).toBe(2);
  });

  // 取消固定是原地不动的（togglePin「unpin 时位置不动」），固定的标签页因此不一定是
  // 前缀：[a, b📌, c📌] 里 b、c 仍是用户看到的固定区，新标签页不能夹进两者之间。
  it("Given 同组成员是前缀之外的一段固定标签页 When 新建 Then 放到这段固定区之后，不夹进固定标签页之间", () => {
    const tabs = [
      newTab("a", 2),
      newTab("b", 1, true),
      newTab("c", 2, true),
      newTab("d", 2),
    ];
    expect(newTabInsertIndex(tabs, 1, ctx)).toBe(3);
  });

  it("Given 同组在固定区与非固定区都有 When 新建 Then 以非固定区里最后一个为准", () => {
    const tabs = [
      newTab("a", 1, true),
      newTab("b", 2),
      newTab("c", 1),
      newTab("d", 2),
    ];
    expect(newTabInsertIndex(tabs, 1, ctx)).toBe(3);
  });

  it("Given 项目树未加载 When 新建 Then 只认项目恰好相同的标签页", () => {
    const exact = { ...ctx, rootOf: rootProjectResolver([]) };
    const tabs = [newTab("a", 1), newTab("b", 11), newTab("c", 2)];
    expect(newTabInsertIndex(tabs, 11, exact)).toBe(2);
    expect(newTabInsertIndex(tabs, 1, exact)).toBe(1);
  });

  it("Given 成员项目未知(null) When 新建 Then 不把它当同组", () => {
    const tabs = [newTab("a", 1), sessionTab("s", 5), newTab("c", 2)];
    expect(newTabInsertIndex(tabs, 1, ctx)).toBe(1);
  });

  it("空标签栏 When 新建 Then 位置 0", () => {
    expect(newTabInsertIndex([], 1, ctx)).toBe(0);
  });
});
