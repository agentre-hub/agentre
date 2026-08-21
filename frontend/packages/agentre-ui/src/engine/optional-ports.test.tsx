import type React from "react";
import { render, screen } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import { describe, expect, it, vi } from "vitest";

import {
  AGENTRE_UI_NAMESPACE,
  AgentBackendsPanel,
  agentreUiResources,
  LlmProvidersPanel,
  type EngineSettingsPorts,
} from "../index";

function createPorts(): EngineSettingsPorts {
  return {
    listProviders: vi.fn().mockResolvedValue([
      {
        id: "provider-1",
        providerKey: "provider-1",
        name: "Anthropic",
        type: "anthropic",
        baseUrl: "https://api.anthropic.com",
        maskedApiKey: "••••1234",
        hasApiKey: true,
        enabled: true,
        defaultModelKey: "model-1",
      },
    ]),
    listModels: vi.fn().mockResolvedValue([]),
    createProvider: vi.fn(),
    updateProvider: vi.fn(),
    deleteProvider: vi.fn(),
    setProviderEnabled: vi.fn(),
    setModelEnabled: vi.fn(),
    createModels: vi.fn(),
    updateModel: vi.fn(),
    deleteModel: vi.fn(),
    setDefaultModel: vi.fn(),
    listBackends: vi.fn().mockResolvedValue([
      {
        id: "backend-1",
        syncId: "backend-1",
        name: "Claude Code",
        type: "claudecode",
        llmProviderKey: "provider-1",
        llmModelKey: "model-1",
        cliByDevice: [{ deviceId: "desktop", status: "path" }],
      },
    ]),
    createBackend: vi.fn(),
    updateBackend: vi.fn(),
    deleteBackend: vi.fn(),
  };
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

describe("engine settings optional ports", () => {
  it("Given a provider panel without testProvider or discoverModels ports, When a provider is rendered, Then the unavailable actions are hidden", async () => {
    renderWithTranslations(<LlmProvidersPanel ports={createPorts()} />);

    await screen.findAllByText("Anthropic");
    expect(screen.queryByRole("button", { name: /test connection/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /discover/i })).toBeNull();
  });

  it("Given a backend panel without scanBackends or cliPath ports, When a backend is rendered, Then scan and path editing affordances are hidden", async () => {
    renderWithTranslations(<AgentBackendsPanel ports={createPorts()} />);

    await screen.findByText("Claude Code");
    expect(screen.queryByRole("button", { name: /auto-detect/i })).toBeNull();
    expect(screen.queryByLabelText(/cli path/i)).toBeNull();
  });
});
