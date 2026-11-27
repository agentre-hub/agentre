import type React from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createInstance } from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import { describe, expect, it, vi } from "vitest";

import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "../i18n";
import { TouchProvider } from "../ui/touch";
import { LlmProvidersPanel } from "./llm-providers";
import type { EngineSettingsPorts } from "./ports";

const PROVIDERS = [
  {
    id: 7,
    providerKey: "p-7",
    name: "Anthropic",
    type: "anthropic",
    baseUrl: "https://api.anthropic.com",
    maskedApiKey: "••••1234",
    hasApiKey: true,
    enabled: true,
    defaultModelKey: "m-1",
  },
  {
    id: 8,
    providerKey: "p-8",
    name: "Local Ollama",
    type: "ollama",
    baseUrl: "http://localhost:11434",
    hasApiKey: false,
    enabled: true,
    defaultModelKey: "",
  },
];

const MODELS = [1, 2].map((n) => ({
  id: n,
  providerId: 7,
  providerKey: "p-7",
  modelKey: `m-${n}`,
  modelId: `claude-${n}`,
  name: `Claude ${n}`,
  contextWindow: 200000,
  maxOutput: 8000,
  enabled: true,
}));

function createPorts(
  overrides: Partial<EngineSettingsPorts> = {},
): EngineSettingsPorts {
  return {
    listProviders: vi.fn().mockResolvedValue(PROVIDERS),
    listModels: vi.fn().mockResolvedValue(MODELS),
    createProvider: vi.fn(),
    updateProvider: vi.fn(),
    deleteProvider: vi.fn(),
    setProviderEnabled: vi.fn(),
    setModelEnabled: vi.fn(),
    createModels: vi.fn(),
    updateModel: vi.fn(),
    deleteModel: vi.fn(),
    setDefaultModel: vi.fn(),
    listBackends: vi.fn().mockResolvedValue([]),
    createBackend: vi.fn(),
    updateBackend: vi.fn(),
    deleteBackend: vi.fn(),
    testProvider: vi.fn().mockResolvedValue({ ok: true, durationMs: 12 }),
    ...overrides,
  } as EngineSettingsPorts;
}

function renderWithTranslations(node: React.ReactNode) {
  const i18n = createInstance();
  void i18n.use(initReactI18next).init({
    lng: "en",
    fallbackLng: "en",
    resources: { en: { [AGENTRE_UI_NAMESPACE]: agentreUiResources.en } },
    react: { useSuspense: false },
  });
  return render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>);
}

describe("LlmProvidersPanel layered mode", () => {
  it("Given layer=detail, When providers load, Then the page header gets no Add-provider action but the selected provider's name as its title", async () => {
    const renderHeader = vi.fn(
      (_actions: React.ReactNode, _ctx?: { providerName?: string }) => null,
    );
    renderWithTranslations(
      <LlmProvidersPanel
        ports={createPorts()}
        layer="detail"
        selectedId={7}
        onSelect={vi.fn()}
        renderHeader={renderHeader}
      />,
    );

    await screen.findByRole("region");
    const [actions, ctx] = renderHeader.mock.calls.at(-1)!;
    expect(actions).toBeNull();
    expect(ctx).toEqual({ providerName: "Anthropic" });
  });

  it("Given layer=list, When providers load, Then the page header still gets the Add-provider action", async () => {
    const renderHeader = vi.fn(
      (actions: React.ReactNode, _ctx?: { providerName?: string }) => actions,
    );
    renderWithTranslations(
      <LlmProvidersPanel
        ports={createPorts()}
        layer="list"
        selectedId={null}
        onSelect={vi.fn()}
        renderHeader={renderHeader}
      />,
    );

    expect(
      await screen.findByRole("button", { name: /New Provider/i }),
    ).toBeInTheDocument();
  });

  it("Given layer=list, When providers load, Then only the list renders and picking one reports its id without opening a detail", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const ports = createPorts();
    renderWithTranslations(
      <LlmProvidersPanel
        ports={ports}
        layer="list"
        selectedId={null}
        onSelect={onSelect}
      />,
    );

    await user.click(
      await screen.findByRole("button", { name: /Local Ollama/ }),
    );

    expect(onSelect).toHaveBeenCalledWith(8);
    expect(screen.queryByRole("region")).toBeNull();
    expect(ports.listModels).not.toHaveBeenCalled();
    // 受控：宿主没改 selectedId，列表不自行高亮
    expect(
      screen.getByRole("button", { name: /Local Ollama/ }),
    ).toHaveAttribute("aria-current", "false");
  });

  it("Given layer=detail with a selectedId, When it renders, Then connection info is one row each, models are one row each with the switch on the right, and no table", async () => {
    const ports = createPorts();
    renderWithTranslations(
      <LlmProvidersPanel
        ports={ports}
        layer="detail"
        selectedId={7}
        onSelect={vi.fn()}
      />,
    );

    const region = await screen.findByRole("region");
    expect(screen.queryByRole("complementary")).toBeNull();
    expect(screen.queryByRole("table")).toBeNull();

    const info = within(region).getByRole("list", { name: "Connection" });
    const rows = within(info).getAllByRole("listitem");
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("https://api.anthropic.com"),
      expect.stringContaining("••••1234"),
      expect.stringContaining("Claude 1"),
      expect.stringContaining("—"),
    ]);

    const models = await within(region).findByRole("list", { name: "Models" });
    const modelRows = within(models).getAllByRole("listitem");
    expect(modelRows).toHaveLength(2);
    expect(modelRows[0]).toHaveTextContent("Claude 1");
    const sw = within(modelRows[0]).getByRole("switch");
    expect(modelRows[0].lastElementChild?.contains(sw)).toBe(true);
    expect(modelRows[0].className).not.toMatch(/min-w-\[/);
  });

  it("Given layer=detail, When Test connection is pressed, Then the existing testProvider port runs for that provider", async () => {
    const user = userEvent.setup();
    const ports = createPorts();
    renderWithTranslations(
      <LlmProvidersPanel
        ports={ports}
        layer="detail"
        selectedId={7}
        onSelect={vi.fn()}
      />,
    );

    await user.click(
      await screen.findByRole("button", { name: /Test Anthropic/ }),
    );

    await waitFor(() => expect(ports.testProvider).toHaveBeenCalled());
    expect(vi.mocked(ports.testProvider!).mock.calls[0][0]).toBe("p-7");
  });

  it("Given a controlled selection that disappears from the catalog, When providers reload, Then the host is told to clear it", async () => {
    const onSelect = vi.fn();
    renderWithTranslations(
      <LlmProvidersPanel
        ports={createPorts()}
        layer="detail"
        selectedId={99}
        onSelect={onSelect}
      />,
    );
    await waitFor(() => expect(onSelect).toHaveBeenCalledWith(null));
  });

  it("Given no layer prop, When it renders, Then list and workspace sit side by side as before with the first provider selected", async () => {
    renderWithTranslations(<LlmProvidersPanel ports={createPorts()} />);
    expect(await screen.findByRole("region")).toBeInTheDocument();
    expect(screen.getByRole("complementary")).toBeInTheDocument();
    expect(await screen.findByRole("table")).toBeInTheDocument();
  });
});

describe("W9 供应商清单读失败", () => {
  const failing = () =>
    createPorts({
      listProviders: vi
        .fn()
        .mockRejectedValueOnce(new Error("request timed out after 15000ms"))
        .mockResolvedValue(PROVIDERS),
    });

  it("Given the list fails to load, When it renders, Then no empty state invites a duplicate; an in-place notice with Retry shows, and Retry loads the list", async () => {
    const user = userEvent.setup();
    const ports = failing();
    renderWithTranslations(
      <TouchProvider>
        <LlmProvidersPanel ports={ports} />
      </TouchProvider>,
    );

    const notice = await screen.findByTestId("provider-load-failure");
    expect(notice).toHaveTextContent("LLM providers didn't load");
    // 读失败只说本地化的人话，不把「request timed out after …」原文贴在 flash 里。
    expect(document.body.textContent).not.toMatch(/timed out after|15000/);
    expect(screen.queryByText("No LLM providers configured")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Add First Provider" }),
    ).toBeNull();

    await user.click(within(notice).getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("Local Ollama")).toBeInTheDocument();
    expect(screen.queryByTestId("provider-load-failure")).toBeNull();
    expect(ports.listProviders).toHaveBeenCalledTimes(2);
  });

  it("Given a genuinely empty account, When it renders, Then the empty state still shows (no failure notice)", async () => {
    renderWithTranslations(
      <LlmProvidersPanel
        ports={createPorts({ listProviders: vi.fn().mockResolvedValue([]) })}
      />,
    );
    expect(
      await screen.findByText("No LLM providers configured"),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("provider-load-failure")).toBeNull();
  });

  it("Given the touch tier, When the notice renders, Then Retry is a 44px target; desktop (no touch) keeps the empty state and the raw-error flash", async () => {
    const { unmount } = renderWithTranslations(
      <LlmProvidersPanel ports={failing()} />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Load failed: request timed out after 15000ms",
    );
    expect(screen.getByText("No LLM providers configured")).toBeInTheDocument();
    expect(screen.queryByTestId("provider-load-failure")).toBeNull();
    unmount();
    renderWithTranslations(
      <TouchProvider>
        <LlmProvidersPanel ports={failing()} />
      </TouchProvider>,
    );
    expect(
      within(await screen.findByTestId("provider-load-failure")).getByRole(
        "button",
        { name: "Retry" },
      ),
    ).toHaveClass("min-h-11");
  });
});

describe("F10 / F22 触屏档（供应商）", () => {
  it("触屏：供应商与模型开关各有 44px 点按区；桌面没有", async () => {
    const { unmount } = renderWithTranslations(
      <LlmProvidersPanel
        ports={createPorts()}
        layer="detail"
        selectedId={7}
        onSelect={vi.fn()}
      />,
    );
    await screen.findByRole("list", { name: "Models" });
    const desktop = screen.getAllByRole("switch");
    expect(desktop.length).toBeGreaterThan(1);
    for (const sw of desktop) expect(sw).not.toHaveClass("before:absolute");
    unmount();

    renderWithTranslations(
      <TouchProvider>
        <LlmProvidersPanel
          ports={createPorts()}
          layer="detail"
          selectedId={7}
          onSelect={vi.fn()}
        />
      </TouchProvider>,
    );
    await screen.findByRole("list", { name: "Models" });
    const touch = screen.getAllByRole("switch");
    expect(touch.length).toBe(desktop.length);
    for (const sw of touch) {
      expect(sw).toHaveClass(
        "relative",
        "before:absolute",
        "before:-inset-y-3.5",
      );
    }
  });

  it("触屏：供应商组头不再是等宽大写的「ANTHROPIC」；桌面保持", async () => {
    const { unmount } = renderWithTranslations(
      <LlmProvidersPanel
        ports={createPorts()}
        layer="list"
        selectedId={null}
        onSelect={vi.fn()}
      />,
    );
    const header = await screen.findByText("Anthropic", { selector: "div" });
    expect(header).toHaveClass("font-mono", "uppercase");
    unmount();
    renderWithTranslations(
      <TouchProvider>
        <LlmProvidersPanel
          ports={createPorts()}
          layer="list"
          selectedId={null}
          onSelect={vi.fn()}
        />
      </TouchProvider>,
    );
    const human = await screen.findByText("Anthropic", { selector: "div" });
    expect(human).not.toHaveClass("font-mono");
    expect(human).not.toHaveClass("uppercase");
  });
});
