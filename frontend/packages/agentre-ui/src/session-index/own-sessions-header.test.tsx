import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { OwnSessionsHeader } from "./own-sessions-header";

describe("OwnSessionsHeader", () => {
  it("父项目自己的会话有自己的折叠箭头，读屏念得出是谁的（不与子项目绑死）", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    render(
      <OwnSessionsHeader
        name="Agentre"
        count={7}
        expanded
        onToggle={onToggle}
      />,
    );

    const header = screen.getByRole("button", {
      name: "Toggle Agentre sessions",
    });
    expect(header).toHaveAttribute("aria-expanded", "true");
    expect(header).toHaveTextContent("Sessions");
    expect(header).toHaveTextContent("7");

    await user.click(header);
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("折叠态如实报折叠", () => {
    render(
      <OwnSessionsHeader
        name="Agentre"
        count={0}
        expanded={false}
        onToggle={vi.fn()}
      />,
    );

    expect(screen.getByRole("button")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });
});
