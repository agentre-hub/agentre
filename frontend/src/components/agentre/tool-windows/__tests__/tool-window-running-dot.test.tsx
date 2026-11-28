import { render, screen, within } from "@testing-library/react";
import { Terminal } from "lucide-react";
import { describe, expect, it } from "vitest";

import { TooltipProvider } from "@agentre-hub/agentre-ui";

import { ToolWindowLayout } from "../tool-window-layout";
import type { ToolWindowDefinition } from "../types";

function renderStrip(running: boolean | undefined) {
  const defs: ToolWindowDefinition[] = [
    {
      id: "terminal",
      label: "Terminal",
      icon: Terminal,
      running,
      content: <div>terminal body</div>,
    },
  ];
  return render(
    <TooltipProvider>
      <ToolWindowLayout windows={defs}>
        <main>editor</main>
      </ToolWindowLayout>
    </TooltipProvider>,
  );
}

const button = () =>
  within(screen.getByRole("toolbar", { name: "Tool windows" })).getByRole(
    "button",
    { name: "Terminal" },
  );

describe("工具窗图标的运行中圆点", () => {
  // Given 工具窗报告有进程在跑, Then 图标上有圆点,且读屏能读到「运行中」。
  it("shows a dot with an accessible description while running", () => {
    renderStrip(true);
    expect(
      within(button()).getByTestId("tool-window-running-dot"),
    ).toBeTruthy();
    expect(button().getAttribute("aria-describedby")).toBeTruthy();
    expect(
      document.getElementById(button().getAttribute("aria-describedby")!)
        ?.textContent,
    ).toBe("Running");
  });

  // Given 没有进程在跑, Then 没有圆点。
  it("shows no dot when nothing is running", () => {
    renderStrip(false);
    expect(
      within(button()).queryByTestId("tool-window-running-dot"),
    ).toBeNull();
  });

  // Given 工具窗没有「运行中」概念(不传), Then 没有圆点。
  it("shows no dot for a window without a running notion", () => {
    renderStrip(undefined);
    expect(
      within(button()).queryByTestId("tool-window-running-dot"),
    ).toBeNull();
  });
});
