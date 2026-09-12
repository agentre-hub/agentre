import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { Switch } from "../index";

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
});
