import { render, screen } from "@testing-library/react";
import { Image as ImageIcon, Smile } from "lucide-react";
import { describe, expect, it, vi } from "vitest";

import { ModeChip } from "./mode-chip";

// 规格「结构线」：选中态靠填充，不靠外框圈出来。
describe("ModeChip", () => {
  it("Given an active and an idle chip, When rendered, Then neither draws an outline and the active one is the soft brand fill", () => {
    render(
      <>
        <ModeChip
          active
          icon={Smile}
          label="Icon"
          hint="lucide"
          onClick={vi.fn()}
        />
        <ModeChip
          active={false}
          icon={ImageIcon}
          label="Image"
          hint="png"
          onClick={vi.fn()}
        />
      </>,
    );

    const active = screen.getByRole("tab", { name: /Icon/ });
    const idle = screen.getByRole("tab", { name: /Image/ });
    for (const chip of [active, idle]) {
      expect(chip.className).not.toMatch(/(^|\s)border(-[a-z]+)?(\s|$)/);
    }
    expect(active).toHaveClass("bg-primary-soft");
  });
});
