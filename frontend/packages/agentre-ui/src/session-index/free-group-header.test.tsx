import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { FreeGroupHeader } from "./free-group-header";

describe("FreeGroupHeader", () => {
  it("一条自由会话都没有时组头照在，＋ 也照在（决策 6）", async () => {
    const user = userEvent.setup();
    const onNewSession = vi.fn();
    render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
        onNewSession={onNewSession}
      />,
    );

    expect(screen.getByTestId("free-group-header")).toBeInTheDocument();
    expect(screen.getByText("Quick chats")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "New quick chat" }));
    expect(onNewSession).toHaveBeenCalledTimes(1);
  });

  it("虚拟组没有可设置的东西，所以没有 ⋮（决策 6）", () => {
    render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={1}
        attentionTone={null}
        onNewSession={vi.fn()}
      />,
    );

    // 虚拟组没有设置 / 子项目 / 合并 / 删除可言 —— 挂一个菜单上去是骗人。
    // 组头上只该有两个按钮：折叠与 ＋。
    const buttons = screen.getAllByRole("button");
    expect(buttons).toHaveLength(2);
    expect(buttons.map((button) => button.getAttribute("aria-label"))).toEqual([
      null,
      "New quick chat",
    ]);
    // 而且组头里没有任何菜单接线。
    const header = screen.getByTestId("free-group-header");
    expect(
      header.querySelector(
        "[data-slot='dropdown-menu-trigger'],[data-slot='context-menu-trigger']",
      ),
    ).toBeNull();
  });

  it("点组头这一行就折叠 / 展开", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    render(
      <FreeGroupHeader
        expanded={false}
        onToggle={onToggle}
        attentionCount={0}
        attentionTone={null}
        onNewSession={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("button", { expanded: false }));

    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("需要关注的条数带一枚圆点；0 条时连圆点都不摆", () => {
    const { unmount } = render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={3}
        attentionTone="running"
        onNewSession={vi.fn()}
      />,
    );
    expect(screen.getByTestId("free-attention-mark")).toHaveTextContent("3");
    unmount();

    render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
        onNewSession={vi.fn()}
      />,
    );
    // 组内总条数**不在**组头上：条数就在下面列着，写出来是复述。
    expect(screen.queryByTestId("free-attention-mark")).toBeNull();
  });

  it("记号按最强 reason 着色，不写死绿色 —— 与项目组头同一枚记号", () => {
    render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={2}
        attentionTone="error"
        onNewSession={vi.fn()}
      />,
    );

    const mark = screen.getByTestId("free-attention-mark");
    expect(mark.className).toContain("text-status-error");
    expect(mark.querySelector("span")?.className).toContain("bg-status-error");
  });
});

/**
 * 导入入口（规格 2026-08-26，决策 13）：四条轴共用同一条菜单条目，所以这一档也得
 * 有个地方挂宿主递进来的动作 —— 它与 ＋ 并排，不是替换 ＋。
 */
describe("宿主递进来的动作", () => {
  it("Given 宿主同时给了 ＋ 与一枚自己的动作, When 组头渲染, Then 两个都在（新动作不把 ＋ 挤掉）", () => {
    render(
      <FreeGroupHeader
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
        onNewSession={vi.fn()}
        actions={<button type="button" data-testid="host-action" />}
      />,
    );

    expect(screen.getByTestId("free-group-header-plus")).toBeTruthy();
    expect(screen.getByTestId("host-action")).toBeTruthy();
  });
});
