import { describe, expect, it, beforeEach } from "vitest";
import {
  useChatTabsStore,
  __setNextIdFactoryForTesting,
  __setNowForTesting,
} from "../chat-tabs-store";

beforeEach(() => {
  localStorage.clear();
});

describe("chat-tabs-store · openSession", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
    __setNowForTesting(() => 1000);
  });

  it("空 tab 列表时, openSession 新建一个 preview tab 并激活", () => {
    useChatTabsStore.getState().openSession(42);
    const { tabs, activeTabId } = useChatTabsStore.getState();
    expect(tabs).toEqual([
      {
        id: "t1",
        meta: { kind: "session", sessionId: 42 },
        isPreview: true,
        isPinned: false,
        pinAt: 0,
        openedAt: 1000,
      },
    ]);
    expect(activeTabId).toBe("t1");
  });

  it("session 已经在某 tab 时, 直接激活那个 tab, 不新建", () => {
    useChatTabsStore.getState().openSession(42);
    useChatTabsStore.getState().openSession(43);
    useChatTabsStore.getState().openSession(42);
    const { tabs, activeTabId } = useChatTabsStore.getState();
    // 原 preview 被 43 替换, 然后第三次 openSession(42) 又把它替换回 42
    expect(tabs).toHaveLength(1);
    expect(tabs[0].meta).toEqual({ kind: "session", sessionId: 42 });
    expect(activeTabId).toBe(tabs[0].id);
  });

  it("第二次 openSession 替换 preview tab, 不增加 tab 数", () => {
    useChatTabsStore.getState().openSession(42);
    useChatTabsStore.getState().openSession(43);
    const { tabs } = useChatTabsStore.getState();
    expect(tabs).toHaveLength(1);
    expect(tabs[0].meta).toEqual({ kind: "session", sessionId: 43 });
    expect(tabs[0].isPreview).toBe(true);
  });

  it("当前没有 preview 但已有非预览 tab 时, 新开为 preview", () => {
    useChatTabsStore.getState().openSession(42);
    useChatTabsStore.getState().promoteCurrent();
    useChatTabsStore.getState().openSession(43);
    const { tabs } = useChatTabsStore.getState();
    expect(tabs).toHaveLength(2);
    expect(tabs[0].isPreview).toBe(false);
    expect(tabs[1].isPreview).toBe(true);
    expect(tabs[1].meta).toEqual({ kind: "session", sessionId: 43 });
  });
});

describe("chat-tabs-store · openSessionInNewTab", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
    __setNowForTesting(() => 1000);
  });

  it("强制新建非预览 tab, 不替换预览", () => {
    useChatTabsStore.getState().openSession(42);
    useChatTabsStore.getState().openSessionInNewTab(43);
    const { tabs, activeTabId } = useChatTabsStore.getState();
    expect(tabs).toHaveLength(2);
    expect(tabs[0].meta).toEqual({ kind: "session", sessionId: 42 });
    expect(tabs[0].isPreview).toBe(true);
    expect(tabs[1].meta).toEqual({ kind: "session", sessionId: 43 });
    expect(tabs[1].isPreview).toBe(false);
    expect(activeTabId).toBe(tabs[1].id);
  });

  it("session 已在某 tab 时, openSessionInNewTab 仍激活那条, 不新建", () => {
    useChatTabsStore.getState().openSession(42);
    useChatTabsStore.getState().openSessionInNewTab(42);
    expect(useChatTabsStore.getState().tabs).toHaveLength(1);
  });
});

describe("chat-tabs-store · openNewSession", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
    __setNowForTesting(() => 1000);
  });

  it("新建 kind:'new' tab, 非预览, 携带 projectId/agentId/workMode", () => {
    useChatTabsStore.getState().openNewSession(7, 3, "worktree");
    const { tabs, activeTabId } = useChatTabsStore.getState();
    expect(tabs).toHaveLength(1);
    expect(tabs[0].meta).toEqual({
      kind: "new",
      projectId: 7,
      agentId: 3,
      workMode: "worktree",
    });
    expect(tabs[0].isPreview).toBe(false);
    expect(activeTabId).toBe(tabs[0].id);
  });
});

describe("chat-tabs-store · promoteCurrent", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
  });

  it("把当前 active preview tab 翻成非预览", () => {
    useChatTabsStore.getState().openSession(42);
    useChatTabsStore.getState().promoteCurrent();
    expect(useChatTabsStore.getState().tabs[0].isPreview).toBe(false);
  });

  it("没有 active tab 时 promoteCurrent 是 noop", () => {
    useChatTabsStore.getState().promoteCurrent();
    expect(useChatTabsStore.getState().tabs).toHaveLength(0);
  });

  it("active 已经是非预览时 promoteCurrent 不变", () => {
    useChatTabsStore.getState().openSession(42);
    useChatTabsStore.getState().promoteCurrent();
    const before = useChatTabsStore.getState().tabs[0];
    useChatTabsStore.getState().promoteCurrent();
    expect(useChatTabsStore.getState().tabs[0]).toEqual(before);
  });
});

describe("chat-tabs-store · togglePin", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
    let t = 1000;
    __setNowForTesting(() => t++);
  });

  it("Pin: 把 isPinned 翻 true 并写 pinAt=now", () => {
    useChatTabsStore.getState().openSession(42);
    const id = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().togglePin(id);
    const t = useChatTabsStore.getState().tabs[0];
    expect(t.isPinned).toBe(true);
    expect(t.pinAt).toBeGreaterThan(0);
  });

  it("Unpin: 二次 toggle 翻回 false 并清空 pinAt", () => {
    useChatTabsStore.getState().openSession(42);
    const id = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().togglePin(id);
    useChatTabsStore.getState().togglePin(id);
    expect(useChatTabsStore.getState().tabs[0].isPinned).toBe(false);
    expect(useChatTabsStore.getState().tabs[0].pinAt).toBe(0);
  });

  it("Pin 自动 promote (pinned 不可能是预览)", () => {
    useChatTabsStore.getState().openSession(42);
    const id = useChatTabsStore.getState().tabs[0].id;
    expect(useChatTabsStore.getState().tabs[0].isPreview).toBe(true);
    useChatTabsStore.getState().togglePin(id);
    expect(useChatTabsStore.getState().tabs[0].isPreview).toBe(false);
  });

  it("pin 一个中间位置的 tab, 自动搬到 pinned 前缀末端", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const t1Id = useChatTabsStore.getState().tabs[0].id;
    const t3Id = useChatTabsStore.getState().tabs[2].id;
    useChatTabsStore.getState().togglePin(t1Id);
    // tabs = [P1, X2, X3]. Pin t3 → 应到 index 1, tabs = [P1, P3, X2]
    useChatTabsStore.getState().togglePin(t3Id);
    const tabs = useChatTabsStore.getState().tabs;
    expect(
      tabs.map((t) => (t.meta as { sessionId: number }).sessionId),
    ).toEqual([1, 3, 2]);
    expect(tabs.map((t) => t.isPinned)).toEqual([true, true, false]);
  });

  it("unpin 时位置不动", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const t1Id = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().togglePin(t1Id);
    useChatTabsStore.getState().togglePin(t1Id); // unpin
    const tabs = useChatTabsStore.getState().tabs;
    // 原位置 index 0, unpin 后还是 index 0
    expect((tabs[0].meta as { sessionId: number }).sessionId).toBe(1);
    expect(tabs[0].isPinned).toBe(false);
  });
});

describe("chat-tabs-store · setActive", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
  });

  it("切换到指定 tab id", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const firstId = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().setActive(firstId);
    expect(useChatTabsStore.getState().activeTabId).toBe(firstId);
  });

  it("不存在的 id 是 noop", () => {
    useChatTabsStore.getState().openSession(1);
    const before = useChatTabsStore.getState().activeTabId;
    useChatTabsStore.getState().setActive("nonexistent");
    expect(useChatTabsStore.getState().activeTabId).toBe(before);
  });
});

describe("chat-tabs-store · closeTab", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
  });

  it("关闭非 active tab: tabs 减 1, activeTabId 不变", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const second = useChatTabsStore.getState().tabs[1];
    const beforeActive = useChatTabsStore.getState().activeTabId;
    useChatTabsStore.getState().closeTab(second.id);
    expect(useChatTabsStore.getState().tabs).toHaveLength(2);
    expect(useChatTabsStore.getState().activeTabId).toBe(beforeActive);
  });

  it("关闭 active tab: 激活右邻居", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const second = useChatTabsStore.getState().tabs[1];
    useChatTabsStore.getState().setActive(second.id);
    useChatTabsStore.getState().closeTab(second.id);
    const tabs = useChatTabsStore.getState().tabs;
    // 第二个被关, 右邻居 (原本 index=2 → 现在 index=1) 应当激活
    expect(useChatTabsStore.getState().activeTabId).toBe(tabs[1].id);
  });

  it("关闭 active 末尾 tab: 激活左邻居", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const last = useChatTabsStore.getState().tabs[1];
    useChatTabsStore.getState().closeTab(last.id);
    expect(useChatTabsStore.getState().activeTabId).toBe(
      useChatTabsStore.getState().tabs[0].id,
    );
  });

  it("关闭最后一个 tab: activeTabId 变 null", () => {
    useChatTabsStore.getState().openSession(1);
    const onlyId = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().closeTab(onlyId);
    expect(useChatTabsStore.getState().tabs).toHaveLength(0);
    expect(useChatTabsStore.getState().activeTabId).toBe(null);
  });
});

describe("chat-tabs-store · closeOthers", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
  });

  it("保留目标 tab 与所有 pinned tab, 关闭其余", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    useChatTabsStore.getState().openSessionInNewTab(4);
    const tabs = useChatTabsStore.getState().tabs;
    // pin 第一个
    useChatTabsStore.getState().togglePin(tabs[0].id);
    // closeOthers 第三个: 留下 pinned(t1) + 目标(t3)
    useChatTabsStore.getState().closeOthers(tabs[2].id);
    const sessionIds = useChatTabsStore
      .getState()
      .tabs.map((t) => (t.meta as { sessionId: number }).sessionId);
    expect(sessionIds).toEqual([1, 3]);
  });

  it("active 在被关闭 tab 中时, 切到目标 tab", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const tabs = useChatTabsStore.getState().tabs;
    useChatTabsStore.getState().setActive(tabs[0].id);
    useChatTabsStore.getState().closeOthers(tabs[2].id);
    expect(useChatTabsStore.getState().activeTabId).toBe(tabs[2].id);
  });

  it("active 是目标 tab 时不变", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const tabs = useChatTabsStore.getState().tabs;
    useChatTabsStore.getState().setActive(tabs[1].id);
    useChatTabsStore.getState().closeOthers(tabs[1].id);
    expect(useChatTabsStore.getState().activeTabId).toBe(tabs[1].id);
  });
});

describe("chat-tabs-store · closeTabsToRight", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
  });

  it("关闭目标右侧的非 pinned tab, 保留 pinned", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    useChatTabsStore.getState().openSessionInNewTab(4);
    let tabs = useChatTabsStore.getState().tabs;
    // pin 第四个 (右侧)
    useChatTabsStore.getState().togglePin(tabs[3].id);
    // 在 togglePin 之后重新获取 tabs，因为 pin 会搬位置
    // 现在 tabs = [P4, X1, X2, X3], 要对 X2 (sessionId=2) closeTabsToRight
    tabs = useChatTabsStore.getState().tabs;
    const tab2 = tabs.find(
      (t) => (t.meta as { sessionId: number }).sessionId === 2,
    );
    useChatTabsStore.getState().closeTabsToRight(tab2!.id);
    const sessionIds = useChatTabsStore
      .getState()
      .tabs.map((t) => (t.meta as { sessionId: number }).sessionId);
    expect(sessionIds).toEqual([4, 1, 2]);
  });

  it("active 在右侧被关闭 tab 中时, 切到目标 tab", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const tabs = useChatTabsStore.getState().tabs;
    useChatTabsStore.getState().setActive(tabs[2].id);
    useChatTabsStore.getState().closeTabsToRight(tabs[0].id);
    expect(useChatTabsStore.getState().activeTabId).toBe(tabs[0].id);
  });

  it("目标已是最右 tab 时是 noop", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const tabs = useChatTabsStore.getState().tabs;
    const beforeActive = useChatTabsStore.getState().activeTabId;
    useChatTabsStore.getState().closeTabsToRight(tabs[1].id);
    expect(useChatTabsStore.getState().tabs).toHaveLength(2);
    expect(useChatTabsStore.getState().activeTabId).toBe(beforeActive);
  });
});

describe("chat-tabs-store · resolveNewTab", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
  });

  it("把 kind:'new' tab 翻成 kind:'session', tabId 不变", () => {
    useChatTabsStore.getState().openNewSession(7, 3, "shared");
    const tabId = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().resolveNewTab(tabId, 99);
    const t = useChatTabsStore.getState().tabs[0];
    expect(t.id).toBe(tabId);
    expect(t.meta).toEqual({ kind: "session", sessionId: 99 });
    expect(t.isPreview).toBe(false);
  });

  it("非 kind:'new' tab 是 noop", () => {
    useChatTabsStore.getState().openSession(1);
    const tabId = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().resolveNewTab(tabId, 99);
    expect(useChatTabsStore.getState().tabs[0].meta).toEqual({
      kind: "session",
      sessionId: 1,
    });
  });
});

describe("chat-tabs-store · reconcileMissingSessions", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
  });

  it("DB 不存在的 session tab 被剔除", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    useChatTabsStore.getState().reconcileMissingSessions(new Set([1, 3]));
    const tabs = useChatTabsStore.getState().tabs;
    expect(
      tabs.map((t) => (t.meta as { sessionId: number }).sessionId),
    ).toEqual([1, 3]);
  });

  it("剔除的 tab 是 active 时, 回退到 sortedTabs[0] 或 null", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const secondId = useChatTabsStore.getState().tabs[1].id;
    useChatTabsStore.getState().setActive(secondId);
    useChatTabsStore.getState().reconcileMissingSessions(new Set([1]));
    expect(useChatTabsStore.getState().activeTabId).toBe(
      useChatTabsStore.getState().tabs[0].id,
    );
  });

  it("kind:'new' tab 不受 reconcile 影响", () => {
    useChatTabsStore.getState().openNewSession(7, 3, "");
    useChatTabsStore.getState().reconcileMissingSessions(new Set());
    expect(useChatTabsStore.getState().tabs).toHaveLength(1);
  });

  it("保留 terminal tab(不因 session 缺失被移除)", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openTerminal(5, "", undefined);
    useChatTabsStore.getState().openSessionInNewTab(2);
    // session 2 gone; session 1 kept; terminal must also survive
    useChatTabsStore.getState().reconcileMissingSessions(new Set([1]));
    const s = useChatTabsStore.getState();
    expect(s.tabs.map((t) => t.meta.kind)).toEqual(["session", "terminal"]);
  });
});

describe("chat-tabs-store · moveTab", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
    __setNowForTesting(() => 1000);
  });

  it("把 index 0 的 tab 移到 index 2, 数组顺序更新, isPinned 不变", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    useChatTabsStore.getState().moveTab(0, 2);
    expect(
      useChatTabsStore
        .getState()
        .tabs.map((t) => (t.meta as { sessionId: number }).sessionId),
    ).toEqual([2, 3, 1]);
  });

  it("from === to 时无副作用, 不改 state 引用", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const before = useChatTabsStore.getState().tabs;
    useChatTabsStore.getState().moveTab(1, 1);
    expect(useChatTabsStore.getState().tabs).toBe(before);
  });

  it("越界 from/to 不抛, state 不变", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    const before = useChatTabsStore.getState().tabs;
    useChatTabsStore.getState().moveTab(5, 0);
    useChatTabsStore.getState().moveTab(0, 5);
    expect(useChatTabsStore.getState().tabs).toBe(before);
  });

  it("拖动 pinned tab 到 pinned 前缀之外, isPinned 自动变 false 且 pinAt 清零", () => {
    // tabs = [P1, P2, X, Y, Z],把 P2 拖到 index 4
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    useChatTabsStore.getState().openSessionInNewTab(4);
    useChatTabsStore.getState().openSessionInNewTab(5);
    const t1Id = useChatTabsStore.getState().tabs[0].id;
    const t2Id = useChatTabsStore.getState().tabs[1].id;
    useChatTabsStore.getState().togglePin(t1Id);
    useChatTabsStore.getState().togglePin(t2Id);

    // 此时 tabs = [P1, P2, X, Y, Z], pinned 前缀末端 = 1。
    // 拖 P2 (index 1) 到 index 4。
    useChatTabsStore.getState().moveTab(1, 4);
    const tabs = useChatTabsStore.getState().tabs;
    const moved = tabs[4];
    expect((moved.meta as { sessionId: number }).sessionId).toBe(2);
    expect(moved.isPinned).toBe(false);
    expect(moved.pinAt).toBe(0);
    // P1 仍然在 index 0, 仍 pinned
    expect(tabs[0].isPinned).toBe(true);
  });

  it("拖动 pinned tab 在 pinned 前缀内换位, 保持 pinned", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const t1Id = useChatTabsStore.getState().tabs[0].id;
    const t2Id = useChatTabsStore.getState().tabs[1].id;
    const t3Id = useChatTabsStore.getState().tabs[2].id;
    useChatTabsStore.getState().togglePin(t1Id);
    useChatTabsStore.getState().togglePin(t2Id);
    useChatTabsStore.getState().togglePin(t3Id);
    // tabs = [P1, P2, P3], 全 pinned。拖 P1 → index 2。
    useChatTabsStore.getState().moveTab(0, 2);
    const tabs = useChatTabsStore.getState().tabs;
    expect(tabs.map((t) => t.isPinned)).toEqual([true, true, true]);
    expect((tabs[2].meta as { sessionId: number }).sessionId).toBe(1);
  });

  it("拖动非 pinned tab 进入 pinned 前缀, 不自动 pin", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const t1Id = useChatTabsStore.getState().tabs[0].id;
    useChatTabsStore.getState().togglePin(t1Id);
    // tabs = [P1, X2, X3]。拖 X3 (index 2) 到 index 0。
    useChatTabsStore.getState().moveTab(2, 0);
    const tabs = useChatTabsStore.getState().tabs;
    expect(tabs[0].isPinned).toBe(false);
    expect((tabs[0].meta as { sessionId: number }).sessionId).toBe(3);
  });
});

describe("chat-tabs-store · openTerminal", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    __setNowForTesting(() => 1000);
  });

  it("openTerminal 新增 terminal tab 并激活", () => {
    let n = 0;
    __setNextIdFactoryForTesting(() => `id-${++n}`);
    useChatTabsStore.getState().openTerminal(7, "", undefined);
    const s = useChatTabsStore.getState();
    expect(s.tabs).toHaveLength(1);
    expect(s.tabs[0].meta).toMatchObject({
      kind: "terminal",
      projectId: 7,
      deviceId: "",
    });
    expect((s.tabs[0].meta as { terminalId: string }).terminalId).toBeTruthy();
    expect(s.tabs[0].title).toBe("Terminal");
    expect(s.tabs[0].isPreview).toBe(false);
    expect(s.activeTabId).toBe(s.tabs[0].id);
  });

  it("openTerminal 远端带设备名进标题", () => {
    let n = 0;
    __setNextIdFactoryForTesting(() => `id-${++n}`);
    useChatTabsStore.getState().openTerminal(7, "42", "MacMini");
    const tab = useChatTabsStore.getState().tabs.at(-1)!;
    expect(tab.meta).toMatchObject({ kind: "terminal", deviceId: "42" });
    expect(tab.title).toBe("Terminal · MacMini");
  });
});

describe("chat-tabs-store · bumpToAfterPinned", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    let nid = 1;
    __setNextIdFactoryForTesting(() => `t${nid++}`);
    __setNowForTesting(() => 1000);
  });

  it("把指定 tab 搬到 lastPinnedPrefixIndex + 1, 不动 isPinned", () => {
    // tabs = [P1, X2, X3, X4]
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    useChatTabsStore.getState().openSessionInNewTab(4);
    const t1Id = useChatTabsStore.getState().tabs[0].id;
    const t4Id = useChatTabsStore.getState().tabs[3].id;
    useChatTabsStore.getState().togglePin(t1Id);
    // bump X4 (index 3) → 应该到 index 1
    useChatTabsStore.getState().bumpToAfterPinned(t4Id);
    const tabs = useChatTabsStore.getState().tabs;
    expect((tabs[1].meta as { sessionId: number }).sessionId).toBe(4);
    expect(tabs[1].isPinned).toBe(false);
  });

  it("没有 pinned 时, 搬到 index 0", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    useChatTabsStore.getState().openSessionInNewTab(3);
    const t3Id = useChatTabsStore.getState().tabs[2].id;
    useChatTabsStore.getState().bumpToAfterPinned(t3Id);
    expect(
      (useChatTabsStore.getState().tabs[0].meta as { sessionId: number })
        .sessionId,
    ).toBe(3);
  });

  it("tab 已在目标位置时无副作用", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const t1Id = useChatTabsStore.getState().tabs[0].id;
    const before = useChatTabsStore.getState().tabs;
    useChatTabsStore.getState().bumpToAfterPinned(t1Id);
    expect(useChatTabsStore.getState().tabs).toBe(before);
  });

  it("未知 id 不抛, state 不变", () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    const before = useChatTabsStore.getState().tabs;
    useChatTabsStore.getState().bumpToAfterPinned("nope");
    expect(useChatTabsStore.getState().tabs).toBe(before);
  });
});
