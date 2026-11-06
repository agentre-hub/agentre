import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { OrgAgentRow } from "./org-agent-row";
import type { OrgIndexRow } from "./org-index-model";

const row = { agent: { id: 7, name: "Eva" } } as OrgIndexRow;

function renderRow(gap?: string) {
  return render(
    <OrgAgentRow
      row={row}
      indent={0}
      selected={false}
      onSelect={vi.fn()}
      gap={gap}
    />,
  );
}

describe("OrgAgentRow gap", () => {
  it("给了 gap：画 6px status-waiting 点，可读名带原因", () => {
    const { container } = renderRow("Cannot chat yet");
    const dot = container.querySelector("[data-gap-dot]")!;
    expect(dot).toBeInTheDocument();
    expect(dot).toHaveAttribute("aria-hidden", "true");
    expect(dot.className).toContain("size-1.5");
    expect(dot.className).toContain("bg-status-waiting");
    expect(
      screen.getByRole("button", { name: "Eva, Cannot chat yet" }),
    ).toBeInTheDocument();
  });

  it("没给 gap：无点，可读名仍是名字本身", () => {
    const { container } = renderRow();
    expect(container.querySelector("[data-gap-dot]")).toBeNull();
    expect(screen.getByTestId("org-row-select-7")).not.toHaveAttribute(
      "aria-label",
    );
  });
});
