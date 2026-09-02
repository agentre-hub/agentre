import "@testing-library/jest-dom/vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ReasoningEffortPicker } from "./reasoning-effort-picker";

/**
 * 会话级思考力度选择器（spec 2026-09-01「会话级思考力度的选择与生效」）。
 *
 * 这颗控件在桌面端与 agentre-server 的 composer 底栏各渲染一次，所以它在包里，
 * 只吃平坦入参：会话行上的值、后端配置的值、一个回调。持久化、IPC、回滚全在宿主。
 *
 * 两个最容易写错、也最贵的边界各有一条用例：
 *   - 脸上与高亮读的是**有效**档位（会话值为空时回落后端值）；
 *   - 而 no-op 判据读的是**会话行上的值**：会话为空 + 后端 high 时选 high 是一次
 *     真实写入（把「跟随后端」钉成「就是 high」），不是 no-op。
 */
function openPicker() {
  return userEvent.setup();
}

function rowFor(level: string) {
  return screen.getByRole("option", { name: new RegExp(`^${level}`) });
}

describe("ReasoningEffortPicker", () => {
  it("Given a session level, When rendered, Then the face shows it", () => {
    render(
      <ReasoningEffortPicker
        value="xhigh"
        backendValue="high"
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByRole("button")).toHaveTextContent("xhigh");
  });

  it("Given no session level, When rendered, Then the face falls back to the backend level", () => {
    render(
      <ReasoningEffortPicker value="" backendValue="high" onChange={vi.fn()} />,
    );

    expect(screen.getByRole("button")).toHaveTextContent("high");
  });

  it("Given neither level, When rendered, Then the face reads Default and the name announces it", () => {
    render(
      <ReasoningEffortPicker value="" backendValue="" onChange={vi.fn()} />,
    );

    const trigger = screen.getByRole("button");
    expect(trigger).toHaveTextContent("Default");
    expect(trigger).toHaveAttribute("aria-label", "Reasoning effort: Default");
  });

  it("Given the picker, When opened, Then it lists the default block plus the five levels", async () => {
    const user = openPicker();
    render(
      <ReasoningEffortPicker value="" backendValue="high" onChange={vi.fn()} />,
    );

    await user.click(screen.getByRole("button"));

    expect(
      screen.getAllByRole("option").map((row) => row.dataset.effort),
    ).toEqual(["", "low", "medium", "high", "xhigh", "max"]);
  });

  it("Given a backend-resolved level, When opened, Then the default block resolves to it", async () => {
    const user = openPicker();
    render(
      <ReasoningEffortPicker value="" backendValue="high" onChange={vi.fn()} />,
    );

    await user.click(screen.getByRole("button"));

    const defaultRow = screen.getAllByRole("option")[0];
    expect(defaultRow).toHaveTextContent("Follows backend config");
    expect(defaultRow).toHaveTextContent("high");
  });

  it("Given no backend level either, When opened, Then the default block says the backend is unset", async () => {
    const user = openPicker();
    render(
      <ReasoningEffortPicker value="" backendValue="" onChange={vi.fn()} />,
    );

    await user.click(screen.getByRole("button"));

    expect(screen.getAllByRole("option")[0]).toHaveTextContent(
      "Follows backend config · unset",
    );
  });

  it("Given the effective level comes from the backend, When opened, Then that row alone is the current one", async () => {
    const user = openPicker();
    render(
      <ReasoningEffortPicker value="" backendValue="high" onChange={vi.fn()} />,
    );

    await user.click(screen.getByRole("button"));

    expect(rowFor("high")).toHaveAttribute("data-current", "true");
    expect(rowFor("high")).toHaveAttribute("aria-selected", "true");
    expect(within(rowFor("high")).getByText("Current")).toBeInTheDocument();
    for (const level of ["low", "medium", "xhigh", "max"]) {
      expect(rowFor(level)).not.toHaveAttribute("data-current", "true");
      expect(rowFor(level)).toHaveAttribute("aria-selected", "false");
    }
  });

  it("Given each level row, When opened, Then the strength dot walks the heat scale", async () => {
    const user = openPicker();
    render(
      <ReasoningEffortPicker value="" backendValue="" onChange={vi.fn()} />,
    );

    await user.click(screen.getByRole("button"));

    for (const [index, level] of [
      "low",
      "medium",
      "high",
      "xhigh",
      "max",
    ].entries()) {
      const dot = within(rowFor(level)).getByTestId("effort-dot");
      expect(dot).toHaveClass(`bg-heat-${index}`);
    }
  });

  it("Given a different level, When it is picked, Then the change is reported", async () => {
    const user = openPicker();
    const onChange = vi.fn();
    render(
      <ReasoningEffortPicker value="low" backendValue="" onChange={onChange} />,
    );

    await user.click(screen.getByRole("button"));
    await user.click(rowFor("max"));

    expect(onChange).toHaveBeenCalledWith("max");
  });

  it("Given the level already on the session row, When it is picked again, Then nothing is reported", async () => {
    const user = openPicker();
    const onChange = vi.fn();
    render(
      <ReasoningEffortPicker
        value="high"
        backendValue="low"
        onChange={onChange}
      />,
    );

    await user.click(screen.getByRole("button"));
    await user.click(rowFor("high"));

    expect(onChange).not.toHaveBeenCalled();
  });

  it("Given an empty session row, When Default is picked again, Then nothing is reported", async () => {
    const user = openPicker();
    const onChange = vi.fn();
    render(
      <ReasoningEffortPicker
        value=""
        backendValue="high"
        onChange={onChange}
      />,
    );

    await user.click(screen.getByRole("button"));
    await user.click(screen.getAllByRole("option")[0]);

    expect(onChange).not.toHaveBeenCalled();
  });

  it("Given an empty session row resolving to high, When high is picked, Then it is a real write", async () => {
    const user = openPicker();
    const onChange = vi.fn();
    render(
      <ReasoningEffortPicker
        value=""
        backendValue="high"
        onChange={onChange}
      />,
    );

    await user.click(screen.getByRole("button"));
    await user.click(rowFor("high"));

    expect(onChange).toHaveBeenCalledWith("high");
  });

  it("Given a session level, When Default is picked, Then it reports the empty value", async () => {
    const user = openPicker();
    const onChange = vi.fn();
    render(
      <ReasoningEffortPicker
        value="max"
        backendValue="high"
        onChange={onChange}
      />,
    );

    await user.click(screen.getByRole("button"));
    await user.click(screen.getAllByRole("option")[0]);

    expect(onChange).toHaveBeenCalledWith("");
  });

  it("Given only the keyboard, When the picker is opened and a row is chosen, Then the change is reported", async () => {
    const user = openPicker();
    const onChange = vi.fn();
    render(
      <ReasoningEffortPicker value="" backendValue="" onChange={onChange} />,
    );

    await user.tab();
    expect(screen.getByRole("button")).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(screen.getAllByRole("option")).toHaveLength(6);

    // 弹层里的每一行都是真正的按钮：展开后焦点就落在首行，Tab 顺序即档位顺序。
    expect(screen.getAllByRole("option")[0]).toHaveFocus();
    await user.tab();
    await user.tab();
    expect(rowFor("medium")).toHaveFocus();
    await user.keyboard("{Enter}");

    expect(onChange).toHaveBeenCalledWith("medium");
  });

  it("Given the popover, When opened, Then the effect timing note is always shown", async () => {
    const user = openPicker();
    render(
      <ReasoningEffortPicker value="" backendValue="" onChange={vi.fn()} />,
    );

    await user.click(screen.getByRole("button"));

    expect(
      screen.getByText(
        "Takes effect from the next turn; the current turn is unaffected.",
      ),
    ).toBeInTheDocument();
  });

  it("Given a failed write, When the popover is opened, Then the reason is shown as an alert", async () => {
    const user = openPicker();
    render(
      <ReasoningEffortPicker
        value=""
        backendValue=""
        onChange={vi.fn()}
        errorText="session not found"
      />,
    );

    await user.click(screen.getByRole("button"));

    expect(screen.getByRole("alert")).toHaveTextContent("session not found");
  });

  it("Given the read-out shape, When the trigger is inspected, Then it has no fill or border until hover and keeps a chevron", () => {
    render(
      <ReasoningEffortPicker
        value="xhigh"
        backendValue=""
        onChange={vi.fn()}
        dataTestId="effort-pill"
      />,
    );

    const trigger = screen.getByTestId("effort-pill");
    // 决策 9：与右侧两个计量器同款读数形态——描边透明、hover 才显边框，
    // 不套 ProviderPill 那身有填充有描边的 pill 外壳。
    expect(trigger.className).toContain("border-transparent");
    expect(trigger.className).toContain("hover:border-border");
    expect(trigger.className).not.toContain("bg-input-bg");
    // 但它可点：计量器是 cursor-default，这颗必须是手形。
    expect(trigger.className).toContain("cursor-pointer");
    expect(trigger).toHaveAttribute("aria-haspopup", "listbox");
    expect(within(trigger).getByTestId("effort-chevron")).toBeInTheDocument();
  });
});
