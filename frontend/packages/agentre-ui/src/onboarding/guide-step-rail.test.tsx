import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { GuideStepRail, type GuideStep } from "./guide-step-rail";

const STEPS: readonly GuideStep[] = [
  {
    key: "install",
    title: "Install",
    hint: "agentred",
    doneLabel: "Installed",
  },
  { key: "service", title: "Keep it running", hint: "Stay online" },
  { key: "pair", title: "Pair", hint: "One-time code" },
];

function renderRail(
  over: Partial<React.ComponentProps<typeof GuideStepRail>> = {},
) {
  const onSelect = vi.fn();
  const view = render(
    <GuideStepRail
      steps={STEPS}
      current={1}
      done={[]}
      onSelect={onSelect}
      {...over}
    />,
  );
  return { ...view, onSelect };
}

/**
 * 两端的步骤条此前是两份实现，语义却完全一样。这里钉住的是那份语义，标题与副标题
 * 仍由宿主给——桌面端走「安装 / 常驻 / 配对」，控制台走「安装并登录 / 输码 / 常驻」。
 */
describe("GuideStepRail", () => {
  it("三格都是可点的按钮，当前步骤对辅助技术可识别", () => {
    renderRail({ current: 2 });

    const cells = screen.getAllByRole("button", { name: /^Step \d/ });
    expect(cells).toHaveLength(3);
    expect(cells[1]).toHaveAttribute("aria-current", "step");
    expect(cells[0]).not.toHaveAttribute("aria-current");
  });

  /**
   * 序号只画在 aria-hidden 的徽标里，读屏拿不到；不自带序号的话，这一格听起来就是
   * 一个带副标题尾巴的普通按钮。
   */
  it("每一格的无障碍名称自带序号，不与格内文本重名", () => {
    renderRail();

    expect(
      screen.getByRole("button", { name: "Step 3: Pair" }),
    ).toBeInTheDocument();
  });

  it("点任意一格都把那一步交回宿主，不自己改当前步骤", () => {
    const { onSelect } = renderRail();

    fireEvent.click(screen.getByRole("button", { name: "Step 3: Pair" }));

    expect(onSelect).toHaveBeenCalledWith(3);
  });

  /** 完成态由宿主判定，副标题跟着换；没给 doneLabel 的格子保持原副标题。 */
  it("已完成的格子换成完成副标题", () => {
    renderRail({ current: 2, done: [1, 2] });

    expect(
      screen.getByRole("button", { name: "Step 1: Install" }),
    ).toHaveTextContent("Installed");
    expect(
      screen.getByRole("button", { name: "Step 2: Keep it running" }),
    ).toHaveTextContent("Stay online");
  });

  /** 配对在途时宿主会锁住整条步骤条，免得表单被提前卸载吞掉失败原因。 */
  it("disabled 时三格全部不可点", () => {
    const { onSelect } = renderRail({ disabled: true });

    const cell = screen.getByRole("button", { name: "Step 3: Pair" });
    expect(cell).toBeDisabled();
    fireEvent.click(cell);
    expect(onSelect).not.toHaveBeenCalled();
  });

  /** 省略 onDismiss 即不可收起——设备列表为空时宿主没有可回退的地方。 */
  it("没有 onDismiss 就不画收起控件", () => {
    renderRail();
    expect(screen.queryByRole("button", { name: "Collapse guide" })).toBeNull();
  });

  it("给了 onDismiss 就画一个收起控件并把点击交回去", () => {
    const onDismiss = vi.fn();
    renderRail({ onDismiss });

    fireEvent.click(screen.getByRole("button", { name: "Collapse guide" }));

    expect(onDismiss).toHaveBeenCalledTimes(1);
  });
});
