import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { MachineGroupHeader } from "./machine-group-header";

const online = { name: "本机", online: true };
const offline = { name: "构建机", online: false };

describe("MachineGroupHeader", () => {
  it("报机器名，并用状态点说在不在线", () => {
    render(
      <MachineGroupHeader
        machine={online}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    expect(screen.getByText("本机")).toBeInTheDocument();
    expect(screen.getByTestId("machine-group-status").dataset.online).toBe(
      "true",
    );
  });

  it("状态点那一格与项目组头的字形同尺寸 —— 几种组头的名字起始 x 要对齐", () => {
    render(
      <MachineGroupHeader
        machine={online}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    expect(screen.getByTestId("machine-group-status").className).toContain(
      "size-6",
    );
  });

  it("离线的机器组头置灰，但**仍可展开** —— 本体在库里，离线只影响能不能继续跑", () => {
    const onToggle = vi.fn();
    render(
      <MachineGroupHeader
        machine={offline}
        expanded={false}
        onToggle={onToggle}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    expect(screen.getByTestId("machine-group-status").dataset.online).toBe(
      "false",
    );
    const toggle = screen.getByRole("button");
    expect(toggle).not.toBeDisabled();
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
  });

  it("点组头把折叠交回宿主", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onToggle = vi.fn();
    render(
      <MachineGroupHeader
        machine={offline}
        expanded
        onToggle={onToggle}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    await user.click(screen.getByRole("button"));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("需要关注的条数带一枚按最强 reason 着色的记号；0 条时不摆", () => {
    const { unmount } = render(
      <MachineGroupHeader
        machine={online}
        expanded
        onToggle={vi.fn()}
        attentionCount={3}
        attentionTone="error"
      />,
    );
    const mark = screen.getByTestId("machine-attention-mark");
    expect(mark).toHaveTextContent("3");
    expect(mark.className).toContain("text-status-error");
    unmount();

    render(
      <MachineGroupHeader
        machine={online}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );
    expect(screen.queryByTestId("machine-attention-mark")).toBeNull();
  });

  it("一台机器没有设置 / 删除可言，所以不给就没有任何多余按钮", () => {
    render(
      <MachineGroupHeader
        machine={online}
        expanded
        onToggle={vi.fn()}
        attentionCount={0}
        attentionTone={null}
      />,
    );

    expect(screen.getAllByRole("button")).toHaveLength(1);
  });

  it("宿主自己的角标与动作各就各位：角标跟名字走，动作在折叠按钮外", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    const onRetry = vi.fn();
    render(
      <MachineGroupHeader
        machine={offline}
        expanded
        onToggle={onToggle}
        attentionCount={0}
        attentionTone={null}
        badges={<span data-testid="badge">需升级</span>}
        actions={
          <button type="button" aria-label="Retry" onClick={onRetry}>
            r
          </button>
        }
      />,
    );

    const toggle = screen.getByRole("button", { expanded: true });
    expect(toggle.contains(screen.getByTestId("badge"))).toBe(true);
    const retry = screen.getByRole("button", { name: "Retry" });
    expect(toggle.contains(retry)).toBe(false);

    await user.click(retry);
    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(onToggle).not.toHaveBeenCalled();
  });
});
