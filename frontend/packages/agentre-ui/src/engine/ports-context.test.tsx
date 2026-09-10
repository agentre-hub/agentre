import type React from "react";
import { render, screen, waitFor } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import { describe, expect, it, vi } from "vitest";

import {
  AGENTRE_UI_NAMESPACE,
  AgentBackendsPanel,
  agentreUiResources,
  EngineSettingsPortsProvider,
  LlmProvidersPanel,
  useEngineSettingsPorts,
  type EngineSettingsPorts,
} from "../index";

function createPorts(
  overrides: Partial<EngineSettingsPorts> = {},
): EngineSettingsPorts {
  return {
    listProviders: vi.fn().mockResolvedValue([]),
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
    listBackends: vi.fn().mockResolvedValue([]),
    createBackend: vi.fn(),
    updateBackend: vi.fn(),
    deleteBackend: vi.fn(),
    ...overrides,
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

describe("engine settings ports are per-subtree", () => {
  it("Given two panels mounted at once with different ports, When each loads, Then each panel calls the ports it was given", async () => {
    // 端口过去经模块全局传递,后渲染的面板会同时决定两个面板的行为,
    // 而 React 不保证渲染顺序 —— 这条用例正是那个缺陷的判据。
    const providerPorts = createPorts();
    const backendPorts = createPorts();

    renderWithTranslations(
      <>
        <LlmProvidersPanel ports={providerPorts} />
        <AgentBackendsPanel ports={backendPorts} />
      </>,
    );

    await waitFor(() => {
      expect(providerPorts.listProviders).toHaveBeenCalled();
      expect(backendPorts.listBackends).toHaveBeenCalled();
    });
    expect(providerPorts.listBackends).not.toHaveBeenCalled();
  });

  it("Given the panels mounted in the opposite order, When each loads, Then each panel still calls the ports it was given", async () => {
    const providerPorts = createPorts();
    const backendPorts = createPorts();

    renderWithTranslations(
      <>
        <AgentBackendsPanel ports={backendPorts} />
        <LlmProvidersPanel ports={providerPorts} />
      </>,
    );

    await waitFor(() => {
      expect(providerPorts.listProviders).toHaveBeenCalled();
      expect(backendPorts.listBackends).toHaveBeenCalled();
    });
    expect(providerPorts.listBackends).not.toHaveBeenCalled();
  });
});

function PortConsumer() {
  const ports = useEngineSettingsPorts();
  return <span>{typeof ports.listProviders}</span>;
}

describe("EngineSettingsPortsProvider", () => {
  it("hands the injected ports to the engine settings tree", () => {
    render(
      <EngineSettingsPortsProvider ports={createPorts()}>
        <PortConsumer />
      </EngineSettingsPortsProvider>,
    );

    expect(screen.getByText("function")).toBeTruthy();
  });

  it("fails loudly when a consumer is mounted without a provider", () => {
    // 装配错误必须在挂载期就炸,而不是等用户点了按钮才发现没接线。
    // React 会把渲染异常打到 console.error,这里静音以免污染测试输出。
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});

    expect(() => render(<PortConsumer />)).toThrow(
      /EngineSettingsPortsProvider/,
    );

    spy.mockRestore();
  });
});
