import { beforeEach, describe, expect, it } from "vitest";
import {
  CHAT_TABS_STORAGE_KEY,
  readPersistedTabs,
  writePersistedTabs,
} from "../chat-tabs-persistence";
import type { ChatTab } from "../chat-tabs-store";

beforeEach(() => {
  localStorage.clear();
});

describe("chat-tabs-persistence · write", () => {
  it("写入 kind:'session' + isPinned + activeTabId, 跳过 kind:'new' 和 preview", () => {
    const tabs: ChatTab[] = [
      {
        id: "t1",
        meta: { kind: "session", sessionId: 1 },
        isPreview: false,
        isPinned: true,
        pinAt: 100,
        openedAt: 50,
      },
      {
        id: "t2",
        meta: { kind: "session", sessionId: 2 },
        isPreview: true,
        isPinned: false,
        pinAt: 0,
        openedAt: 60,
      },
      {
        id: "t3",
        meta: { kind: "new", projectId: 7, agentId: 3, workMode: "" },
        isPreview: false,
        isPinned: false,
        pinAt: 0,
        openedAt: 70,
      },
    ];
    writePersistedTabs(tabs, "t1");
    const raw = localStorage.getItem(CHAT_TABS_STORAGE_KEY);
    expect(raw).toBeTruthy();
    const parsed = JSON.parse(raw!);
    expect(parsed).toMatchObject({
      v: 2,
      tabs: [
        {
          id: "t1",
          meta: { kind: "session", sessionId: 1 },
          isPinned: true,
          pinAt: 100,
          openedAt: 50,
        },
      ],
      activeTabId: "t1",
    });
  });
});

describe("chat-tabs-persistence · read", () => {
  it("schema 版本不匹配返回 null", () => {
    localStorage.setItem(
      CHAT_TABS_STORAGE_KEY,
      JSON.stringify({ v: 99, tabs: [] }),
    );
    expect(readPersistedTabs()).toBeNull();
  });

  it("解析失败返回 null", () => {
    localStorage.setItem(CHAT_TABS_STORAGE_KEY, "not-json");
    expect(readPersistedTabs()).toBeNull();
  });

  it("空 storage 返回 null", () => {
    expect(readPersistedTabs()).toBeNull();
  });
});

describe("chat-tabs-persistence · v2 升级", () => {
  it("terminal tab 往返持久化不被丢弃", () => {
    const tab: ChatTab = {
      id: "t1",
      meta: {
        kind: "terminal",
        projectId: 7,
        deviceId: "",
        terminalId: "term-1",
      },
      isPreview: false,
      isPinned: false,
      pinAt: 0,
      openedAt: 1,
      title: "终端",
    };
    writePersistedTabs([tab], "t1");
    const restored = readPersistedTabs();
    expect(restored?.tabs).toHaveLength(1);
    expect(restored?.tabs[0].meta).toMatchObject({
      kind: "terminal",
      projectId: 7,
      deviceId: "",
      terminalId: "term-1",
    });
  });

  it("session tab 仍能往返", () => {
    const tab: ChatTab = {
      id: "s1",
      meta: { kind: "session", sessionId: 9 },
      isPreview: false,
      isPinned: true,
      pinAt: 5,
      openedAt: 1,
    };
    writePersistedTabs([tab], "s1");
    expect(readPersistedTabs()?.tabs[0].meta).toMatchObject({
      kind: "session",
      sessionId: 9,
    });
  });

  it("new / preview tab 不被持久化", () => {
    const tabs: ChatTab[] = [
      {
        id: "n",
        meta: { kind: "new", projectId: 1, agentId: 1, workMode: "code" },
        isPreview: false,
        isPinned: false,
        pinAt: 0,
        openedAt: 1,
      },
      {
        id: "p",
        meta: { kind: "session", sessionId: 5 },
        isPreview: true,
        isPinned: false,
        pinAt: 0,
        openedAt: 1,
      },
    ];
    writePersistedTabs(tabs, "n");
    expect(readPersistedTabs()?.tabs).toHaveLength(0);
  });

  it("attach terminal tab 不被持久化,普通 terminal tab 被持久化", () => {
    const attachTab: ChatTab = {
      id: "at",
      meta: {
        kind: "terminal",
        projectId: 0,
        deviceId: "",
        terminalId: "some-uuid-123",
        attach: true,
        command: "pnpm test",
      },
      isPreview: false,
      isPinned: false,
      pinAt: 0,
      openedAt: 10,
    };
    const regularTab: ChatTab = {
      id: "rt",
      meta: {
        kind: "terminal",
        projectId: 7,
        deviceId: "",
        terminalId: "term-real",
      },
      isPreview: false,
      isPinned: false,
      pinAt: 0,
      openedAt: 20,
      title: "终端",
    };
    writePersistedTabs([attachTab, regularTab], "rt");
    const restored = readPersistedTabs();
    // attach tab must NOT be persisted
    expect(
      restored?.tabs.some((t) => t.id === "at"),
      "attach tab should not be persisted",
    ).toBe(false);
    // regular terminal tab must be persisted
    expect(
      restored?.tabs.some((t) => t.id === "rt"),
      "regular terminal tab should be persisted",
    ).toBe(true);
    expect(restored?.tabs).toHaveLength(1);
  });
});
