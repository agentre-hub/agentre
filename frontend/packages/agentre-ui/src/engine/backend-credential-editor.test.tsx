// 共享编辑器里凭据操作的设备路由 + 状态展示 + 门控（spec「Editor behaviour on
// both hosts」）。用没有 localDeviceFingerprint 端口的宿主形状（控制台/浏览器）
// 覆盖「未选择 / 离线 / 不在账号内」三种门控，用带 localDeviceFingerprint 的宿主
// 形状（桌面）覆盖「绑定本机永远可达」与「查一次凭据状态」。
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
  type BackendView,
  type EngineSettingsPorts,
} from "../index";

const ACCOUNT_DEVICES = [
  {
    fingerprint: "fp-online",
    name: "build-box",
    kind: "agentred",
    online: true,
  },
  {
    fingerprint: "fp-offline",
    name: "laptop",
    kind: "agentred",
    online: false,
  },
];

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
    listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
    ...overrides,
  };
}

function openclawBackend(overrides: Partial<BackendView> = {}): BackendView {
  return {
    id: 1,
    syncId: "sync-openclaw-1",
    name: "Gateway box",
    type: "openclaw",
    llmProviderKey: "",
    llmModelKey: "",
    agentCount: 0,
    cliByDevice: [],
    openClawGatewayUrl: "wss://gw.example.com/ws",
    hasToken: true,
    deviceId: "fp-online",
    ...overrides,
  } as BackendView;
}

function hermesBackend(overrides: Partial<BackendView> = {}): BackendView {
  return {
    id: 2,
    syncId: "sync-hermes-1",
    name: "Gated hermes",
    type: "hermes",
    llmProviderKey: "",
    llmModelKey: "",
    agentCount: 0,
    cliByDevice: [],
    hermesUrl: "http://10.0.0.8:9119",
    hermesAuthProvider: "basic",
    hermesUserId: "stale-user",
    deviceId: "fp-online",
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

async function openEditDialog(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
) {
  await user.click(await screen.findByLabelText(`Edit ${name}`));
  return screen.findByRole("dialog");
}

describe("shared editor credential status (console-shaped host, no local device)", () => {
  it("Given an OpenClaw backend bound to an online account device, When the editor opens, Then it queries status once by type/syncId/deviceId", async () => {
    const user = userEvent.setup();
    const backendCredentialStatus = vi.fn().mockResolvedValue({
      openClawTokenSaved: true,
      hermesLoggedIn: false,
    });
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([openclawBackend()]),
        updateOpenClawBackend: vi.fn(),
        backendCredentialStatus,
      }),
    );

    await openEditDialog(user, "Gateway box");

    await waitFor(() =>
      expect(backendCredentialStatus).toHaveBeenCalledWith({
        type: "openclaw",
        syncId: "sync-openclaw-1",
        hermesUrl: undefined,
        deviceId: "fp-online",
      }),
    );
  });

  it("Given the live status says the token is not saved, When the editor renders, Then it shows the empty-token copy rather than the last-known hasToken", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listBackends: vi
          .fn()
          .mockResolvedValue([openclawBackend({ hasToken: true })]),
        updateOpenClawBackend: vi.fn(),
        backendCredentialStatus: vi.fn().mockResolvedValue({
          openClawTokenSaved: false,
          hermesLoggedIn: false,
        }),
      }),
    );

    const dialog = await openEditDialog(user, "Gateway box");

    expect(
      await within(dialog).findByPlaceholderText("Optional token"),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByPlaceholderText(/stored securely/i),
    ).toBeNull();
  });

  it("Given a Hermes backend whose synced fields say logged in, When the live status says logged out on the bound device, Then it shows the sign-in form, not the stale logged-in pill (rebinding shows fresh state)", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([hermesBackend()]),
        listHermesAuthProviders: vi.fn().mockResolvedValue([]),
        loginHermesBackend: vi.fn(),
        logoutHermesBackend: vi.fn(),
        backendCredentialStatus: vi.fn().mockResolvedValue({
          openClawTokenSaved: false,
          hermesLoggedIn: false,
        }),
      }),
    );

    const dialog = await openEditDialog(user, "Gated hermes");

    await waitFor(() =>
      expect(within(dialog).queryByText(/Signed in as stale-user/)).toBeNull(),
    );
    expect(
      within(dialog).getByRole("button", { name: "Sign in" }),
    ).toBeInTheDocument();
  });

  it("Given the bound device is not selected, When the editor opens, Then it shows the device-required note, sends no credential request, and disables login/save-token/test", async () => {
    const user = userEvent.setup();
    const backendCredentialStatus = vi.fn();
    const listHermesAuthProviders = vi.fn();
    renderPanel(
      createPorts({
        listBackends: vi
          .fn()
          .mockResolvedValue([hermesBackend({ deviceId: "" })]),
        listHermesAuthProviders,
        loginHermesBackend: vi.fn(),
        logoutHermesBackend: vi.fn(),
        backendCredentialStatus,
      }),
    );

    const dialog = await openEditDialog(user, "Gated hermes");

    expect(
      await within(dialog).findByText("Pick the device this backend runs on."),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", { name: "Sign in" }),
    ).toBeNull();
    expect(
      within(dialog).getByRole("button", { name: "Test Connection" }),
    ).toBeDisabled();
    expect(backendCredentialStatus).not.toHaveBeenCalled();
    expect(listHermesAuthProviders).not.toHaveBeenCalled();
  });

  it("Given the bound device is offline, When the editor opens, Then it shows the offline note and disables save-token", async () => {
    const user = userEvent.setup();
    const backendCredentialStatus = vi.fn();
    renderPanel(
      createPorts({
        listBackends: vi
          .fn()
          .mockResolvedValue([openclawBackend({ deviceId: "fp-offline" })]),
        updateOpenClawBackend: vi.fn(),
        backendCredentialStatus,
      }),
    );

    const dialog = await openEditDialog(user, "Gateway box");

    expect(
      await within(dialog).findByText(
        "That device is offline right now; its login status is unknown.",
      ),
    ).toBeInTheDocument();
    // 门控命中时整个 token 输入直接不摆出来（与「宿主没有这个能力端口」同一条既有
    // 语义：不可用就别给一个填了也送不出去的框），而不是摆一个禁用态的框。
    expect(within(dialog).queryByLabelText(/token/i)).toBeNull();
    expect(backendCredentialStatus).not.toHaveBeenCalled();
  });

  it("Given the bound device is no longer in the account, When the editor opens, Then it shows the unknown-device note", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listBackends: vi
          .fn()
          .mockResolvedValue([openclawBackend({ deviceId: "fp-revoked" })]),
        updateOpenClawBackend: vi.fn(),
        backendCredentialStatus: vi.fn(),
      }),
    );

    const dialog = await openEditDialog(user, "Gateway box");

    expect(
      await within(dialog).findByText(
        "That device is no longer in this account. Pick another one.",
      ),
    ).toBeInTheDocument();
  });
});
