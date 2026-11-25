import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ChatComposer } from "./chat-composer";
import { ProviderPillTrigger } from "../engine/model-target-picker/provider-pill-trigger";

const state = {
  mode: "follow-agent" as const,
  providerLabel: "",
  providerType: "",
  modelLabel: "claude-sonnet-4-6",
  modelId: "",
  resolutionLabel: "",
  dynamic: false,
  cliLogin: false,
};

function editor(container: HTMLElement) {
  return container.querySelector(".ProseMirror") as HTMLElement;
}

describe("ChatComposer touch input", () => {
  it("touch placeholder is the short 'Reply…'; desktop keeps the default", () => {
    const touch = render(<ChatComposer inputMode="touch" onSubmit={vi.fn()} />);
    expect(
      editor(touch.container).querySelector("[data-placeholder]"),
    ).toHaveAttribute("data-placeholder", "Reply…");
    touch.unmount();
    const desktop = render(<ChatComposer onSubmit={vi.fn()} />);
    expect(
      editor(desktop.container).querySelector("[data-placeholder]"),
    ).toHaveAttribute("data-placeholder", "Type a message");
  });

  it("maxHeight caps the editor height; omitted keeps the 25vh default", () => {
    const capped = render(<ChatComposer maxHeight={210} onSubmit={vi.fn()} />);
    expect(
      capped.container
        .querySelector("form")!
        .style.getPropertyValue("--chat-composer-max-h"),
    ).toBe("210px");
    expect(editor(capped.container).className).toContain(
      "max-h-[var(--chat-composer-max-h)]",
    );
    expect(editor(capped.container).className).not.toContain("max-h-[25vh]");
    capped.unmount();
    const plain = render(<ChatComposer onSubmit={vi.fn()} />);
    expect(editor(plain.container).className).toContain("max-h-[25vh]");
    expect(
      plain.container.querySelector("form")!.getAttribute("style"),
    ).toBeNull();
  });
});

describe("ProviderPillTrigger shortName", () => {
  it("shows the short name instead of the person icon, keeps full name for screen readers", () => {
    render(<ProviderPillTrigger state={state} shortName="Sonnet" />);
    expect(screen.getByTestId("provider-pill-short-name")).toHaveTextContent(
      "Sonnet",
    );
    expect(screen.queryByTestId("follow-agent-icon")).toBeNull();
    expect(screen.getByText(/claude-sonnet-4-6/)).toBeInTheDocument();
  });

  it("without shortName the person icon stays", () => {
    render(<ProviderPillTrigger state={state} />);
    expect(screen.getByTestId("follow-agent-icon")).toBeInTheDocument();
    expect(screen.queryByTestId("provider-pill-short-name")).toBeNull();
  });
});
