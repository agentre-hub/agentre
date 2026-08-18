import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { chat_svc } from "../../../../wailsjs/go/models";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";

const appMocks = vi.hoisted(() => ({
  ListChatAgents: vi.fn(),
  ProjectGet: vi.fn(),
  ProjectLocationList: vi.fn(),
  ProjectListTree: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

import { CommandPalette } from "./command-palette";

function mkAgent(
  over: Partial<chat_svc.ChatAgentItem> = {},
): chat_svc.ChatAgentItem {
  return {
    id: 1,
    name: "Agent",
    avatarColor: "agent-1",
    avatarIcon: "",
    avatarDataUrl: "",
    backendType: "claudecode",
    chattable: true,
    pinned: false,
    chattableHint: "",
    activeCount: 0,
    recentCount: 0,
    totalSessions: 0,
    sessions: [],
    attentionSessions: [],
    ...over,
  } as chat_svc.ChatAgentItem;
}

// 默认 /chat —— 会话索引唯一的落点。项目已经不是页面，命令面板里
// 「新建对话落在哪」由 new-chat-context store 决定，与路由无关。
function renderHarness(initialPath = "/chat") {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <CommandPalette />
    </MemoryRouter>,
  );
}

async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

beforeEach(() => {
  appMocks.ListChatAgents.mockReset();
  appMocks.ProjectGet.mockReset();
  appMocks.ProjectLocationList.mockReset();
  appMocks.ProjectListTree.mockReset();
  appMocks.ProjectGet.mockResolvedValue({
    project: null,
    directMembers: [],
    inheritedMembers: [],
  });
  appMocks.ProjectLocationList.mockResolvedValue([]);
  appMocks.ProjectListTree.mockResolvedValue([]);
  useChatAgentsStore.getState().__reset();
  useCommandPaletteStore.setState({ open: false, initialQuery: "" });
  useNewChatContextStore.getState().clear();
  localStorage.clear();
});

afterEach(() => {
  useCommandPaletteStore.setState({ open: false, initialQuery: "" });
  useNewChatContextStore.getState().clear();
});

describe("CommandPalette — ⌘N opens command mode and lists agents (BDD)", () => {
  it("Given /chat route + ⌘N seeds '> ', When palette opens, Then 命令 chip shows, chattable agents as 'New chat with', non-chattable in a 'Needs setup' group", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        mkAgent({ id: 1, name: "CEO 助手" }),
        mkAgent({ id: 2, name: "工程师" }),
        mkAgent({
          id: 3,
          name: "未绑后端",
          chattable: false,
          chattableHint: "请在组织页绑定后端",
          blockReason: "no-backend",
        }),
      ],
    });

    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.getByLabelText("Command mode")).toBeTruthy();
    expect(screen.getByText("CEO 助手")).toBeTruthy();
    expect(screen.getByText("工程师")).toBeTruthy();
    // 不可对话 agent 不再被过滤：单列「Needs setup」组
    expect(screen.getByText("Needs setup")).toBeTruthy();
    expect(screen.getByText("未绑后端")).toBeTruthy();

    // /chat 路由：newChatSource 激活 → "New chat with"
    const labels = screen.getAllByText("New chat with");
    expect(labels.length).toBeGreaterThanOrEqual(2);
    // 同时 newProjectChatSource 未激活
    expect(screen.queryByText("New project chat with")).toBeNull();
  });
});

describe("CommandPalette — ContextBar 项目 chip 可点击切换 (BDD)", () => {
  function mkProject(
    over: Partial<{
      id: number;
      name: string;
    }> = {},
  ) {
    return {
      id: 1,
      parentID: 0,
      name: "项目 A",
      icon: "",
      color: "",
      description: "",
      path: "",
      isGitRepo: false,
      createtime: 0,
      updatetime: 0,
      ...over,
    };
  }

  it("Given projects in tree, When clicking the project chip, Then the popover lists 无项目 + each project", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      { project: mkProject({ id: 10, name: "后端重构" }), children: [] },
      { project: mkProject({ id: 11, name: "前端 UI" }), children: [] },
    ]);
    renderHarness();

    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const chip = screen.getByLabelText("Switch project context");
    await act(async () => {
      fireEvent.click(chip);
    });
    await flush();

    // chip 本身也叫"无项目"，popover 里会再出现一次 → 用 getAllByText
    expect(screen.getAllByText("No project").length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("后端重构")).toBeTruthy();
    expect(screen.getByText("前端 UI")).toBeTruthy();
  });

  it("Selecting a project writes context store + closes popover", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      {
        project: mkProject({
          id: 42,
          name: "后端重构",
        }),
        children: [],
      },
    ]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    fireEvent.click(screen.getByLabelText("Switch project context"));
    await flush();
    fireEvent.click(screen.getByText("后端重构"));
    await flush();

    const ctx = useNewChatContextStore.getState().projectContext;
    expect(ctx).toEqual({
      projectID: 42,
      projectName: "后端重构",
    });
  });

  it("Selecting 无项目 clears the context", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();

    // 预置一个 context
    await act(async () => {
      useNewChatContextStore.getState().setContext({
        projectID: 42,
        projectName: "后端重构",
      });
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    fireEvent.click(screen.getByLabelText("Switch project context"));
    await flush();
    fireEvent.click(screen.getByText("No project"));
    await flush();

    expect(useNewChatContextStore.getState().projectContext).toBeNull();
  });
});

describe("CommandPalette — Backspace 键盘流：先清 context，再退出命令模式 (BDD)", () => {
  it("Given empty payload + projectContext set, Then Backspace clears the context but stays in command mode", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();

    await act(async () => {
      useNewChatContextStore.getState().setContext({
        projectID: 42,
        projectName: "后端重构",
      });
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    // 还在命令模式
    expect(screen.queryByLabelText("Command mode")).toBeTruthy();

    // 按 Backspace（Input 已聚焦：autoFocus）
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "Backspace" });
    await flush();

    // context 清了
    expect(useNewChatContextStore.getState().projectContext).toBeNull();
    // 但还在命令模式
    expect(screen.queryByLabelText("Command mode")).toBeTruthy();
  });

  it("Given empty payload + NO projectContext, Then Backspace exits command mode (legacy behavior preserved)", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.queryByLabelText("Command mode")).toBeTruthy();
    const input = screen.getByRole("combobox");
    fireEvent.keyDown(input, { key: "Backspace" });
    await flush();
    // 退出命令模式（chip 消失）
    expect(screen.queryByLabelText("Command mode")).toBeNull();
  });
});

describe("CommandPalette — last-context persistence (BDD: 默认值 = 上次手动选过的)", () => {
  it("Given user picks a project in palette, When palette is reopened without project-page injecting, Then the last picked project is restored", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      {
        project: {
          id: 42,
          parentID: 0,
          name: "后端重构",
          icon: "",
          color: "",
          description: "",
          path: "",
          isGitRepo: false,
          createtime: 0,
          updatetime: 0,
        },
        children: [],
      },
    ]);

    renderHarness();

    // 第一次开：选项目 42
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();
    fireEvent.click(screen.getByLabelText("Switch project context"));
    await flush();
    fireEvent.click(screen.getByText("后端重构"));
    await flush();

    // 关 palette + 清 store（模拟切到对话页 / 切到其它 nav，project-page unmount 不再注入）
    await act(async () => {
      useCommandPaletteStore.getState().close();
      useNewChatContextStore.getState().clear();
    });
    await flush();

    // 再打开 palette（⌘N）
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    // 应回填上次的项目
    const ctx = useNewChatContextStore.getState().projectContext;
    expect(ctx?.projectID).toBe(42);
    expect(ctx?.projectName).toBe("后端重构");
  });

  it("project-page injection wins over localStorage fallback", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    // 模拟 localStorage 里有"上次"选过的项目 42
    localStorage.setItem(
      "agentre.commandPalette.lastContext",
      JSON.stringify({
        projectID: 42,
        projectName: "旧",
      }),
    );
    renderHarness();
    // 模拟 project-page mount 写入了新项目 99
    await act(async () => {
      useNewChatContextStore.getState().setContext({
        projectID: 99,
        projectName: "新",
      });
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();
    // 新的（store 已存）应该胜出
    expect(useNewChatContextStore.getState().projectContext?.projectID).toBe(
      99,
    );
  });

  it("Selecting 无项目 clears localStorage too", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    localStorage.setItem(
      "agentre.commandPalette.lastContext",
      JSON.stringify({
        projectID: 42,
        projectName: "旧",
      }),
    );

    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    fireEvent.click(screen.getByLabelText("Switch project context"));
    await flush();
    // 取最后一个匹配（popover 在 Portal，DOM 后插入；chip 是先存在的）
    const matches = screen.getAllByText("No project");
    fireEvent.click(matches[matches.length - 1]!);
    await flush();

    expect(
      localStorage.getItem("agentre.commandPalette.lastContext"),
    ).toBeNull();
  });
});

describe("CommandPalette — 视觉键盘提示（与 Pencil 设计稿一致）", () => {
  it("ContextBar 右侧显示 Tab 切项目", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.getAllByText("Tab").length).toBeGreaterThan(0);
    expect(screen.getByText("Switch project")).toBeTruthy();
  });

  it("Footer 在命令模式下展示 ⌫ 清上下文 提示", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.getByText("Clear context")).toBeTruthy();
    expect(screen.getByText("⌫")).toBeTruthy();
  });

  it("Footer 在默认模式下 NOT 显示 ⌫ 清上下文 提示", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().toggle(); // 默认模式
    });
    await flush();

    expect(screen.queryByText("Clear context")).toBeNull();
    expect(screen.queryByText("⌫")).toBeNull();
  });

  it("Footer 在默认模式下显示新建对话快捷键提示（脱离 Provider 时兜底 ⌘N）", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().toggle(); // 默认模式
    });
    await flush();

    expect(screen.getByText("New chat")).toBeTruthy();
    expect(screen.getByText("⌘N")).toBeTruthy();
  });

  it("Footer 在命令模式下 NOT 显示新建对话快捷键提示，保留命令模式状态标识", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.getByText("Command mode")).toBeTruthy();
    expect(screen.queryByText("New chat")).toBeNull();
  });
});

describe("CommandPalette — Tab 直接切上下文 (BDD)", () => {
  function mkProject(
    over: Partial<{
      id: number;
      name: string;
    }> = {},
  ) {
    return {
      id: 1,
      parentID: 0,
      name: "项目 A",
      icon: "",
      color: "",
      description: "",
      path: "",
      isGitRepo: false,
      createtime: 0,
      updatetime: 0,
      ...over,
    };
  }

  it("Given projects [A,B] + 无项目, When Tab in Input, Then projectContext === A 且 focus 留在 Input", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      { project: mkProject({ id: 10, name: "后端重构" }), children: [] },
      { project: mkProject({ id: 11, name: "前端 UI" }), children: [] },
    ]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    input.focus();
    fireEvent.keyDown(input, { key: "Tab" });

    expect(useNewChatContextStore.getState().projectContext?.projectID).toBe(
      10,
    );
    expect(document.activeElement).toBe(input);
  });

  it("Given projectContext === A (idx 0), When Tab, Then projectContext === B (idx 1)", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      { project: mkProject({ id: 10, name: "A" }), children: [] },
      { project: mkProject({ id: 11, name: "B" }), children: [] },
    ]);
    renderHarness();
    await act(async () => {
      useNewChatContextStore.getState().setContext({
        projectID: 10,
        projectName: "A",
      });
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    input.focus();
    fireEvent.keyDown(input, { key: "Tab" });

    expect(useNewChatContextStore.getState().projectContext?.projectID).toBe(
      11,
    );
  });

  it("Given projectContext === B (末尾), When Tab, Then projectContext === null (回到无项目)", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      { project: mkProject({ id: 10, name: "A" }), children: [] },
      { project: mkProject({ id: 11, name: "B" }), children: [] },
    ]);
    renderHarness();
    await act(async () => {
      useNewChatContextStore.getState().setContext({
        projectID: 11,
        projectName: "B",
      });
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    input.focus();
    fireEvent.keyDown(input, { key: "Tab" });

    expect(useNewChatContextStore.getState().projectContext).toBeNull();
  });

  it("Given projectContext === B (idx 1), When Shift+Tab, Then projectContext === A (idx 0)", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      { project: mkProject({ id: 10, name: "A" }), children: [] },
      { project: mkProject({ id: 11, name: "B" }), children: [] },
    ]);
    renderHarness();
    await act(async () => {
      useNewChatContextStore.getState().setContext({
        projectID: 11,
        projectName: "B",
      });
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    input.focus();
    fireEvent.keyDown(input, { key: "Tab", shiftKey: true });

    expect(useNewChatContextStore.getState().projectContext).toEqual({
      projectID: 10,
      projectName: "A",
    });
    expect(document.activeElement).toBe(input);
  });

  it("Given projectContext === null, When Shift+Tab, Then projectContext === last project", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      { project: mkProject({ id: 10, name: "A" }), children: [] },
      { project: mkProject({ id: 11, name: "B" }), children: [] },
    ]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    input.focus();
    fireEvent.keyDown(input, { key: "Tab", shiftKey: true });

    expect(useNewChatContextStore.getState().projectContext).toEqual({
      projectID: 11,
      projectName: "B",
    });
  });

  it("Given 无项目列表 + 无 context, When Tab, Then projectContext 保持 null (no-op)", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    input.focus();
    fireEvent.keyDown(input, { key: "Tab" });

    expect(useNewChatContextStore.getState().projectContext).toBeNull();
  });

  it("默认模式 (无 > 前缀): Tab 在 Input 中不改 projectContext", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      { project: mkProject({ id: 10, name: "A" }), children: [] },
    ]);
    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().toggle(); // 默认模式打开
    });
    await flush();

    expect(screen.queryByLabelText("Switch project context")).toBeNull();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    input.focus();
    fireEvent.keyDown(input, { key: "Tab" });

    expect(useNewChatContextStore.getState().projectContext).toBeNull();
  });
});

describe("CommandPalette — ⌘N 的项目选择不挂在 /projects 路由上（回归）", () => {
  // 「项目」合并进会话索引后 /projects 只剩一条重定向，索引页本身挂在 /chat。
  // 项目选择条一旦按路由开关，用户就再也够不到它。
  it("Given 会话索引所在的 /chat + ⌘N, Then ContextBar 可见，项目可选", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "CEO 助手" })],
    });
    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.getByLabelText("Switch project context")).toBeTruthy();
    expect(screen.getByText(/No project/)).toBeTruthy();
  });

  it("Given /chat + 已选项目上下文 + ⌘N, Then 出 'New project chat with'，自由 'New chat with' 让位", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "CEO 助手" })],
    });
    appMocks.ProjectGet.mockResolvedValue({
      project: { id: 1, name: "Agentre" },
      directMembers: [{ agentID: 1 }],
      inheritedMembers: [],
    });
    useNewChatContextStore
      .getState()
      .setContext({ projectID: 1, projectName: "Agentre" });

    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.getByText("New project chat with")).toBeTruthy();
    expect(screen.queryByText("New chat with")).toBeNull();
    expect(await screen.findByText("New chat in Agentre")).toBeTruthy();
  });
});

describe("CommandPalette — 两个命令 source 按项目上下文互斥", () => {
  it("无项目上下文 ⌘N → 只显示 'New chat with X'，不显示 'New project chat with'；ContextBar 仍在（随时可选项目）", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "CEO 助手" })],
    });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.getByText("New chat with")).toBeTruthy();
    expect(screen.queryByText("New project chat with")).toBeNull();
    // ContextBar 常驻：不选项目也要能看见「现在落在哪」+ 改掉它
    expect(screen.getByLabelText("Switch project context")).toBeTruthy();
  });

  it("已选项目上下文 ⌘N → 只显示 'New project chat with X'，不显示自由 'New chat with' + ContextBar 可见", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "CEO 助手" })],
    });
    appMocks.ProjectListTree.mockResolvedValue([]);
    useNewChatContextStore
      .getState()
      .setContext({ projectID: 1, projectName: "Agentre" });
    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(screen.getByText("New project chat with")).toBeTruthy();
    expect(screen.queryByText("New chat with")).toBeNull();
    // ContextBar 可见
    expect(screen.getByLabelText("Switch project context")).toBeTruthy();
  });

  it("⌘N + Tab 在 /chat 也切上下文（项目不再是一个路由）", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    appMocks.ProjectListTree.mockResolvedValue([
      {
        project: {
          id: 10,
          parentID: 0,
          name: "A",
          icon: "",
          color: "",
          description: "",
          path: "",
          isGitRepo: false,
          createtime: 0,
          updatetime: 0,
        },
        children: [],
      },
    ]);
    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    input.focus();
    fireEvent.keyDown(input, { key: "Tab" });

    expect(useNewChatContextStore.getState().projectContext?.projectID).toBe(
      10,
    );
  });

  it("已选项目上下文 ⌘N + 输入 'new project chat ce' → 只命中含 'CEO' 的 New project chat with 行", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        mkAgent({ id: 1, name: "CEO 助手" }),
        mkAgent({ id: 2, name: "工程师" }),
      ],
    });
    appMocks.ProjectListTree.mockResolvedValue([]);
    useNewChatContextStore
      .getState()
      .setContext({ projectID: 1, projectName: "Agentre" });
    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> new project chat ce");
    });
    await flush();

    expect(screen.getByText("CEO 助手")).toBeTruthy();
    expect(screen.queryByText("工程师")).toBeNull();
  });

  it("⌘N reopen → refetches project members instead of reusing a stale empty set", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        mkAgent({ id: 5, name: "Builder" }),
        mkAgent({ id: 6, name: "Reviewer" }),
      ],
    });
    appMocks.ProjectGet.mockResolvedValueOnce({
      project: { id: 1, name: "Agentre" },
      directMembers: [],
      inheritedMembers: [],
    }).mockResolvedValueOnce({
      project: { id: 1, name: "Agentre" },
      directMembers: [{ agentID: 5 }],
      inheritedMembers: [],
    });
    useNewChatContextStore
      .getState()
      .setContext({ projectID: 1, projectName: "Agentre" });

    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();
    expect(appMocks.ProjectGet).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Other Agents")).toBeTruthy();

    await act(async () => {
      useCommandPaletteStore.getState().close();
    });
    await flush();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    expect(appMocks.ProjectGet).toHaveBeenCalledTimes(2);
    expect(await screen.findByText("New chat in Agentre")).toBeTruthy();
  });
});

describe("CommandPalette — 命令模式分组顺序：新建对话先于操作 (BDD)", () => {
  it("Given /chat + ⌘N with both New chat and New agent visible, When the palette opens, Then the 'New Chat' group and its first agent command come before 'Actions / New agent'", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "CEO 助手" })],
    });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    // New Chat 分组（含首个 agent 命令）先于 Actions 分组 / New agent 项
    const newChatHeading = screen.getByText("New Chat");
    const actionsHeading = screen.getByText("Actions");
    expect(
      newChatHeading.compareDocumentPosition(actionsHeading) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    const firstAgent = screen.getByText("CEO 助手");
    expect(
      firstAgent.closest("[cmdk-item]")?.getAttribute("aria-selected"),
    ).toBe("true");
    const newAgentItem = screen.getByText("New agent");
    expect(
      firstAgent.compareDocumentPosition(newAgentItem) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("Given 已选项目上下文 + ⌘N with project new chat and New agent visible, When the palette opens, Then the 'New Project Chat' group/results come before 'Actions / New agent' while the project ContextBar stays visible", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "CEO 助手" })],
    });
    appMocks.ProjectListTree.mockResolvedValue([]);
    appMocks.ProjectGet.mockResolvedValue({
      project: { id: 1, name: "Agentre" },
      directMembers: [{ agentID: 1 }],
      inheritedMembers: [],
    });
    useNewChatContextStore
      .getState()
      .setContext({ projectID: 1, projectName: "Agentre" });
    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    // 项目新对话分组 / 结果先于 Actions 分组 / New agent 项
    const projectChatHeading = screen.getByText("New Project Chat");
    const actionsHeading = screen.getByText("Actions");
    expect(
      projectChatHeading.compareDocumentPosition(actionsHeading) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    const projectChatItem = screen.getByText("New project chat with");
    expect(
      projectChatItem.closest("[cmdk-item]")?.getAttribute("aria-selected"),
    ).toBe("true");
    const newAgentItem = screen.getByText("New agent");
    expect(
      projectChatItem.compareDocumentPosition(newAgentItem) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();

    // 既有上下文表现保留：ContextBar 仍可见
    expect(screen.getByLabelText("Switch project context")).toBeTruthy();
  });

  it("Given agents are still loading, When the pointer moves over a non-command control, Then the first agent command takes selection after loading", async () => {
    let resolveAgents!: (value: { agents: chat_svc.ChatAgentItem[] }) => void;
    const agentsPromise = new Promise<{ agents: chat_svc.ChatAgentItem[] }>(
      (resolve) => {
        resolveAgents = resolve;
      },
    );
    appMocks.ListChatAgents.mockReturnValue(agentsPromise);
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness("/chat");

    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    expect(
      screen
        .getByText("New agent")
        .closest("[cmdk-item]")
        ?.getAttribute("aria-selected"),
    ).toBe("true");
    fireEvent.pointerMove(input);

    await act(async () => {
      resolveAgents({ agents: [mkAgent({ id: 1, name: "Builder" })] });
      await agentsPromise;
    });

    await vi.waitFor(() => {
      expect(
        screen
          .getByText("Builder")
          .closest("[cmdk-item]")
          ?.getAttribute("aria-selected"),
      ).toBe("true");
    });
  });

  it("Given a command item is selected, When Backspace exits command mode, Then the first default-mode result is selected", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "Builder" })],
    });
    appMocks.ProjectListTree.mockResolvedValue([]);
    renderHarness("/chat");

    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();
    expect(
      screen
        .getByText("Builder")
        .closest("[cmdk-item]")
        ?.getAttribute("aria-selected"),
    ).toBe("true");

    fireEvent.keyDown(screen.getByRole("combobox"), { key: "Backspace" });
    await flush();

    expect(
      screen
        .getByText("Chat")
        .closest("[cmdk-item]")
        ?.getAttribute("aria-selected"),
    ).toBe("true");
  });

  it("Given keyboard selection was moved to an agent in project A, When Tab switches to project B where that agent is disabled, Then selection returns to the first enabled project command", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        mkAgent({ id: 5, name: "Builder" }),
        mkAgent({ id: 6, name: "Reviewer" }),
      ],
    });
    appMocks.ProjectListTree.mockResolvedValue([
      {
        project: {
          id: 1,
          parentID: 0,
          name: "Project A",
          icon: "",
          color: "",
          description: "",
          path: "",
          isGitRepo: false,
          createtime: 0,
          updatetime: 0,
        },
        children: [],
      },
      {
        project: {
          id: 2,
          parentID: 0,
          name: "Project B",
          icon: "",
          color: "",
          description: "",
          path: "",
          isGitRepo: false,
          createtime: 0,
          updatetime: 0,
        },
        children: [],
      },
    ]);
    appMocks.ProjectGet.mockImplementation(async (projectID: number) => ({
      project: null,
      directMembers:
        projectID === 1 ? [{ agentID: 5 }, { agentID: 6 }] : [{ agentID: 5 }],
      inheritedMembers: [],
    }));
    useNewChatContextStore
      .getState()
      .setContext({ projectID: 1, projectName: "Project A" });

    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    expect(await screen.findByText("New chat in Project A")).toBeTruthy();

    const input = screen.getByRole("combobox") as HTMLInputElement;
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(
      screen
        .getByText("Reviewer")
        .closest("[cmdk-item]")
        ?.getAttribute("aria-selected"),
    ).toBe("true");

    fireEvent.keyDown(input, { key: "Tab" });
    expect(await screen.findByText("New chat in Project B")).toBeTruthy();

    await vi.waitFor(() => {
      const reviewerItem = screen.getByText("Reviewer").closest("[cmdk-item]");
      expect(reviewerItem?.getAttribute("aria-disabled")).toBe("true");
      expect(reviewerItem?.getAttribute("aria-selected")).toBe("false");
      expect(
        screen
          .getByText("Builder")
          .closest("[cmdk-item]")
          ?.getAttribute("aria-selected"),
      ).toBe("true");
    });
  });
});

describe("CommandPalette — 非成员（其它 Agent）行不可选/不可点（disabled）", () => {
  it("非成员行 aria-disabled=true + cursor-not-allowed；成员行可选", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        mkAgent({ id: 5, name: "Builder" }), // member
        mkAgent({ id: 6, name: "Outsider" }), // non-member（其它 Agent）
      ],
    });
    appMocks.ProjectGet.mockResolvedValue({
      project: { id: 1, name: "Agentre" },
      directMembers: [{ agentID: 5 }],
      inheritedMembers: [],
    });
    useNewChatContextStore
      .getState()
      .setContext({ projectID: 1, projectName: "Agentre" });

    renderHarness();
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    // 非成员行：cmdk-item 被标记 disabled（不可键盘导航 / 不可点）
    const outsiderItem = screen.getByText("Outsider").closest("[cmdk-item]");
    expect(outsiderItem?.getAttribute("aria-disabled")).toBe("true");
    expect(outsiderItem?.className).toContain("cursor-not-allowed");
    expect(outsiderItem?.className).not.toContain("cursor-pointer");

    // 成员行：未被禁用
    const builderItem = screen.getByText("Builder").closest("[cmdk-item]");
    expect(builderItem?.getAttribute("aria-disabled")).toBe("false");
    expect(builderItem?.className).toContain("cursor-pointer");
  });
});

describe("CommandPalette — ⌘P does NOT enter command mode after a prior ⌘N (regression for stale initialQuery)", () => {
  it("Given ⌘N → close (setOpen false) → ⌘P (toggle), Then palette is in default mode", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "CEO 助手" })],
    });
    renderHarness();

    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();
    expect(screen.queryByLabelText("Command mode")).toBeTruthy();

    await act(async () => {
      useCommandPaletteStore.getState().setOpen(false);
    });
    await flush();

    await act(async () => {
      useCommandPaletteStore.getState().toggle();
    });
    await flush();

    expect(screen.queryByLabelText("Command mode")).toBeNull();
    expect(screen.queryByText("New chat with")).toBeNull();
  });

  it("Given ⌘N → close → ⌘N again, Then second open still has the seed (single-fire consumption is per-open)", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [mkAgent({ id: 1, name: "CEO 助手" })],
    });
    renderHarness();

    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();
    expect(screen.queryByLabelText("Command mode")).toBeTruthy();

    await act(async () => {
      useCommandPaletteStore.getState().close();
    });
    await flush();

    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();
    expect(screen.queryByLabelText("Command mode")).toBeTruthy();
  });
});

describe("CommandPalette — 非可对话 agent：需要先配置 分组 + 选中开引导弹窗", () => {
  it("Given a non-chattable agent on /chat, When selecting it, Then the guidance dialog opens, the palette closes and no session is created", async () => {
    appMocks.ListChatAgents.mockResolvedValue({
      agents: [
        mkAgent({ id: 1, name: "CEO 助手" }),
        mkAgent({
          id: 3,
          name: "未绑后端",
          chattable: false,
          blockReason: "no-backend",
          chattableHint: "请在组织页绑定后端",
        }),
      ],
    });

    renderHarness("/chat");
    await act(async () => {
      useCommandPaletteStore.getState().openWith("> ");
    });
    await flush();

    // 不可对话行：在「Needs setup」组内 + 徽标
    expect(screen.getByText("Needs setup")).toBeTruthy();
    expect(screen.getAllByText("Not configured").length).toBeGreaterThanOrEqual(
      1,
    );

    // 选中 → 不建会话、面板关闭、引导弹窗打开
    fireEvent.click(screen.getByText("未绑后端"));
    await flush();

    expect(useCommandPaletteStore.getState().open).toBe(false);
    const dialog = screen.getByRole("dialog");
    expect(dialog).toBeTruthy();
    // no-backend 弹窗标题（task 2 文案）
    expect(screen.getByText("未绑后端 cannot chat yet")).toBeTruthy();
  });
});

describe("CommandPalette — navigation source (BDD)", () => {
  function LocationProbe() {
    const location = useLocation();
    return <div data-testid="location-probe">{location.pathname}</div>;
  }

  it("Given palette open in default mode, When selecting a nav item, Then it navigates and the palette closes — and Projects is gone (decision 1)", async () => {
    appMocks.ListChatAgents.mockResolvedValue({ agents: [] });
    render(
      <MemoryRouter initialEntries={["/chat"]}>
        <LocationProbe />
        <CommandPalette />
      </MemoryRouter>,
    );
    await act(async () => {
      useCommandPaletteStore.getState().toggle(); // 默认模式
    });
    await flush();

    // nav 分组渲染（heading 与 Footer 的 "Navigate" 提示共用同一文案 → getAllByText）
    expect(screen.getAllByText("Navigate").length).toBeGreaterThanOrEqual(1);
    // 5 个导航目的地都在
    for (const label of [
      "Chat",
      "Issues",
      "Organization",
      "Hooks",
      "Settings",
    ]) {
      expect(screen.getByText(label)).toBeTruthy();
    }
    // 「项目」不再是一个导航目的地 —— 它退化成会话索引的一个分组维度（决策 1）。
    // 留在面板里等于又造出一个「换个说法的同一个地方」。
    expect(screen.queryByText("Projects")).toBeNull();

    fireEvent.click(screen.getByText("Issues"));
    await flush();

    expect(useCommandPaletteStore.getState().open).toBe(false);
    expect(screen.getByTestId("location-probe").textContent).toBe("/issues");
  });
});
