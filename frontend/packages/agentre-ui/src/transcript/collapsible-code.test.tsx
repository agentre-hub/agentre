import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { CollapsibleCode } from "./collapsible-code";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("sonner", () => sonnerMocks);

const originalClipboard = navigator.clipboard;

function mockClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

afterEach(() => {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: originalClipboard,
  });
  vi.clearAllMocks();
});

describe("CollapsibleCode", () => {
  it("Given a short value, When rendered, Then it has no expand control", () => {
    render(<CollapsibleCode value="ls -la" />);
    expect(screen.getByText("ls -la")).toBeDefined();
    expect(screen.queryByRole("button", { name: "Expand all" })).toBeNull();
  });

  it("Given a long value, When rendered, Then it uses a scrollable body without expand or fade affordances", () => {
    render(
      <CollapsibleCode
        value={"line1\nline2\nline3\nline4\nline5\nline6"}
        testId="tool-content"
      />,
    );
    const body = screen.getByTestId("tool-content-body");
    expect(body).toHaveClass("max-h-64", "overflow-auto");
    expect(screen.queryByRole("button", { name: "Expand all" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Collapse" })).toBeNull();
    expect(
      document.querySelector('[aria-hidden="true"].bg-gradient-to-t'),
    ).toBeNull();
    expect(body).toHaveTextContent("line6");
  });

  it("Given a long unbroken value, When rendered, Then horizontal overflow can be scrolled", () => {
    render(<CollapsibleCode value={"x".repeat(300)} testId="tool-content" />);
    expect(screen.getByTestId("tool-content-body")).toHaveClass(
      "overflow-auto",
    );
  });

  it("copies the full value (not truncated)", async () => {
    const writeText = mockClipboard();
    const value = "line1\nline2\nline3\nline4\nline5\nline6";
    render(<CollapsibleCode value={value} />);
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(value));
  });
});
