import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ProjectChipPicker } from "../../command-palette/project-chip-picker";

/**
 * 命令面板里切换项目上下文的那颗 chip。
 *
 * 它是一颗手写的 <button>，此前带着 `outline-none` 却没有任何聚焦样式：键盘从
 * 搜索框 Tab 过来，焦点落在它身上而界面没有任何变化。按钮类控件的键盘焦点
 * 一律是共享的那一圈实色环（docs/design.md §10）。
 */
describe("ProjectChipPicker", () => {
  it.each([
    ["with a project in context", { projectID: 3, projectName: "agentre" }],
    ["with no project in context", null],
  ])(
    "Given the chip %s, When it takes keyboard focus, Then it draws the shared outline",
    (_label, projectContext) => {
      render(
        <ProjectChipPicker
          projectContext={projectContext as never}
          projects={[]}
          open={false}
          onOpenChange={vi.fn()}
          onPick={vi.fn()}
        />,
      );

      expect(
        screen.getByRole("button", { name: "Switch project context" }),
      ).toHaveClass("focus-visible:focus-outline");
    },
  );
});
