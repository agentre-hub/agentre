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

describe("shared editor Hermes provider selection vs. the live status answer", () => {
  it("Given the credential status answers after the provider directory, When it says logged out, Then the selected provider survives and Sign in submits it", async () => {
    const user = userEvent.setup();
    const loginHermesBackend = vi
      .fn()
      .mockResolvedValue({ provider: "basic", userId: "user-9" });
    const listHermesAuthProviders = vi
      .fn()
      .mockResolvedValue([
        { name: "basic", displayName: "Basic", supportsPassword: true },
      ]);
    // 状态查询比提供方目录晚落地。「没登录」是一句关于身份的回答，不该顺手把
    // 登录表单里选好的 provider 抹掉——那一格在登出态下是输入，不是状态展示。
    const backendCredentialStatus = vi.fn().mockImplementation(
      () =>
        new Promise((resolve) => {
          setTimeout(
            () => resolve({ openClawTokenSaved: false, hermesLoggedIn: false }),
            0,
          );
        }),
    );
    renderPanel(
      createPorts({
        listBackends: vi
          .fn()
          .mockResolvedValue([hermesBackend({ hermesUserId: "" })]),
        listHermesAuthProviders,
        loginHermesBackend,
        logoutHermesBackend: vi.fn(),
        backendCredentialStatus,
      }),
    );

    const dialog = await openEditDialog(user, "Gated hermes");
    await waitFor(() => expect(listHermesAuthProviders).toHaveBeenCalled());
    await waitFor(() => expect(backendCredentialStatus).toHaveBeenCalled());

    await user.type(within(dialog).getByLabelText("Username"), "alice");
    await user.type(within(dialog).getByLabelText("Password"), "pw");
    await user.click(within(dialog).getByRole("button", { name: "Sign in" }));

    await waitFor(() =>
      expect(loginHermesBackend).toHaveBeenCalledWith(
        expect.objectContaining({ provider: "basic", username: "alice" }),
      ),
    );
  });
});

// 凭据失败的文案规范（spec「所有凭据相关错误按界面文案规范解析成中英文可读句子，
// 不直接显示协议原文」）。列表行的测试连接与编辑器里的那颗按钮走同一条判定，
// 因此结构化码在两处都必须先翻成人话再落地。
describe("readable copy for a structured OpenClaw test failure", () => {
  const notPairedResult = {
    ok: false,
    code: "OPENCLAW_NOT_PAIRED",
    message:
      "openclaw gateway RPC NOT_PAIRED: pairing required: device is not approved yet",
    latencyMs: 8,
  };

  it("Given the bound device answers NOT_PAIRED, When the row's test connection finishes, Then the flash reads the pairing sentence and never the gateway's protocol text", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([openclawBackend()]),
        updateOpenClawBackend: vi.fn(),
        backendCredentialStatus: vi.fn().mockResolvedValue({
          openClawTokenSaved: true,
          hermesLoggedIn: false,
        }),
        testBackend: vi.fn(),
        testOpenClawBackend: vi.fn().mockResolvedValue(notPairedResult),
      }),
    );

    await user.click(
      await screen.findByLabelText("Test connection for Gateway box"),
    );

    expect(
      await screen.findByText(
        /This Gateway requires device pairing before granting the requested scopes/,
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/openclaw gateway RPC/)).toBeNull();
  });
});

// V13：远端 agentred 的说明必须与本轮交付一致——token 已经能存到绑定设备上，
// 还不能在上面跑对话。
describe("the OpenClaw bound-device note", () => {
  it("Given an OpenClaw backend bound to an online agentred, When the editor opens, Then the note says the token lives on that device and the token field plus test stay usable", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([openclawBackend()]),
        updateOpenClawBackend: vi.fn(),
        backendCredentialStatus: vi.fn().mockResolvedValue({
          openClawTokenSaved: false,
          hermesLoggedIn: false,
        }),
        testOpenClawBackend: vi.fn(),
      }),
    );

    const dialog = await openEditDialog(user, "Gateway box");

    expect(
      within(dialog).queryByText(
        /until secure secret enrollment is implemented/,
      ),
    ).toBeNull();
    expect(
      await within(dialog).findByText(
        "The token is stored only on the device this backend is bound to, never on the account server. Running conversations on a remote agentred is not available yet.",
      ),
    ).toBeInTheDocument();
    expect(
      await within(dialog).findByPlaceholderText("Optional token"),
    ).toBeEnabled();
    expect(
      within(dialog).getByRole("button", { name: "Test Connection" }),
    ).toBeEnabled();
  });

  it("Given the zh-CN bundle, When the note is read, Then it states the same current truth", () => {
    const zh = agentreUiResources["zh-CN"] as Record<string, unknown>;
    const note = (
      (
        (zh.agentBackends as Record<string, unknown>).openclaw as Record<
          string,
          unknown
        >
      ).remoteUnavailable as string
    ).trim();
    expect(note).toBe(
      "Token 只保存在该后端绑定的那台设备上，不经过账号服务器。远端 agentred 上跑对话尚未支持。",
    );
  });
});
