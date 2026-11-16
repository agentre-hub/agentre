import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MessageSquare } from "lucide-react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

// ChatComposer 现在通过 useFileDropZone → file-drop → OnFileDrop 间接依赖 wailsjs runtime。
// happy-dom 下 window.runtime 不存在,故把 OnFileDrop/OnFileDropOff 桩成 no-op,其余保持真实。
vi.mock("../../../../wailsjs/runtime/runtime", async () => {
  const actual = await vi.importActual<
    typeof import("../../../../wailsjs/runtime/runtime")
  >("../../../../wailsjs/runtime/runtime");
  return {
    ...actual,
    OnFileDrop: vi.fn(),
    OnFileDropOff: vi.fn(),
  };
});

import {
  AgentAvatar,
  AgentGroup,
  AppStatusBar,
  AppTopBar,
  ApprovalGate,
  ChatComposer,
  SessionRow,
  ShortcutsProvider,
  SidebarButton,
  StatusPill,
} from "@/components/agentre";
import { StatusDot } from "@agentre-hub/agentre-ui";
import i18n from "@/i18n";

function mockWailsRuntime() {
  const windowToggleMaximise = vi.fn();

  Object.defineProperty(window, "runtime", {
    configurable: true,
    value: {
      WindowToggleMaximise: windowToggleMaximise,
    },
  });

  return { windowToggleMaximise };
}

afterEach(() => {
  Reflect.deleteProperty(window, "runtime");
});

describe("Agentre foundation components", () => {
  it("renders agent identity, status, and active rail navigation accessibly", () => {
    render(
      <div>
        <AgentAvatar name="CEO 助手" initials="C" color="agent-1" />
        <StatusPill status="running" label="Running now" />
        <SidebarButton label="对话" icon={MessageSquare} active />
      </div>,
    );

    expect(screen.getByLabelText("CEO 助手")).toHaveTextContent("C");
    expect(screen.getByText("Running now")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "对话" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    const tooltip = screen.getByRole("tooltip", { name: "对话" });

    expect(tooltip).toHaveClass("left-full", "group-hover:opacity-100");
    expect(tooltip.querySelector('[data-slot="tooltip-arrow"]')).toHaveClass(
      "-left-1",
      "rotate-45",
    );
  });

  it("hides active/recent counts in the agent list while preserving the running breathing light", () => {
    const { rerender } = render(
      <AgentGroup
        name="工程师"
        activeCount={2}
        sessions={[
          {
            id: "s1",
            status: "running",
            title: "修复构建",
            trailingMark: "running",
          },
        ]}
      />,
    );

    expect(screen.queryByText("2")).not.toBeInTheDocument();
    expect(screen.queryByText("running")).not.toBeInTheDocument();
    expect(
      document.querySelector('[data-slot="status-mark"][data-state="running"]'),
    ).toBeInTheDocument();
    expect(screen.queryByText(/active/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/recent/i)).not.toBeInTheDocument();
    expect(screen.getAllByLabelText("Running")[0]).toHaveClass(
      "motion-safe:animate-pulse",
    );

    rerender(<AgentGroup name="设计师" activeCount={0} />);

    expect(screen.queryByText(/active/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/recent/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Running")).not.toBeInTheDocument();
  });

  it("shows an empty placeholder when expanded with no sessions", async () => {
    const user = userEvent.setup();
    render(<AgentGroup name="工程师" sessions={[]} />);

    await user.click(screen.getByRole("button", { name: "Expand 工程师" }));

    expect(screen.getByText("No sessions")).toBeInTheDocument();
  });

  it("persists user-toggled expanded state to localStorage", async () => {
    const user = userEvent.setup();
    const { unmount } = render(
      <AgentGroup
        name="工程师"
        persistenceKey="agent:42"
        sessions={[
          { id: "s1", status: "idle", title: "草稿", trailingLabel: "2m" },
        ]}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Expand 工程师" }));

    expect(localStorage.getItem("agentre.agentExpanded.agent:42")).toBe("1");

    unmount();

    // Remount: should restore expanded state from localStorage
    render(
      <AgentGroup
        name="工程师"
        persistenceKey="agent:42"
        sessions={[
          { id: "s1", status: "idle", title: "草稿", trailingLabel: "2m" },
        ]}
      />,
    );

    expect(
      screen.getByRole("button", { name: "Collapse 工程师" }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Collapse 工程师" }));

    expect(localStorage.getItem("agentre.agentExpanded.agent:42")).toBe("0");
  });

  it("treats expandedProp as a mount-time default only — later changes don't force state", async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <AgentGroup
        name="工程师"
        expanded={true}
        sessions={[
          { id: "s1", status: "idle", title: "草稿", trailingLabel: "2m" },
        ]}
      />,
    );

    // expanded=true at mount → starts expanded.
    expect(
      screen.getByRole("button", { name: "Collapse 工程师" }),
    ).toBeInTheDocument();

    // User explicitly collapses it.
    await user.click(screen.getByRole("button", { name: "Collapse 工程师" }));
    expect(
      screen.getByRole("button", { name: "Expand 工程师" }),
    ).toBeInTheDocument();

    // Parent re-asserting expanded=true must NOT force re-expand — selecting
    // an agent should preserve the user's expand/collapse choice (UX rule:
    // "保持展开/收起状态不变" when clicking an agent header).
    rerender(
      <AgentGroup
        name="工程师"
        expanded={true}
        sessions={[
          { id: "s1", status: "idle", title: "草稿", trailingLabel: "2m" },
        ]}
      />,
    );

    expect(
      screen.getByRole("button", { name: "Expand 工程师" }),
    ).toBeInTheDocument();
  });

  it("animates the agent session list by expanding and collapsing height", async () => {
    const user = userEvent.setup();
    const { container } = render(
      <AgentGroup
        name="工程师"
        sessions={[
          {
            id: "s1",
            status: "idle",
            title: "规划新功能",
            trailingLabel: "2m",
          },
        ]}
      />,
    );
    const content = container.querySelector(
      '[data-slot="agent-group-content"]',
    );

    expect(content).toHaveAttribute("aria-hidden", "true");
    expect(content).toHaveClass("transition-[grid-template-rows]");
    expect(content).not.toHaveClass(
      "motion-safe:fade-in-0",
      "motion-safe:animate-in",
    );
    expect(content).toHaveStyle("grid-template-rows: 0fr");

    await user.click(screen.getByRole("button", { name: "Expand 工程师" }));

    expect(screen.getByText("规划新功能")).toBeInTheDocument();
    expect(content).toHaveAttribute("aria-hidden", "false");
    expect(content).toHaveStyle("grid-template-rows: 1fr");

    await user.click(screen.getByRole("button", { name: "Collapse 工程师" }));

    expect(content).toHaveAttribute("aria-hidden", "true");
    expect(content).toHaveStyle("grid-template-rows: 0fr");
  });

  it("uses a pointer cursor for actionable controls", () => {
    render(
      <div>
        <SidebarButton label="对话" icon={MessageSquare} active />
        <SessionRow
          status="running"
          title="处理周一例会纪要"
          trailingLabel="3m"
        />
        <AppTopBar appName="Agentre" breadcrumb="CEO 助手" platform="windows" />
      </div>,
    );

    expect(screen.getByRole("button", { name: "对话" })).toHaveClass(
      "cursor-pointer",
    );
    expect(
      screen.getByRole("button", { name: /处理周一例会纪要/ }),
    ).toHaveClass("cursor-pointer");
    expect(
      screen.getByRole("button", { name: /Open command palette/ }),
    ).toHaveClass("cursor-text");
    expect(screen.getByRole("button", { name: "Minimize window" })).toHaveClass(
      "cursor-pointer",
    );
  });

  it("given notifications and user profiles are not available, when the top bar renders, then it omits those buttons", () => {
    render(
      <AppTopBar appName="Agentre" breadcrumb="CEO 助手" platform="windows" />,
    );

    expect(
      screen.queryByRole("button", { name: /通知/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "用户菜单" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("你")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Open command palette/ }),
    ).toBeInTheDocument();
  });

  it("renders the shared chrome from the Pencil design", () => {
    const onAttentionClick = vi.fn();
    // 包一层 ShortcutsProvider 固定平台：命令面板 kbd 的兜底值现在按平台生成，
    // 不固定的话会跟着 happy-dom 的 UA 变（本机 Linux / CI macOS 不同）。
    const { container } = render(
      <MemoryRouter>
        <ShortcutsProvider platform="darwin">
          <div>
            <AppTopBar
              appName="Agentre"
              breadcrumb="CEO 助手"
              platform="windows"
            />
            <AppStatusBar
              agentCount={7}
              runningCount={12}
              approvals={[
                {
                  id: 101,
                  title: "A",
                  agentName: "A",
                  avatarColor: "agent-1",
                },
              ]}
              unread={[
                { id: 202, title: "B", agentName: "B", avatarColor: "agent-2" },
                { id: 203, title: "C", agentName: "C", avatarColor: "agent-3" },
              ]}
              status="waiting"
              version="0.1.0"
              onAttentionClick={onAttentionClick}
            />
          </div>
        </ShortcutsProvider>
      </MemoryRouter>,
    );

    expect(screen.getByText("Agentre")).toBeInTheDocument();
    expect(screen.getByText("CEO 助手")).toBeInTheDocument();
    // 「/」骑在 --rail 上，且分隔应用名与面包屑（承载信息），必须用正文色：
    // --decorative-foreground 在 rail 上只有 2.70，连 §1.4.11 的 3:1 都不到。
    expect(screen.getByText("/")).toHaveClass("text-muted-foreground");
    expect(screen.getByText("⌘P")).toBeInTheDocument();
    expect(screen.getByRole("banner")).toHaveClass("wails-drag");
    expect(
      screen.getByRole("button", { name: /Open command palette/ }),
    ).toHaveClass("wails-no-drag");
    expect(
      container.querySelector('[data-slot="native-window-controls-inset"]'),
    ).not.toBeInTheDocument();
    expect(
      container.querySelector('[data-slot="windows-window-controls"]'),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Minimize window" })).toHaveClass(
      "wails-no-drag",
    );
    expect(
      screen.getByRole("button", { name: "Maximize window" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Close window" }),
    ).toBeInTheDocument();
    // 标题栏上这四颗都去掉了浏览器默认的 outline：键盘焦点只剩一层 rail-accent
    // 底色（对 rail 1.19 / 1.27），命令面板入口更是什么都没有。按钮类一律画
    // 共享的那一圈实色环。
    for (const name of [
      /Open command palette/,
      "Minimize window",
      "Maximize window",
      "Close window",
    ]) {
      expect(screen.getByRole("button", { name })).toHaveClass(
        "focus-visible:focus-outline",
      );
    }
    expect(
      screen.queryByRole("button", { name: /通知/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "用户菜单" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("12 sessions running")).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: /1 approval · 2 unread — click to choose a session/,
      }),
    ).toBeInTheDocument();
    expect(screen.queryByText("main · ~/Code/agentre")).not.toBeInTheDocument();
    expect(screen.queryByText("synced 2s ago")).not.toBeInTheDocument();
    expect(screen.getByText("0.1.0")).toBeInTheDocument();
  });

  it("puts the title bar and status bar on the chrome surface with no structural lines", () => {
    render(
      <MemoryRouter>
        <ShortcutsProvider platform="darwin">
          <AppTopBar appName="Agentre" breadcrumb="CEO" platform="windows" />
          <AppStatusBar
            agentCount={1}
            runningCount={0}
            approvals={[]}
            unread={[]}
            status="idle"
            version="0.1.0"
            onAttentionClick={vi.fn()}
          />
        </ShortcutsProvider>
      </MemoryRouter>,
    );

    for (const band of [
      screen.getByRole("banner"),
      screen.getByRole("contentinfo"),
    ]) {
      expect(band).toHaveClass("bg-chrome");
      expect(band).not.toHaveClass("bg-rail");
      expect(band.className).not.toMatch(/(^|\s)border(-[tb])?(\s|$)/);
    }
  });

  it("marks the active rail item with the raised surface and no accent fill", () => {
    render(<SidebarButton label="对话" icon={MessageSquare} active />);

    const button = screen.getByRole("button", { name: "对话" });
    expect(button).toHaveClass("bg-raised");
    expect(button).not.toHaveClass("bg-primary-soft");
    expect(screen.getByRole("tooltip").className).not.toMatch(
      /(^|\s)border(\s|$)/,
    );
  });

  const item = (id: number, title: string, agentName = "Reviewer") => ({
    id,
    title,
    agentName,
    avatarColor: "agent-1",
    avatarIcon: "",
    avatarDataUrl: "",
  });

  it("counts running sessions in the summary and moves the agent count to the tooltip", () => {
    render(
      <AppStatusBar
        agentCount={7}
        runningCount={5}
        approvals={[]}
        unread={[]}
        status="running"
        version="0.1.0"
        onAttentionClick={() => {}}
      />,
    );

    const summary = screen.getByText("5 sessions running");
    expect(summary).toBeInTheDocument();
    expect(summary.closest("[title]")).toHaveAttribute("title", "7 agents");
    expect(screen.queryByText(/agent ·/)).not.toBeInTheDocument();
  });

  it("jumps straight to the only pending session without a chevron", async () => {
    const user = userEvent.setup();
    const onAttentionClick = vi.fn();

    const { container } = render(
      <AppStatusBar
        agentCount={1}
        runningCount={0}
        approvals={[item(101, "Fix login")]}
        unread={[]}
        status="waiting"
        version="0.1.0"
        onAttentionClick={onAttentionClick}
      />,
    );

    const button = screen.getByRole("button", { name: /1 approval/ });
    expect(button).not.toHaveAttribute("aria-haspopup");
    expect(container.querySelector("footer .lucide-chevron-up")).toBeNull();
    await user.click(button);
    expect(onAttentionClick).toHaveBeenCalledWith(101);
  });

  it("opens an upward popover grouped approvals then unread when several are pending", async () => {
    const user = userEvent.setup();
    const onAttentionClick = vi.fn();

    render(
      <AppStatusBar
        agentCount={3}
        runningCount={0}
        approvals={[item(101, "Fix login")]}
        unread={[item(202, "Write docs", "Writer"), item(303, "Refactor")]}
        status="waiting"
        version="0.1.0"
        onAttentionClick={onAttentionClick}
      />,
    );

    await user.click(screen.getByRole("button", { name: /1 approval/ }));
    expect(onAttentionClick).not.toHaveBeenCalled();

    const headings = screen.getAllByRole("heading").map((h) => h.textContent);
    expect(headings).toEqual(["Pending approval", "Unread"]);
    const rows = screen.getAllByRole("button", {
      name: /Fix login|Write docs|Refactor/,
    });
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("Fix login"),
      expect.stringContaining("Write docs"),
      expect.stringContaining("Refactor"),
    ]);

    await user.click(rows[1]);
    expect(onAttentionClick).toHaveBeenCalledWith(202);
  });

  it("tints the dot and text amber only for approvals; unread alone stays muted", () => {
    const { container, rerender } = render(
      <AppStatusBar
        agentCount={1}
        runningCount={0}
        approvals={[]}
        unread={[item(202, "Write docs")]}
        status="idle"
        version="0.1.0"
        onAttentionClick={() => {}}
      />,
    );

    const unreadText = screen.getByText("1 unread");
    expect(unreadText).toHaveClass("text-muted-foreground");
    expect(unreadText.closest("button")?.className).not.toMatch(
      /status-waiting/,
    );
    expect(
      container.querySelector('footer [aria-label="Idle"]'),
    ).toBeInTheDocument();

    rerender(
      <AppStatusBar
        agentCount={1}
        runningCount={0}
        approvals={[item(101, "Fix login")]}
        unread={[]}
        status="waiting"
        version="0.1.0"
        onAttentionClick={() => {}}
      />,
    );
    expect(screen.getByText("1 approval")).toHaveClass(
      "text-status-waiting-text",
    );
  });

  it("renders no pending entry when nothing needs attention", () => {
    render(
      <AppStatusBar
        agentCount={1}
        runningCount={0}
        approvals={[]}
        unread={[]}
        status="idle"
        version="0.1.0"
        onAttentionClick={() => {}}
      />,
    );
    expect(
      screen.queryByRole("button", { name: /approval|unread/ }),
    ).toBeNull();
  });

  it("tones the rail badge: waiting for approvals, primary by default", () => {
    const { rerender } = render(
      <SidebarButton label="Chat" icon={MessageSquare} badge />,
    );
    expect(document.querySelector('[data-slot="sidebar-badge"]')).toHaveClass(
      "bg-primary",
    );
    rerender(
      <SidebarButton
        label="Chat, 2 approvals pending"
        icon={MessageSquare}
        badge
        badgeTone="waiting"
      />,
    );
    const badge = document.querySelector('[data-slot="sidebar-badge"]');
    expect(badge).toHaveClass("bg-status-waiting");
    expect(badge).not.toHaveClass("bg-primary");
    expect(
      screen.getByRole("button", { name: "Chat, 2 approvals pending" }),
    ).toBeInTheDocument();
  });

  it("reserves native traffic-light space only on macOS", () => {
    const { container } = render(
      <AppTopBar appName="Agentre" breadcrumb="CEO 助手" platform="darwin" />,
    );

    expect(
      container.querySelector('[data-slot="native-window-controls-inset"]'),
    ).toBeInTheDocument();
    expect(
      container.querySelector('[data-slot="windows-window-controls"]'),
    ).not.toBeInTheDocument();
  });

  it("toggles window zoom from the draggable top bar without hijacking top-bar controls", async () => {
    const user = userEvent.setup();
    const { windowToggleMaximise } = mockWailsRuntime();

    render(
      <AppTopBar appName="Agentre" breadcrumb="CEO 助手" platform="darwin" />,
    );

    await user.dblClick(
      screen.getByRole("button", { name: /Open command palette/ }),
    );

    expect(windowToggleMaximise).not.toHaveBeenCalled();

    await user.dblClick(screen.getByRole("banner"));

    expect(windowToggleMaximise).toHaveBeenCalledTimes(1);
  });

  it("submits trimmed composer text and guards empty messages", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();

    render(<ChatComposer onSubmit={onSubmit} />);

    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();

    const editor = screen.getByRole("textbox");
    await user.click(editor);
    await user.paste("  规划 Q3 路线图  ");
    await waitFor(() => {
      expect(editor).toHaveTextContent("规划 Q3 路线图");
    });
    const sendButton = screen.getByRole("button", { name: "Send" });
    await waitFor(() => {
      expect(sendButton).toBeEnabled();
    });
    await user.click(sendButton);

    expect(onSubmit).toHaveBeenCalledWith({ text: "规划 Q3 路线图" });
    // 发送后编辑器被 clearContent，TipTap 会留一个空段落。
    expect(editor.textContent ?? "").toBe("");
  });

  it("keeps focus on the editor after clicking the send button", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();

    render(<ChatComposer onSubmit={onSubmit} />);

    const editor = screen.getByRole("textbox");
    await user.click(editor);
    await user.paste("继续输入");
    await waitFor(() => {
      expect(editor).toHaveTextContent("继续输入");
    });
    const sendButton = screen.getByRole("button", { name: "Send" });
    await waitFor(() => {
      expect(sendButton).toBeEnabled();
    });
    await user.click(sendButton);

    // 点击发送按钮后，浏览器默认会把焦点交给按钮；ChatComposer 必须主动把焦点抓回
    // 编辑器，否则用户接着输入下一条消息时还得手动 click 一次。
    expect(editor).toHaveFocus();
  });

  it("autoFocusOnMount=true focuses the editor on first mount", async () => {
    render(<ChatComposer autoFocusOnMount onSubmit={() => undefined} />);
    // 新建会话进入时，ChatPanel 通过 newSessionAgent 传 true，让用户直接打字。
    // TipTap 的 autofocus 是 setTimeout(0) 异步执行的，所以 waitFor。
    await waitFor(() => {
      expect(screen.getByRole("textbox")).toHaveFocus();
    });
  });

  it("Given a mounted composer, When new-session autofocus becomes enabled, Then the editor receives focus", async () => {
    const { rerender } = render(<ChatComposer onSubmit={() => undefined} />);

    expect(screen.getByRole("textbox")).not.toHaveFocus();

    rerender(<ChatComposer autoFocusOnMount onSubmit={() => undefined} />);

    await waitFor(() => {
      expect(screen.getByRole("textbox")).toHaveFocus();
    });
  });

  it("autoFocusOnMount default (false) leaves focus untouched on mount", () => {
    render(<ChatComposer onSubmit={() => undefined} />);
    // 续聊已有会话时不抢焦点，避免打断用户在侧栏的其它操作。
    expect(screen.getByRole("textbox")).not.toHaveFocus();
  });

  it("disables browser text assistance in the composer edit box", () => {
    render(<ChatComposer onSubmit={() => undefined} />);

    const composer = screen.getByRole("textbox");

    expect(composer).toHaveAttribute("autocomplete", "off");
    expect(composer).toHaveAttribute("autocorrect", "off");
    expect(composer).toHaveAttribute("autocapitalize", "off");
    expect(composer).toHaveAttribute("spellcheck", "false");
  });

  it("exposes the configured placeholder via data-placeholder for CSS rendering", async () => {
    render(<ChatComposer onSubmit={() => undefined} />);

    const editor = screen.getByRole("textbox");
    const emptyParagraph = editor.querySelector("p.is-editor-empty");

    // 占位按**本次接上的能力**拼,不再按 backendType 查表许诺 `/ 与 !`
    // (见包内 chat-input/placeholder.ts)。裸 composer 没接 / 也没接 !,所以那两段
    // 不出现;`@` 出现是因为桌面端至少能提及本机这一台设备。
    await waitFor(() =>
      expect(emptyParagraph).toHaveAttribute(
        "data-placeholder",
        "Type a message · @ to mention",
      ),
    );
  });

  it("renders approval copy with explicit approve and reject actions", () => {
    render(
      <ApprovalGate
        title="需要你的确认"
        description="Agent 即将发送消息到 #general（约 23 人会看到）"
      />,
    );

    expect(screen.getByText("需要你的确认")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reject" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeInTheDocument();
  });
});

describe("StatusDot 读屏名", () => {
  it.each([
    ["running", "Running", "运行中"],
    ["waiting", "Needs you", "等你"],
    ["idle", "Idle", "空闲"],
    ["error", "Error", "出错"],
  ] as const)(
    "Given status %s, When the UI language changes, Then the dot is named in that language",
    async (status, en, zh) => {
      const { rerender } = render(<StatusDot status={status} />);
      expect(screen.getByLabelText(en)).toBeInTheDocument();

      await i18n.changeLanguage("zh-CN");
      rerender(<StatusDot status={status} />);

      expect(screen.getByLabelText(zh)).toBeInTheDocument();
      expect(screen.queryByLabelText(/status$/)).toBeNull();
    },
  );
});

describe("SidebarButton 角标", () => {
  it("Given 有待处理的东西, When 渲染, Then 图标上出现角标且不进无障碍名", () => {
    const { container } = render(
      <SidebarButton label="设置" icon={MessageSquare} badge />,
    );

    expect(
      container.querySelector("[data-slot='sidebar-badge']"),
    ).not.toBeNull();
    // 角标是纯装饰：信息由按钮自己的 aria-label 承载，别让读屏念一个圆点。
    expect(screen.getByRole("button", { name: "设置" })).toBeInTheDocument();
  });

  it("Given 没有待处理的东西, When 渲染, Then 没有角标", () => {
    const { container } = render(
      <SidebarButton label="设置" icon={MessageSquare} />,
    );

    expect(container.querySelector("[data-slot='sidebar-badge']")).toBeNull();
  });
});

describe("SidebarButton 快捷键提示", () => {
  it("Given 传入快捷键, When 渲染, Then tooltip 同时显示名称与快捷键", () => {
    render(<SidebarButton label="看板" icon={MessageSquare} shortcut="⌘B" />);

    const tooltip = screen.getByRole("tooltip");
    expect(within(tooltip).getByText("看板")).toBeInTheDocument();
    expect(within(tooltip).getByText("⌘B")).toBeInTheDocument();
  });

  it("Given 没有快捷键, When 渲染, Then tooltip 只有名称", () => {
    render(<SidebarButton label="看板" icon={MessageSquare} />);

    expect(screen.getByRole("tooltip")).toHaveTextContent(/^看板$/);
  });
});
