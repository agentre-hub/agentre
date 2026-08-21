import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { MachineGroupHeader } from "./machine-group-header";

const online = { deviceId: 0, name: "本机", online: true };
const offline = { deviceId: 7, name: "构建机", online: false };

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

  it("离线的机器组头置灰，但**仍可展开** —— 本体在本地库里，离线只影响能不能继续跑", () => {
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

    const status = screen.getByTestId("machine-group-status");
    expect(status.dataset.online).toBe("false");

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
    // 与项目组头、随手对话组头同一套投影 —— 不写死一个颜色。
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

  it("虚拟组没有 ⋮：一台机器上没有设置 / 删除可言", () => {
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
});
