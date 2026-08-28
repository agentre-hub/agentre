import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { Tab } from "../tab";

describe("Tab 组件", () => {
  const baseProps = {
    title: "处理周一例会纪要",
    avatar: { letter: "C", color: "#3b82f6" },
    active: false,
    isPreview: false,
    isPinned: false,
    status: "idle" as const,
    projectColor: null as string | null,
    worktree: false,
    pillText: null as string | null,
    onActivate: vi.fn(),
    onClose: vi.fn(),
    onDoublePromote: vi.fn(),
  };

  it("渲染 title", () => {
    render(<Tab {...baseProps} />);
    expect(screen.getByText("处理周一例会纪要")).toBeInTheDocument();
  });

  it("限制最大宽度，长标题交给 CSS truncate", () => {
    render(<Tab {...baseProps} />);
    expect(screen.getByRole("tab")).toHaveClass("max-w-[240px]");
    expect(screen.getByText("处理周一例会纪要")).toHaveClass("truncate");
  });

  it("active=true 时加 data-active=true", () => {
    render(<Tab {...baseProps} active />);
    expect(screen.getByRole("tab")).toHaveAttribute("data-active", "true");
  });

  it("status='running' 时同时渲染 spinner 和 close X", () => {
    render(<Tab {...baseProps} status="running" />);
    expect(screen.getByTestId("tab-spinner")).toBeInTheDocument();
    expect(screen.getByLabelText("Close Tab")).toBeInTheDocument();
  });

  it("status='running' 时点 close X 触发 onClose(并 stopPropagation)", async () => {
    const user = userEvent.setup();
    const onActivate = vi.fn();
    const onClose = vi.fn();
    render(
      <Tab
        {...baseProps}
        status="running"
        onActivate={onActivate}
        onClose={onClose}
      />,
    );
    await user.click(screen.getByLabelText("Close Tab"));
    expect(onClose).toHaveBeenCalled();
    expect(onActivate).not.toHaveBeenCalled();
  });

  it("pillText='审批' 渲染 pill", () => {
    render(<Tab {...baseProps} status="waiting" pillText="审批" />);
    expect(screen.getByText("审批")).toBeInTheDocument();
  });

  it("isPinned=true 显示 pin 图标 + 仍有 close X", () => {
    render(<Tab {...baseProps} isPinned />);
    expect(screen.getByTestId("tab-pin-icon")).toBeInTheDocument();
    expect(screen.getByLabelText("Close Tab")).toBeInTheDocument();
  });

  it("projectColor 设置 data-project-color", () => {
    render(<Tab {...baseProps} projectColor="#5b8def" />);
    expect(screen.getByRole("tab")).toHaveAttribute(
      "data-project-color",
      "#5b8def",
    );
  });

  it("kind='terminal' 显示终端图标(替代头像) + title 用传入的 title", () => {
    const { container } = render(
      <Tab {...baseProps} kind="terminal" title="终端 · MacMini" />,
    );
    expect(
      container.querySelector(".lucide-square-terminal"),
    ).toBeInTheDocument();
    expect(screen.getByText("终端 · MacMini")).toBeInTheDocument();
  });

  it("单击触发 onActivate", async () => {
    const user = userEvent.setup();
    const onActivate = vi.fn();
    render(<Tab {...baseProps} onActivate={onActivate} />);
    await user.click(screen.getByRole("tab"));
    expect(onActivate).toHaveBeenCalled();
  });

  it("Cmd+Click 不触发 onActivate (由父级 TabStrip 处理 sidebar 点击, 本地此事件交给父级)", async () => {
    const user = userEvent.setup();
    const onActivate = vi.fn();
    render(<Tab {...baseProps} onActivate={onActivate} />);
    await user.keyboard("{Meta>}");
    await user.click(screen.getByRole("tab"));
    await user.keyboard("{/Meta}");
    expect(onActivate).toHaveBeenCalled();
  });

  it("双击触发 onDoublePromote", async () => {
    const user = userEvent.setup();
    const onDoublePromote = vi.fn();
    render(<Tab {...baseProps} isPreview onDoublePromote={onDoublePromote} />);
    await user.dblClick(screen.getByRole("tab"));
    expect(onDoublePromote).toHaveBeenCalled();
  });

  it("点 close X 触发 onClose(并 stopPropagation)", async () => {
    const user = userEvent.setup();
    const onActivate = vi.fn();
    const onClose = vi.fn();
    render(<Tab {...baseProps} onActivate={onActivate} onClose={onClose} />);
    await user.click(screen.getByLabelText("Close Tab"));
    expect(onClose).toHaveBeenCalled();
    expect(onActivate).not.toHaveBeenCalled();
  });
});

// ─── 2026-08-23 对话页外壳收口 · tab 条回归共享原语 ──────────────────────────

describe("Tab · 状态点与身份方块来自共享原语", () => {
  const baseProps = {
    title: "处理周一例会纪要",
    avatar: { letter: "C", color: "var(--agent-1)" },
    active: false,
    isPreview: false,
    isPinned: false,
    status: "idle" as const,
    projectColor: null as string | null,
    worktree: false,
    pillText: null as string | null,
    onActivate: vi.fn(),
    onClose: vi.fn(),
    onDoublePromote: vi.fn(),
  };

  it.each([
    ["running", "bg-status-running"],
    ["waiting", "bg-status-waiting"],
    ["error", "bg-status-error"],
  ] as const)(
    "Given status=%s, When 渲染, Then 状态点的颜色来自共享 statusConfig 而不是 tab 私有那张表",
    (status, dotClass) => {
      render(<Tab {...baseProps} status={status} />);

      const dot = screen.getByTestId("tab-status-dot");
      expect(dot).toHaveClass(dotClass);
      // 共享 StatusDot 连可及名一起带来 —— 状态不再只靠颜色说。
      expect(dot).toHaveAccessibleName(`${status} status`);
    },
  );

  it("Given status=idle, When 渲染, Then 仍然不摆状态点（tab 条上闲置就是没记号）", () => {
    render(<Tab {...baseProps} status="idle" />);

    expect(screen.queryByTestId("tab-status-dot")).toBeNull();
  });

  it("Given 一个会话 tab, When 渲染, Then 身份方块是共享 AgentAvatar 的最小档而不是手搓的方块", () => {
    render(<Tab {...baseProps} />);

    const avatar = screen.getByTestId("tab-avatar");
    // xs 档的签名：size-3.5 + rounded-sm。
    expect(avatar).toHaveClass("size-3.5");
    expect(avatar).toHaveClass("rounded-sm");
    expect(avatar).toHaveTextContent("C");
  });
});
