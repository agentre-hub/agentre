import "@testing-library/jest-dom/vitest";

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";

import { SettingsNav } from "../nav";

function renderNav(backendGap: number, providerGap: number) {
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
  it("缺口（含几个 Agent）并入按钮可读名（点本身仍 aria-hidden）", () => {
    const { container } = renderNav(2, 1);
    expect(
      screen.getByRole("button", {
        name: "Agent Backends, 2 agents have no backend yet",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "LLM Providers, 1 agent needs an activated LLM provider",
      }),
    ).toBeInTheDocument();
    expect(
      container.querySelector('[data-gap-dot="agent-backend"]'),
    ).toHaveAttribute("aria-hidden", "true");
  });

  // 规格 S2 的例句：「Agent 后端，有 2 个 Agent 未配置」—— 中文用全角逗号，带上数。
  it("中文可读名是规格那句：全角逗号 + Agent 数", () => {
    expect(
      i18n.t("settings.nav.agentBackendGap", {
        lng: "zh-CN",
        label: "Agent 后端",
        count: 2,
      }),
    ).toBe("Agent 后端，有 2 个 Agent 未配置");
  });

  it("没缺口：可读名就是标签", () => {
    renderNav(0, 0);
    expect(
      screen.getByRole("button", { name: "Agent Backends" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "LLM Providers" }),
    ).toBeInTheDocument();
  });
});
