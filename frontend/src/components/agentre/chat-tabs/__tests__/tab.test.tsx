import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { presetForKey } from "@agentre-hub/agentre-ui";

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
    pillText: null as string | null,
    onActivate: vi.fn(),
    onClose: vi.fn(),
    onDoublePromote: vi.fn(),
  };

  it("渲染 title", () => {
    render(<Tab {...baseProps} />);
    expect(screen.getByText("处理周一例会纪要")).toBeInTheDocument();
  });

  it("填满外壳、自己不设最小 / 最大宽度（宽度由 Tab 条的外壳分配），长标题交给 CSS truncate", () => {
    render(<Tab {...baseProps} />);
    const tab = screen.getByRole("tab");
    expect(tab).toHaveClass("w-full");
    // 自己再设 min-width 会撑出外壳、压到右边那一格上（2026-11-17 截图里的叠压）。
    expect(tab.className).not.toMatch(/(^|\s)min-w-\[/);
    expect(tab.className).not.toMatch(/(^|\s)max-w-\[/);
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
    pillText: null as string | null,
    onActivate: vi.fn(),
    onClose: vi.fn(),
    onDoublePromote: vi.fn(),
  };

  it.each([
    ["waiting", "bg-status-waiting", "Needs you"],
    ["error", "bg-status-error", "Error"],
  ] as const)(
    "Given status=%s, When 渲染, Then 状态点的颜色来自共享 statusConfig 而不是 tab 私有那张表",
    (status, dotClass, name) => {
      render(<Tab {...baseProps} status={status} />);

      const dot = screen.getByTestId("tab-status-dot");
      expect(dot).toHaveClass(dotClass);
      // 共享 StatusDot 连可及名一起带来 —— 状态不再只靠颜色说；名字用界面语言（测试里是 en）。
      expect(dot).toHaveAccessibleName(name);
    },
  );

  it("Given status=running, When 渲染, Then 只有 spinner 一个记号，不再同时画状态点", () => {
    render(<Tab {...baseProps} status="running" />);

    expect(screen.getByTestId("tab-spinner")).toBeInTheDocument();
    expect(screen.queryByTestId("tab-status-dot")).toBeNull();
  });

  it("Given status=idle, When 渲染, Then 仍然不摆状态点（tab 条上闲置就是没记号）", () => {
    render(<Tab {...baseProps} status="idle" />);

    expect(screen.queryByTestId("tab-status-dot")).toBeNull();
  });

  it("Given 一个会话 tab, When 渲染, Then 身份方块是共享 AgentAvatar 的最小档而不是手搓的方块", () => {
    render(<Tab {...baseProps} />);

    const avatar = screen.getByTestId("tab-avatar");
    // xs 档的签名：size-3.5 + rounded-[3.5px]。
    expect(avatar).toHaveClass("size-3.5");
    expect(avatar).toHaveClass("rounded-[3.5px]");
    expect(avatar).toHaveTextContent("C");
  });

  describe("Given Agent 有完整身份, When tab 渲染, Then 身份方块按上传图片 ▸ 预设 ▸ 图标 ▸ 首字母显示", () => {
    const renderAvatar = (avatar: {
      letter: string;
      color: string;
      avatarIcon?: string;
      avatarDataUrl?: string;
    }) => {
      render(<Tab {...baseProps} avatar={avatar} />);
      return screen.getByTestId("tab-avatar");
    };

    it("上传图片：画的是图片", () => {
      const glyph = renderAvatar({
        letter: "C",
        color: "#3b82f6",
        avatarDataUrl: "data:image/png;base64,AAAA",
      });

      expect(glyph.querySelector("img")?.getAttribute("src")).toBe(
        "data:image/png;base64,AAAA",
      );
    });

    it("预设 key：画注册表里的预设图", () => {
      const glyph = renderAvatar({
        letter: "C",
        color: "#3b82f6",
        avatarIcon: "preset:agent-assistant",
      });

      expect(glyph.querySelector("img")?.getAttribute("src")).toBe(
        presetForKey("preset:agent-assistant")?.src,
      );
    });

    it("图标 key：画词表里的图标，而不是首字母", () => {
      const glyph = renderAvatar({
        letter: "C",
        color: "#3b82f6",
        avatarIcon: "hammer",
      });

      expect(glyph.querySelector("svg")).not.toBeNull();
      expect(glyph).not.toHaveTextContent("C");
    });

    it("什么都没有：照旧画首字母", () => {
      const glyph = renderAvatar({ letter: "C", color: "#3b82f6" });

      expect(glyph).toHaveTextContent("C");
    });
  });
});

// ─── 2026-11-17 Tab 条像 Chrome 一样一路压缩 ─────────────────────────────────
// 分档按外壳自身的宽度（容器查询，外壳是 @container）：
//   ≥ 88px  方块 + 标题 + 尾部一格（running 是 spinner，否则 ×）
//   < 88px  尾部一格收起（悬停也不出 ×）、running 的 spinner 顶替方块、待批胶囊收起
//   < 56px  标题也收起，只剩居中的方块；状态点贴到方块右上角
// 活动 tab 例外：始终留 ×；窄于 96px 时收起标题。
// happy-dom 不算容器查询，这里守的是类名契约；真机效果见运行期验证。

describe("Tab · 尾部一格与按宽度分档", () => {
  const baseProps = {
    title: "处理周一例会纪要",
    avatar: { letter: "C", color: "#3b82f6" },
    active: false,
    isPreview: false,
    isPinned: false,
    status: "idle" as const,
    projectColor: null as string | null,
    pillText: null as string | null,
    onActivate: vi.fn(),
    onClose: vi.fn(),
    onDoublePromote: vi.fn(),
  };
  const closeX = () => screen.getByLabelText("Close Tab");

  it("Given 非活动、闲置, When 静止, Then × 不出；悬停且够宽（≥88px）才出", () => {
    render(<Tab {...baseProps} />);

    expect(screen.getByRole("tab")).toHaveClass("group/tab");
    expect(closeX()).toHaveClass(
      "hidden",
      "@min-[88px]:group-hover/tab:inline-flex",
    );
  });

  it("Given 活动、闲置, When 渲染, Then × 始终在，再窄也不收", () => {
    render(<Tab {...baseProps} active />);

    expect(closeX()).not.toHaveClass("hidden");
    expect(closeX().className).not.toMatch(/@max-\[\d+px\]:hidden/);
  });

  it("Given 非活动、running, When 渲染, Then 尾部那一格是 spinner、悬停换成 ×；窄于 88px 时这一格整个收起", () => {
    render(<Tab {...baseProps} status="running" />);

    const spinner = screen.getByTestId("tab-spinner");
    expect(spinner).toHaveClass(
      "hidden",
      "@min-[88px]:inline-flex",
      "group-hover/tab:hidden",
    );
    expect(closeX()).toHaveClass(
      "hidden",
      "@min-[88px]:group-hover/tab:inline-flex",
    );
  });

  it("Given 活动、running, When 渲染, Then 静止时尾部是 spinner（不丢状态），悬停换成 ×；窄于 88px 时 × 常驻", () => {
    render(<Tab {...baseProps} active status="running" />);

    expect(screen.getByTestId("tab-spinner")).toHaveClass(
      "group-hover/tab:hidden",
    );
    expect(closeX()).toHaveClass(
      "hidden",
      "group-hover/tab:inline-flex",
      "@max-[88px]:inline-flex",
    );
  });

  it("Given running, When 窄于 88px, Then spinner 顶替身份方块（方块隐去、占位不变）", () => {
    render(<Tab {...baseProps} status="running" />);

    expect(screen.getByTestId("tab-avatar-spinner")).toHaveClass(
      "hidden",
      "@max-[88px]:inline-flex",
    );
    expect(screen.getByTestId("tab-avatar")).toHaveClass(
      "@max-[88px]:invisible",
    );
  });

  it("Given 不在 running, When 渲染, Then 方块位上没有 spinner", () => {
    render(<Tab {...baseProps} />);

    expect(screen.queryByTestId("tab-avatar-spinner")).toBeNull();
    expect(screen.getByTestId("tab-avatar")).not.toHaveClass(
      "@max-[88px]:invisible",
    );
  });

  it("Given 非活动, When 窄于 56px, Then 标题收起、方块居中；活动 tab 窄于 96px 就收标题", () => {
    const { unmount } = render(<Tab {...baseProps} />);
    expect(screen.getByText("处理周一例会纪要")).toHaveClass(
      "@max-[56px]:hidden",
    );
    expect(screen.getByRole("tab")).toHaveClass(
      "@max-[56px]:justify-center",
      "@max-[56px]:px-0",
    );
    unmount();

    render(<Tab {...baseProps} active />);
    expect(screen.getByText("处理周一例会纪要")).toHaveClass(
      "@max-[96px]:hidden",
    );
    expect(screen.getByRole("tab")).toHaveClass("@max-[96px]:justify-between");
  });

  it("Given 待批胶囊, When 窄于 88px, Then 胶囊收起，只留状态点；窄于 56px 状态点贴到方块右上角", () => {
    render(<Tab {...baseProps} status="waiting" pillText="审批" />);

    expect(screen.getByText("审批")).toHaveClass("@max-[88px]:hidden");
    expect(screen.getByTestId("tab-status-dot")).toHaveClass(
      "@max-[56px]:absolute",
    );
  });
});
