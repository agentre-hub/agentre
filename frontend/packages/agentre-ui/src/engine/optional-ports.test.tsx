import type React from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createInstance } from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import { describe, expect, it, vi } from "vitest";

import {
  AGENTRE_UI_NAMESPACE,
  AgentBackendsPanel,
  agentreUiResources,
  LlmProvidersPanel,
  type BackendView,
  type EngineSettingsPorts,
} from "../index";

// 账号设备清单的形状由 listAccountDevices 端口给出；浏览器 / 手机不是执行端，
// 不能出现在运行设备选项里（规格决策 9）。
const ACCOUNT_DEVICES = [
  {
    Fingerprint: "fp-build",
    Name: "build-box",
    Kind: "agentred",
    Online: true,
  },
  { Fingerprint: "fp-laptop", Name: "laptop", Kind: "desktop", Online: false },
  { Fingerprint: "fp-chrome", Name: "chrome", Kind: "browser", Online: true },
];

function createPorts(
  overrides: Partial<EngineSettingsPorts> = {},
): EngineSettingsPorts {
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
    ...overrides,
  };
}

function backendRow(overrides: Partial<BackendView>): BackendView {
  return {
    id: 1,
    syncId: "backend-1",
    name: "Claude Code",
    type: "claudecode",
    llmProviderKey: "",
    llmModelKey: "",
    llmProviderActive: true,
    agentCount: 0,
    cliByDevice: [],
    ...overrides,
  } as BackendView;
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

function renderPanel(ports: EngineSettingsPorts) {
  return renderWithTranslations(
    <AgentBackendsPanel
      ports={ports}
      renderHeader={(actions) => <div>{actions}</div>}
    />,
  );
}

async function openCreateDialog(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByText("Claude Code");
  await user.click(screen.getByTestId("agent-backend-create"));
  return screen.findByRole("dialog");
}

describe("engine settings optional ports", () => {
  it("Given a provider panel without testProvider or discoverModels ports, When a provider is rendered, Then the unavailable actions are hidden", async () => {
    renderWithTranslations(<LlmProvidersPanel ports={createPorts()} />);

    await screen.findAllByText("Anthropic");
    expect(
      screen.queryByRole("button", { name: /test connection/i }),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: /discover/i })).toBeNull();
  });

  it("Given a backend panel without scanBackends or cliPath ports, When a backend is rendered, Then scan and path editing affordances are hidden", async () => {
    renderWithTranslations(<AgentBackendsPanel ports={createPorts()} />);

    await screen.findByText("Claude Code");
    expect(screen.queryByRole("button", { name: /auto-detect/i })).toBeNull();
    expect(screen.queryByLabelText(/cli path/i)).toBeNull();
  });
});

describe("engine settings runtime device selection", () => {
  it("Given a host without localDeviceFingerprint, When the runtime device list opens, Then it offers the account's executable devices and no local item", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
      }),
    );

    const dialog = await openCreateDialog(user);
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );

    await screen.findByRole("option", { name: /build-box/ });
    expect(screen.queryByRole("option", { name: /Local/ })).toBeNull();
    expect(screen.queryByRole("option", { name: /chrome/ })).toBeNull();
  });

  it("Given an offline account device, When the runtime device list opens, Then it stays selectable and is marked offline in words", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
      }),
    );

    const dialog = await openCreateDialog(user);
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );

    const offline = await screen.findByRole("option", { name: /laptop/ });
    expect(offline).not.toHaveAttribute("aria-disabled", "true");
    expect(offline).toHaveTextContent(/offline/i);
  });

  it("Given a host that exposes localDeviceFingerprint, When the runtime device list opens, Then the local item is offered", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        localDeviceFingerprint: vi.fn().mockResolvedValue("fp-self"),
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
      }),
    );

    const dialog = await openCreateDialog(user);
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );

    await screen.findByRole("option", { name: /Local/ });
  });

  it("Given a chosen runtime device, When the type becomes OpenClaw, Then the device stays chosen and editable", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
      }),
    );

    const dialog = await openCreateDialog(user);
    const device = within(dialog).getByRole("combobox", {
      name: "Runtime Device",
    });
    await user.click(device);
    await user.click(await screen.findByRole("option", { name: /build-box/ }));
    await waitFor(() => expect(device).toHaveTextContent(/build-box/));

    await user.click(
      within(dialog).getByRole("radio", { name: /OpenClaw Gateway/ }),
    );

    expect(device).toHaveTextContent(/build-box/);
    expect(device).not.toBeDisabled();
  });
});

describe("engine settings CLI probing", () => {
  it("Given a host without resolveBackendCLIPath, When the create dialog probes, Then every CLI type reads as not probed rather than not installed", async () => {
    const user = userEvent.setup();
    renderPanel(createPorts());

    await openCreateDialog(user);

    await waitFor(() =>
      expect(document.querySelectorAll("[data-probe-state]").length).toBe(3),
    );
    for (const badge of document.querySelectorAll("[data-probe-state]")) {
      expect(badge.getAttribute("data-probe-state")).toBe("failed");
    }
  });
});

describe("engine settings auto scan", () => {
  it("Given a host without a local device, When auto scan runs, Then it scans the device the user picked", async () => {
    const user = userEvent.setup();
    const scanBackendResults = vi
      .fn()
      .mockResolvedValue([
        { name: "Codex", found: true, created: true, skipped: false },
      ]);
    renderPanel(
      createPorts({
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
        scanBackendResults,
      }),
    );

    await screen.findByText("Claude Code");
    await user.click(screen.getByRole("button", { name: /Auto Scan/i }));
    await user.click(await screen.findByRole("menuitem", { name: /laptop/ }));

    await waitFor(() =>
      expect(scanBackendResults).toHaveBeenCalledWith("fp-laptop"),
    );
    expect(await screen.findByText(/laptop/)).toBeTruthy();
  });

  it("Given a host with a local device, When auto scan runs, Then it stays a single click on this machine", async () => {
    const user = userEvent.setup();
    const scanBackendResults = vi.fn().mockResolvedValue([]);
    renderPanel(
      createPorts({
        localDeviceFingerprint: vi.fn().mockResolvedValue("fp-self"),
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
        scanBackendResults,
      }),
    );

    await screen.findByText("Claude Code");
    await user.click(screen.getByRole("button", { name: /Auto Scan/i }));

    await waitFor(() => expect(scanBackendResults).toHaveBeenCalled());
    // 本机就是那台机器，不点名任何别的设备。
    expect(scanBackendResults.mock.calls[0][0]).toBeUndefined();
    expect(screen.queryByRole("menuitem")).toBeNull();
  });
});

describe("engine settings backend row runtime location", () => {
  it("Given a host without a local device, When rows render, Then each states its device, its revocation or that no device is set", async () => {
    renderPanel(
      createPorts({
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
        listBackends: vi.fn().mockResolvedValue([
          backendRow({
            id: 1,
            syncId: "named",
            name: "Named",
            deviceId: "fp-build",
            deviceName: "build-box",
          }),
          backendRow({
            id: 2,
            syncId: "revoked",
            name: "Revoked",
            deviceId: "fp-gone",
          }),
          backendRow({ id: 3, syncId: "none", name: "NoDevice" }),
        ]),
      }),
    );

    const list = await screen.findByRole("list", {
      name: "Agent backend list",
    });
    const rows = within(list).getAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("build-box");
    expect(rows[1]).toHaveTextContent(/revoked/i);
    expect(rows[2]).toHaveTextContent(/no runtime device/i);
    expect(rows[2]).not.toHaveTextContent(/Local/);
  });

  it("Given a host with a local device, When a row has no device, Then it still reads as this machine", async () => {
    renderPanel(
      createPorts({
        localDeviceFingerprint: vi.fn().mockResolvedValue("fp-self"),
        listBackends: vi
          .fn()
          .mockResolvedValue([backendRow({ id: 3, name: "NoDevice" })]),
      }),
    );

    const list = await screen.findByRole("list", {
      name: "Agent backend list",
    });
    expect(within(list).getAllByRole("listitem")[0]).toHaveTextContent("Local");
  });
});
