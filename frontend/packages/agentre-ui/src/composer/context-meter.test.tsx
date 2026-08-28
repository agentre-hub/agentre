import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ContextMeter } from "./context-meter";
import { usageLevel } from "./usage-level";

/**
 * 上下文计量器。此前桌面端 `chat.tsx`（ContextRing / ContextPanel / ContextMeter）
 * 与 agentre-server 的 `SessionComposer.tsx` 各持一份逐行同构的副本：环几何、
 * 200/100 悬停延迟、色阶表、228px 面板、两种语言的文案全都一样。
 *
 * 浮窗用 `focusIn` 打开而不是模拟悬停：token 绝对值已经降级成「悬停才拿得到」，
 * 键盘用户能不能拿到它们正是这里要守的东西（jsdom 也没有真实的 hover 计时）。
 */
describe("ContextMeter", () => {
  it("Given a usage figure, When rendered, Then it is a focusable button showing only the ring and the percentage", () => {
    render(<ContextMeter used={41_200} max={200_000} />);

    const meter = screen.getByRole("button");
    expect(meter.tagName).toBe("BUTTON");
    // 原生 title 只有鼠标拿得到；绝对值必须走 aria + 浮窗，不许藏在 title 里。
    expect(meter.hasAttribute("title")).toBe(false);
    expect(meter.textContent).toContain("21%");
    expect(meter.textContent).not.toContain("41");
  });

  it("Given a usage figure, When the accessible name is read, Then it carries used, limit and percentage", () => {
    render(<ContextMeter used={41_200} max={200_000} />);

    expect(screen.getByRole("button")).toHaveAttribute(
      "aria-label",
      "Context usage 41.2k / 200k, 21% used",
    );
  });

  it("Given a usage figure, When the ring is inspected, Then it reports the raw counts as a progressbar", () => {
    render(<ContextMeter used={41_200} max={200_000} />);

    const ring = within(screen.getByRole("button")).getByRole("progressbar");
    expect(ring).toHaveAttribute("aria-valuenow", "41200");
    expect(ring).toHaveAttribute("aria-valuemax", "200000");
    expect(ring).toHaveAttribute("aria-valuemin", "0");
  });

  it("Given normal usage, When the arc is inspected, Then it is primary-toned and animates between values", () => {
    render(<ContextMeter used={41_200} max={200_000} />);

    const arc = arcOf();
    expect(arc).toHaveClass("stroke-primary");
    // 弧长变化要有过渡，否则数字跳一下环会瞬移。
    expect(arc.className).toContain("transition-[stroke-dashoffset]");
  });

  it("Given usage at or above 75 percent, When the arc is inspected, Then it turns to the waiting tone", () => {
    render(<ContextMeter used={164_000} max={200_000} />);

    expect(screen.getByRole("button").textContent).toContain("82%");
    expect(arcOf()).toHaveClass("stroke-status-waiting");
  });

  it("Given usage at or above 90 percent, When the arc is inspected, Then it turns to the error tone", () => {
    render(<ContextMeter used={190_000} max={200_000} />);

    expect(arcOf()).toHaveClass("stroke-status-error");
  });

  it("Given a ratio that rounds up across the danger threshold, When levelled, Then the un-rounded ratio decides", () => {
    // 89.5% 显示成 90%，但分级必须按未取整的比值算 —— 否则最该读准的那一档会
    // 因为四舍五入提前变红。
    render(<ContextMeter used={179_000} max={200_000} />);

    expect(screen.getByRole("button").textContent).toContain("90%");
    expect(arcOf()).toHaveClass("stroke-status-waiting");
  });

  it("Given the meter is focused, When the panel opens, Then used, limit and remaining are all readable", async () => {
    render(<ContextMeter used={41_200} max={200_000} />);

    fireEvent.focusIn(screen.getByRole("button"));

    expect(await screen.findByText("Context")).toBeInTheDocument();
    expect(screen.getByText("41.2k")).toBeInTheDocument();
    expect(screen.getByText("/ 200k")).toBeInTheDocument();
    expect(screen.getByText("Remaining")).toBeInTheDocument();
    expect(screen.getByText("159k")).toBeInTheDocument();
  });

  it("Given normal usage, When the panel opens, Then the footnote states how the figure is counted", async () => {
    render(<ContextMeter used={41_200} max={200_000} />);

    fireEvent.focusIn(screen.getByRole("button"));

    expect(
      await screen.findByText(
        "Counted from the latest model call's total input",
      ),
    ).toBeInTheDocument();
  });

  it("Given usage past the warning threshold, When the panel opens, Then the footnote warns about the limit instead", async () => {
    render(<ContextMeter used={164_000} max={200_000} />);

    fireEvent.focusIn(screen.getByRole("button"));

    expect(
      await screen.findByText("Close to the context window limit"),
    ).toBeInTheDocument();
  });

  it("Given a host test id, When rendered, Then it lands on the trigger", () => {
    render(
      <ContextMeter
        used={41_200}
        max={200_000}
        dataTestId="composer-context-meter"
      />,
    );

    expect(screen.getByTestId("composer-context-meter").tagName).toBe("BUTTON");
  });

  it("Given a used count below zero, When rendered, Then it is clamped rather than drawing a negative arc", () => {
    render(<ContextMeter used={-5} max={200_000} />);

    expect(screen.getByRole("button").textContent).toContain("0%");
    expect(
      within(screen.getByRole("button")).getByRole("progressbar"),
    ).toHaveAttribute("aria-valuenow", "0");
  });
});

/**
 * 阈值只有这一份：桌面端的 `QuotaMeter` 也委托它。此前它是桌面端 `chat.tsx` 里的
 * `quotaLevel`，注释点名「同一个文件里两套 90/75 常量迟早会改漏一处」——把计量器
 * 搬进包而把阈值留在原地，正好会制造出那种形态。
 */
describe("usageLevel", () => {
  it("Given a percentage below 75, When levelled, Then it is ok", () => {
    expect(usageLevel(0)).toBe("ok");
    expect(usageLevel(74.9)).toBe("ok");
  });

  it("Given a percentage from 75 up to 90, When levelled, Then it is a warning", () => {
    expect(usageLevel(75)).toBe("warn");
    expect(usageLevel(89.9)).toBe("warn");
  });

  it("Given a percentage of 90 or more, When levelled, Then it is dangerous", () => {
    expect(usageLevel(90)).toBe("danger");
    expect(usageLevel(100)).toBe("danger");
  });

  it("Given no percentage at all, When levelled, Then it is ok rather than throwing", () => {
    // 桌面端 QuotaMeter 的窗口可能整个缺失（未登录 / 无凭据），它传 null。
    expect(usageLevel(null)).toBe("ok");
  });
});

function arcOf(): Element {
  const arc = within(screen.getByRole("button"))
    .getByRole("progressbar")
    .querySelector("[data-slot='context-ring-arc']");
  if (!arc) throw new Error("context ring arc not rendered");
  return arc;
}
