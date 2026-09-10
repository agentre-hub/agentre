import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { agentreUiResources } from "../i18n";

import { AxisPicker } from "./axis-picker";
import type { IndexAxis } from "./axis-groups";

/** 桌面端只 offer 三档（没有机器轴），server 控制台四档全给（决策 17）。 */
const DESKTOP_AXES: IndexAxis[] = ["project", "agent", "time"];
const SERVER_AXES: IndexAxis[] = ["project", "agent", "time", "machine"];

// Radix 菜单在 happy-dom 中需要关闭 pointerEvents 检查。
function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

describe("AxisPicker", () => {
  it("选中一档时把那个轴报给宿主", async () => {
    const user = setupUser();
    const onChange = vi.fn();
    render(
      <AxisPicker value="project" axes={DESKTOP_AXES} onChange={onChange} />,
    );

    await user.click(screen.getByTestId("axis-picker"));
    await user.click(await screen.findByTestId("axis-option-agent"));

    expect(onChange).toHaveBeenCalledWith("agent");
  });

  it("可选轴清单由宿主给：宿主没 offer 的轴选不着", async () => {
    // 多出来的那一档写得进地址栏却取不到数 —— 桌面端没有机器这一维。
    const user = setupUser();
    render(
      <AxisPicker value="project" axes={DESKTOP_AXES} onChange={vi.fn()} />,
    );

    await user.click(screen.getByTestId("axis-picker"));

    await screen.findByTestId("axis-option-project");
    expect(screen.queryByTestId("axis-option-machine")).toBeNull();
  });

  it("宿主 offer 四档时四档都在，顺序照宿主给的清单", async () => {
    const user = setupUser();
    render(
      <AxisPicker value="machine" axes={SERVER_AXES} onChange={vi.fn()} />,
    );

    await user.click(screen.getByTestId("axis-picker"));
    await screen.findByTestId("axis-option-project");

    expect(
      SERVER_AXES.map(
        (axis) => screen.getByTestId(`axis-option-${axis}`).textContent,
      ),
    ).toEqual(["Project", "Agent", "Time", "Machine"]);
  });

  it("288px 那一行只放当前值，「分组」二字不上行（决策 3）", () => {
    render(<AxisPicker value="agent" axes={DESKTOP_AXES} onChange={vi.fn()} />);

    // 带上标签这一行就会把「未读 N」chip 挤到第二行；可发现性交给 title，
    // 它不占行内像素。
    const trigger = screen.getByTestId("axis-picker");
    expect(trigger.textContent).toBe("Agent");
    expect(trigger.getAttribute("title")).toBe(
      agentreUiResources.en.sessionIndex.axis.title,
    );
  });

  it("菜单里有「分组方式」这个标题 —— 那里不在 288px 的行上，决策 3 不与它为难", async () => {
    const user = setupUser();
    render(
      <AxisPicker value="project" axes={DESKTOP_AXES} onChange={vi.fn()} />,
    );

    await user.click(screen.getByTestId("axis-picker"));

    expect(await screen.findByTestId("axis-picker-label")).toHaveTextContent(
      agentreUiResources.en.sessionIndex.axis.title,
    );
  });

  it("两个语种里的档名都不带「分组」二字，解释的活儿交给 title（决策 3）", () => {
    for (const bundle of Object.values(agentreUiResources)) {
      const axis = bundle.sessionIndex.axis;
      for (const label of [axis.project, axis.agent, axis.time, axis.machine]) {
        expect(label).not.toMatch(/group/i);
        expect(label).not.toContain("分组");
      }
      expect(axis.title.length).toBeGreaterThan(0);
    }
  });

  it("光键盘也走得完：开菜单、方向键挪、回车选中", async () => {
    // 这是「用 radix 的菜单而不是自己摞几个按钮」换来的东西 —— 手搓的那版看起来
    // 一样，按方向键没反应。
    const user = setupUser();
    const onChange = vi.fn();
    render(
      <AxisPicker value="project" axes={DESKTOP_AXES} onChange={onChange} />,
    );

    screen.getByTestId("axis-picker").focus();
    await user.keyboard("{Enter}");
    await screen.findByTestId("axis-option-project");
    await user.keyboard("{ArrowDown}{Enter}");

    expect(onChange).toHaveBeenCalledWith("agent");
  });

  it("当前选中的那一档在读屏里也说得出来，不只靠颜色", async () => {
    const user = setupUser();
    render(<AxisPicker value="time" axes={DESKTOP_AXES} onChange={vi.fn()} />);

    await user.click(screen.getByTestId("axis-picker"));

    expect(await screen.findByTestId("axis-option-time")).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(screen.getByTestId("axis-option-project")).toHaveAttribute(
      "aria-checked",
      "false",
    );
  });
});
