import "@testing-library/jest-dom/vitest";

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SettingsNav } from "../nav";

function renderNav(backendGap: boolean, providerGap: boolean) {
  return render(
    <SettingsNav
      activePage="appearance"
      backendGap={backendGap}
      providerGap={providerGap}
      onPageChange={vi.fn()}
      syncEnabled={false}
    />,
  );
}

describe("settings nav gap", () => {
  it("缺口并入按钮可读名（点本身仍 aria-hidden）", () => {
    const { container } = renderNav(true, true);
    expect(
      screen.getByRole("button", {
        name: "Agent Backends, Some agents don't have a backend yet",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "LLM Providers, Some backends are missing an activated LLM provider",
      }),
    ).toBeInTheDocument();
    expect(
      container.querySelector('[data-gap-dot="agent-backend"]'),
    ).toHaveAttribute("aria-hidden", "true");
  });

  it("没缺口：可读名就是标签", () => {
    renderNav(false, false);
    expect(
      screen.getByRole("button", { name: "Agent Backends" }),
    ).toBeInTheDocument();
  });
});
