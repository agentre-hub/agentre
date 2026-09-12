import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import React from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentSlim } from "@/hooks/use-chat-agents";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import { consumeNewAgentDialogIntent } from "@/stores/new-agent-intent-store";

// 直接 mock 整个 useChatAgents hook —— 避开 Wails 模块多层 mock 的解析路径风险。
const hookMocks = vi.hoisted(() => ({
  useChatAgents: vi.fn(),
}));

vi.mock("@/hooks/use-chat-agents", () => ({
  useChatAgents: hookMocks.useChatAgents,
}));

import { CommandPalette } from "../../command-palette";
import { CMD_NEW_CHAT_ID } from "../../shortcuts/registry";
import {
  ShortcutsProvider,
  useShortcutsContext,
} from "../../shortcuts/shortcuts-provider";
import type { KeyChord } from "../../shortcuts/types";

const MOCK_AGENTS = [
  {
    id: 1,
    name: "CEO 助手",
    avatarColor: "agent-1",
    avatarIcon: "",
    avatarDataUrl: "",
    backendType: "",
    chattable: true,
    pinned: true,
    chattableHint: "",
    activeCount: 1,
    recentCount: 2,
    totalSessions: 2,
    sessions: [
      {
        id: 101,
        title: "年度报告 v2",
        status: "running",
        needsAttention: false,
        lastMessageAt: Date.now(),
      },
      {
        id: 102,
        title: "周报草稿",
        status: "idle",
        needsAttention: false,
        lastMessageAt: Date.now() - 10000,
      },
    ],
    attentionSessions: [],
  },
] as unknown as AgentSlim[];

beforeEach(() => {
  useCommandPaletteStore.setState({ open: false });
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  hookMocks.useChatAgents.mockReturnValue({
    agents: MOCK_AGENTS,
    loading: false,
    error: null,
    reload: vi.fn(),
  });
});

function LocationProbe() {
  return <output data-testid="location">{useLocation().pathname}</output>;
}

function renderPalette() {
  return render(
    <MemoryRouter initialEntries={["/projects"]}>
      <LocationProbe />
      <ShortcutsProvider platform="darwin">
        <CommandPalette />
      </ShortcutsProvider>
    </MemoryRouter>,
  );
}

describe("CommandPalette", () => {
  it("renders nothing when palette is closed", () => {
    renderPalette();
    expect(screen.queryByPlaceholderText("Search sessions...")).toBeNull();
  });

  it("opens via store and lists active-first sessions", async () => {
    renderPalette();
    act(() => useCommandPaletteStore.getState().setOpen(true));
    await waitFor(() =>
      expect(
        screen.getByPlaceholderText("Search sessions..."),
      ).toBeInTheDocument(),
    );
    expect(await screen.findByText("年度报告 v2")).toBeInTheDocument();
    expect(screen.getByText("周报草稿")).toBeInTheDocument();
  });

  it("filters by pinyin (ndbg → 年度报告)", async () => {
    renderPalette();
    act(() => useCommandPaletteStore.getState().setOpen(true));
    const input = await screen.findByPlaceholderText("Search sessions...");
    await screen.findByText("年度报告 v2");
    await userEvent.type(input, "ndbg");
    await waitFor(() => {
      expect(screen.queryByText("周报草稿")).toBeNull();
    });
    expect(screen.getByText("年度报告 v2")).toBeInTheDocument();
  });

  it("clicking a session opens a session tab and closes palette", async () => {
    renderPalette();
    act(() => useCommandPaletteStore.getState().setOpen(true));
    const row = await screen.findByText("年度报告 v2");
    await userEvent.click(row);
    await waitFor(() => {
      const { tabs, activeTabId } = useChatTabsStore.getState();
      expect(tabs).toHaveLength(1);
      expect(tabs[0].id).toBe(activeTabId);
      expect(tabs[0].meta).toEqual({ kind: "session", sessionId: 101 });
      expect(useCommandPaletteStore.getState().open).toBe(false);
    });
  });

  it("Given command mode, when New agent is selected, then it navigates to org and requests the dialog", async () => {
    consumeNewAgentDialogIntent();
    renderPalette();
    act(() => useCommandPaletteStore.getState().openWith("> New agent"));

    await userEvent.click(await screen.findByText("New agent"));

    expect(screen.getByTestId("location")).toHaveTextContent("/org");
    expect(consumeNewAgentDialogIntent()).toBe(true);
  });
});

describe("CommandPalette — Footer 新建对话快捷键提示（快捷键 Provider 集成）", () => {
  function RebindNewChat({ chord }: { chord: KeyChord }) {
    const { setBinding } = useShortcutsContext();
    React.useEffect(() => {
      setBinding(CMD_NEW_CHAT_ID, chord);
      // 只挂载时重绑一次：验证 Footer 从同一份绑定数据重新格式化
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);
    return null;
  }

  function renderPaletteWithRebind(chord: KeyChord) {
    return render(
      <MemoryRouter initialEntries={["/projects"]}>
        <ShortcutsProvider platform="darwin">
          <RebindNewChat chord={chord} />
          <CommandPalette />
        </ShortcutsProvider>
      </MemoryRouter>,
    );
  }

  it("默认模式显示 cmd.new-chat 默认绑定 ⌘N（macOS）", async () => {
    renderPalette();
    act(() => useCommandPaletteStore.getState().setOpen(true));

    expect(await screen.findByText("New chat")).toBeInTheDocument();
    expect(screen.getByText("⌘N")).toBeInTheDocument();
  });

  it("重绑 cmd.new-chat 后 Footer 立即显示重绑后的按键", async () => {
    renderPaletteWithRebind({ mod: "primary-shift", key: "K" });
    act(() => useCommandPaletteStore.getState().setOpen(true));

    expect(await screen.findByText("New chat")).toBeInTheDocument();
    expect(await screen.findByText("⌘⇧K")).toBeInTheDocument();
  });
});
