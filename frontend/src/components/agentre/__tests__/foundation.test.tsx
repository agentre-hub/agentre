import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MessageSquare } from "lucide-react";
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
  SidebarButton,
  StatusPill,
} from "@/components/agentre";

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
        <StatusPill status="running" />
        <SidebarButton label="对话" icon={MessageSquare} active />
      </div>,
    );

    expect(screen.getByLabelText("CEO 助手")).toHaveTextContent("C");
    expect(screen.getByText("RUNNING")).toBeInTheDocument();
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
            trailingLabel: "running",
          },
        ]}
      />,
    );

    expect(screen.queryByText("2")).not.toBeInTheDocument();
    expect(screen.queryByText(/active/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/recent/i)).not.toBeInTheDocument();
    expect(screen.getByLabelText("running status")).toHaveClass(
      "motion-safe:animate-pulse",
    );

    rerender(<AgentGroup name="设计师" activeCount={0} />);

    expect(screen.queryByText(/active/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/recent/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText("running status")).not.toBeInTheDocument();
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
    const { container } = render(
      <div>
        <AppTopBar appName="Agentre" breadcrumb="CEO 助手" platform="windows" />
        <AppStatusBar
          agentCount={7}
          runningCount={12}
          approvalCount={1}
          unreadCount={2}
          attentionIds={[101, 202]}
          status="waiting"
          version="0.1.0"
          onAttentionClick={onAttentionClick}
        />
      </div>,
    );

    expect(screen.getByText("Agentre")).toBeInTheDocument();
    expect(screen.getByText("CEO 助手")).toBeInTheDocument();
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
    expect(
      screen.queryByRole("button", { name: /通知/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "用户菜单" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("7 agents · 12 running")).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: /1 approval · 2 unread — click to open the first session needing attention/,
      }),
    ).toBeInTheDocument();
    expect(screen.queryByText("main · ~/Code/agentre")).not.toBeInTheDocument();
    expect(screen.queryByText("synced 2s ago")).not.toBeInTheDocument();
    expect(screen.getByText("0.1.0")).toBeInTheDocument();
  });

  it("opens the first attention session when the status bar segment is clicked", async () => {
    const user = userEvent.setup();
    const onAttentionClick = vi.fn();

    render(
      <AppStatusBar
        agentCount={7}
        runningCount={12}
        approvalCount={1}
        unreadCount={2}
        attentionIds={[101, 202]}
        status="waiting"
        version="0.1.0"
        onAttentionClick={onAttentionClick}
      />,
    );

    await user.click(
      screen.getByRole("button", {
        name: /click to open the first session needing attention/,
      }),
    );

    expect(onAttentionClick).toHaveBeenCalledWith(101);
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
    render(<ChatComposer autoFocusOnMount />);
    // 新建会话进入时，ChatPanel 通过 newSessionAgent 传 true，让用户直接打字。
    // TipTap 的 autofocus 是 setTimeout(0) 异步执行的，所以 waitFor。
    await waitFor(() => {
      expect(screen.getByRole("textbox")).toHaveFocus();
    });
  });

  it("Given a mounted composer, When new-session autofocus becomes enabled, Then the editor receives focus", async () => {
    const { rerender } = render(<ChatComposer />);

    expect(screen.getByRole("textbox")).not.toHaveFocus();

    rerender(<ChatComposer autoFocusOnMount />);

    await waitFor(() => {
      expect(screen.getByRole("textbox")).toHaveFocus();
    });
  });

  it("autoFocusOnMount default (false) leaves focus untouched on mount", () => {
    render(<ChatComposer />);
    // 续聊已有会话时不抢焦点，避免打断用户在侧栏的其它操作。
    expect(screen.getByRole("textbox")).not.toHaveFocus();
  });

  it("disables browser text assistance in the composer edit box", () => {
    render(<ChatComposer />);

    const composer = screen.getByRole("textbox");

    expect(composer).toHaveAttribute("autocomplete", "off");
    expect(composer).toHaveAttribute("autocorrect", "off");
    expect(composer).toHaveAttribute("autocapitalize", "off");
    expect(composer).toHaveAttribute("spellcheck", "false");
  });

  it("exposes the configured placeholder via data-placeholder for CSS rendering", () => {
    render(<ChatComposer />);

    const editor = screen.getByRole("textbox");
    const emptyParagraph = editor.querySelector("p.is-editor-empty");

    // 一样能力都没接的裸 composer 只剩基础提示 —— 占位按**本次接上的能力**拼,
    // 不再按 backendType 查表许诺 `@ / !`(见包内 chat-input/placeholder.ts)。
    expect(emptyParagraph).toHaveAttribute("data-placeholder", "Type a message");
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
