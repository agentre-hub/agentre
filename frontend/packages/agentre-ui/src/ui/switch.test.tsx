import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { Switch } from "../index";
import { readTokens } from "../styles/__testing__/tokens";

describe("Switch", () => {
  it("toggles accessibly and keeps the dark-mode thumb white", async () => {
    const user = userEvent.setup();

    render(<Switch aria-label="启用通知" />);

    const control = screen.getByRole("switch", { name: "启用通知" });
    const thumb = control.querySelector('[data-slot="switch-thumb"]');

    expect(control).toHaveAttribute("aria-checked", "false");
    expect(thumb).toHaveClass(
      "dark:data-[state=checked]:bg-white",
      "dark:data-[state=unchecked]:bg-white",
    );

    await user.click(control);

    expect(control).toHaveAttribute("aria-checked", "true");
  });

  it("亮色下滑块是纯白并带投影，从浅灰轨道上托出来", () => {
    // 亮色轨道是 --input #cbcbd0，滑块此前是 --background #fafafa 且没有投影：
    // 两者只差 1.55，关着的开关看上去是一整块浅灰。轨道刻意不加深（那一档是给
    // 「轮廓即控件」的），所以靠滑块自己站出来。
    render(<Switch aria-label="启用通知" />);

    const thumb = screen
      .getByRole("switch", { name: "启用通知" })
      .querySelector('[data-slot="switch-thumb"]') as HTMLElement;
    const classes = Array.from(thumb.classList);
    const fill = classes
      .map((c) => /^bg-([a-z]+(?:-[a-z]+)*)$/.exec(c)?.[1])
      .find(Boolean) as string;

    expect(readTokens(":root")[fill]?.toLowerCase()).toBe("#ffffff");
    expect(classes.some((c) => /^shadow-(?!none)/.test(c))).toBe(true);
    // 暗色的白滑块压在深轨道上本来就有 9:1，不需要投影，外观保持原样。
    expect(classes).toContain("dark:shadow-none");
  });
});
