import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { consumeNewAgentDialogIntent } from "@/stores/new-agent-intent-store";

import { newAgentSource } from "./new-agent-source";

const item = newAgentSource.useItems().items[0];

describe("newAgentSource", () => {
  it("matches the New agent command and renders its action copy", () => {
    expect(newAgentSource.modes).toEqual(["command"]);
    expect(newAgentSource.getScore("New agent", item)).toBeGreaterThan(0);
    expect(newAgentSource.getScore("other command", item)).toBe(0);

    render(<>{newAgentSource.renderItem(item, { active: false })}</>);
    expect(screen.getByText("New agent")).toBeInTheDocument();
    expect(
      screen.getByText("Create a new assistant/employee in the org chart"),
    ).toBeInTheDocument();
  });

  it("Given the New agent command, when selected, then it navigates to org and writes the intent", () => {
    consumeNewAgentDialogIntent();
    const navigate = vi.fn();
    const close = vi.fn();

    newAgentSource.onSelect(item, {
      navigate,
      close,
      openSession: vi.fn(),
      openNewSession: vi.fn(),
      openNotChattableDialog: vi.fn(),
    });

    expect(close).toHaveBeenCalledOnce();
    expect(navigate).toHaveBeenCalledWith("/org");
    expect(consumeNewAgentDialogIntent()).toBe(true);
  });
});
