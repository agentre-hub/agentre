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
  type BackendType,
  type BackendView,
  type EngineSettingsPorts,
} from "../index";

// 账号设备清单的形状由 listAccountDevices 端口给出；浏览器 / 手机不是执行端，
// 不能出现在运行设备选项里（规格决策 9）。
const ACCOUNT_DEVICES = [
  {
    fingerprint: "fp-build",
    name: "build-box",
    kind: "agentred",
    online: true,
  },
  { fingerprint: "fp-laptop", name: "laptop", kind: "desktop", online: false },
  { fingerprint: "fp-chrome", name: "chrome", kind: "browser", online: true },
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

describe("engine settings backend capabilities", () => {
  const browserBackendTypes: readonly BackendType[] = [
    "claudecode",
    "codex",
    "piagent",
    "openclaw",
  ];

  it("hides existing backends whose type the host does not support", async () => {
    renderPanel(
      createPorts({
        supportedBackendTypes: browserBackendTypes,
        listBackends: vi.fn().mockResolvedValue([
          backendRow({ name: "Claude Code", type: "claudecode" }),
          backendRow({
            id: 2,
            syncId: "backend-2",
            name: "Hermes",
            type: "hermes",
          }),
        ]),
      }),
    );

    expect(await screen.findByText("Claude Code")).toBeTruthy();
    expect(screen.queryByText("Hermes")).toBeNull();
  });

  it("limits pointer and arrow-key selection to the host's supported backend types", async () => {
    const user = userEvent.setup();
    renderPanel(createPorts({ supportedBackendTypes: browserBackendTypes }));
    const dialog = await openCreateDialog(user);

    expect(within(dialog).queryByRole("radio", { name: /^Hermes/ })).toBeNull();
    const pi = within(dialog).getByRole("radio", { name: /Pi Agent/ });
    await user.click(pi);
    await user.keyboard("{ArrowRight}");

    expect(
      within(dialog).getByRole("radio", { name: /OpenClaw Gateway/ }),
    ).toHaveAttribute("aria-checked", "true");
  });

  it("keeps all backend types when the host does not declare a restriction", async () => {
    const user = userEvent.setup();
    renderPanel(createPorts());
    const dialog = await openCreateDialog(user);

    expect(within(dialog).getByRole("radio", { name: /^Hermes/ })).toBeTruthy();
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

/**
 * 宿主拿不出手的字段不能摆在表单里。填得进去、保存不下来、再打开是空的，
 * 比一开始就不给这个框糟糕得多 —— 用户会以为自己填错了。
 */
describe("engine settings host-writable fields", () => {
  it("Given a host without the cliPath port, When the backend editor opens on a CLI type, Then no CLI path field or detect button is offered", async () => {
    const user = userEvent.setup();
    renderPanel(createPorts());

    const dialog = await openCreateDialog(user);

    expect(within(dialog).queryByText("CLI Path")).toBeNull();
    expect(within(dialog).queryByRole("button", { name: "Detect" })).toBeNull();
  });

  it("Given a host with the cliPath port, When the backend editor opens on a CLI type, Then the path can still be edited", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        cliPath: {
          get: vi.fn().mockResolvedValue(""),
          set: vi.fn().mockResolvedValue(undefined),
        },
      }),
    );

    const dialog = await openCreateDialog(user);

    expect(within(dialog).getByText("CLI Path")).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "Detect" })).toBeTruthy();
  });

  it("Given a host without the OpenClaw backend ports, When the OpenClaw type is selected, Then no Gateway token field is offered", async () => {
    const user = userEvent.setup();
    renderPanel(createPorts());

    const dialog = await openCreateDialog(user);
    await user.click(
      within(dialog).getByRole("radio", { name: /OpenClaw Gateway/ }),
    );

    expect(
      await within(dialog).findByLabelText(/Gateway WebSocket URL/i),
    ).toBeTruthy();
    expect(within(dialog).queryByLabelText(/token/i)).toBeNull();
  });

  it("Given a host with the OpenClaw backend ports, When the OpenClaw type is selected, Then the token can still be entered", async () => {
    const user = userEvent.setup();
    renderPanel(createPorts({ createOpenClawBackend: vi.fn() }));

    const dialog = await openCreateDialog(user);
    await user.click(
      within(dialog).getByRole("radio", { name: /OpenClaw Gateway/ }),
    );

    expect(await within(dialog).findByLabelText(/token/i)).toBeTruthy();
  });
});

/**
 * 可执行文件路径覆盖是按 (backend, device) 存的一行，不是按 backend 存一行。
 * 编辑器只在打开时取过一次值，换设备不重读就会把旧设备的路径原样提交，写进
 * 新设备那一行 —— 这条测试锚住换设备必须重读、保存必须只带上当前设备。
 */
describe("engine settings CLI path overlay by device", () => {
  it("Given an editor open on an existing backend, When the runtime device changes, Then the CLI path field shows the new device's stored path and save writes only that device's overlay", async () => {
    const user = userEvent.setup();
    const get = vi.fn(async (_backendSyncId: string, deviceId: string) => {
      if (deviceId === "fp-build") return "/usr/bin/claude-build";
      if (deviceId === "fp-laptop") return "/usr/bin/claude-laptop";
      return null;
    });
    const set = vi.fn().mockResolvedValue(undefined);
    const updateBackend = vi.fn().mockResolvedValue(
      backendRow({
        syncId: "backend-1",
        type: "claudecode",
        deviceId: "fp-laptop",
      }),
    );

    renderPanel(
      createPorts({
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
        listBackends: vi.fn().mockResolvedValue([
          backendRow({
            syncId: "backend-1",
            name: "Claude Code",
            type: "claudecode",
            deviceId: "fp-build",
          }),
        ]),
        cliPath: { get, set },
        updateBackend,
      }),
    );

    await user.click(
      await screen.findByRole("button", { name: "Edit Claude Code" }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByDisplayValue("/usr/bin/claude-build");
    expect(get).toHaveBeenCalledWith("backend-1", "fp-build");

    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(await screen.findByRole("option", { name: /laptop/ }));

    const pathField = await within(dialog).findByDisplayValue(
      "/usr/bin/claude-laptop",
    );
    expect(get).toHaveBeenCalledWith("backend-1", "fp-laptop");

    await user.clear(pathField);
    await user.type(pathField, "/opt/claude-laptop");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(set).toHaveBeenCalledWith(
        "backend-1",
        "fp-laptop",
        "/opt/claude-laptop",
      ),
    );
    expect(set).not.toHaveBeenCalledWith(
      "backend-1",
      "fp-build",
      expect.anything(),
    );
  });

  it("Given an editor open on an existing CLI backend, When only the name changes and it is saved, Then the stored CLI path overlay is not written back", async () => {
    const user = userEvent.setup();
    const get = vi.fn().mockResolvedValue("/usr/bin/claude-build");
    const set = vi.fn().mockResolvedValue(undefined);
    const updateBackend = vi.fn().mockResolvedValue(
      backendRow({
        syncId: "backend-1",
        type: "claudecode",
        deviceId: "fp-build",
      }),
    );

    renderPanel(
      createPorts({
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
        listBackends: vi.fn().mockResolvedValue([
          backendRow({
            syncId: "backend-1",
            name: "Claude Code",
            type: "claudecode",
            deviceId: "fp-build",
          }),
        ]),
        cliPath: { get, set },
        updateBackend,
      }),
    );

    await user.click(
      await screen.findByRole("button", { name: "Edit Claude Code" }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByDisplayValue("/usr/bin/claude-build");
    const name = within(dialog).getByLabelText(/name/i);
    await user.clear(name);
    await user.type(name, "Claude Renamed");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(updateBackend).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(set).not.toHaveBeenCalled();
  });

  it("Given a backend type without a CLI executable, When it is saved, Then no CLI overlay row is written", async () => {
    const user = userEvent.setup();
    const set = vi.fn().mockResolvedValue(undefined);
    renderPanel(
      createPorts({
        cliPath: { get: vi.fn().mockResolvedValue(null), set },
        createOpenClawBackend: vi
          .fn()
          .mockResolvedValue(
            backendRow({ syncId: "backend-2", type: "openclaw" }),
          ),
      }),
    );

    const dialog = await openCreateDialog(user);
    await user.click(
      within(dialog).getByRole("radio", { name: /OpenClaw Gateway/ }),
    );
    await user.type(within(dialog).getByLabelText(/name/i), "OpenClaw Local");
    await user.type(
      await within(dialog).findByLabelText(/Gateway WebSocket URL/i),
      "ws://127.0.0.1:18789",
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(within(dialog).queryByRole("dialog")).toBeNull(),
    );
    expect(set).not.toHaveBeenCalled();
  });

  it("Given a new CLI backend being created, When it is saved, Then the overlay is written under the created backend's own syncId", async () => {
    const user = userEvent.setup();
    const set = vi.fn().mockResolvedValue(undefined);
    const createBackend = vi
      .fn()
      .mockResolvedValue(
        backendRow({ syncId: "backend-new", type: "claudecode" }),
      );
    renderPanel(
      createPorts({
        cliPath: { get: vi.fn().mockResolvedValue(null), set },
        createBackend,
      }),
    );

    const dialog = await openCreateDialog(user);
    await user.type(within(dialog).getByLabelText(/name/i), "My Claude");
    await user.type(
      within(dialog).getByPlaceholderText("/usr/local/bin/claude"),
      "/usr/local/bin/claude",
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(set).toHaveBeenCalledWith(
        "backend-new",
        "",
        "/usr/local/bin/claude",
      ),
    );
  });
});

/**
 * `addIsSandbox` 是给**编辑不了 env 表**的宿主准备的口子。
 *
 * 桌面端不用它：env_json 就在它手里，一键按钮改本地 entries 再随整体保存落盘。
 * 浏览器宿主永远拿不到 env_json（agentre-server 的 R19），整表编辑器在那一侧开不了，
 * 但「补上 IS_SANDBOX=1」这一个动作它做得到——由服务端合并，浏览器只送 sync_id。
 *
 * 提示因此不能再挂在 `canEditEnvJSON` 上，否则浏览器宿主连这条路都看不到，而它恰好
 * 是最需要这条提示的一侧（远端 agentred 以 root 跑正是浏览器控制台的常态）。
 */
describe("engine settings IS_SANDBOX 一键补键", () => {
  const bypassRemoteBackend = {
    id: "backend-1",
    syncId: "backend-1",
    name: "Claude Code",
    type: "claudecode",
    llmProviderKey: "provider-1",
    llmModelKey: "model-1",
    deviceId: "fp-build",
    defaultPermissionMode: "bypassPermissions",
    cliByDevice: [],
  };

  it("宿主给了 addIsSandbox 端口时，编辑既有后端能一键补键，只送 sync_id", async () => {
    const user = userEvent.setup();
    const addIsSandbox = vi.fn().mockResolvedValue(undefined);
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([bypassRemoteBackend]),
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
        addIsSandbox,
      }),
    );

    await user.click(await screen.findByLabelText("Edit Claude Code"));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      await within(dialog).findByRole("button", { name: /Add IS_SANDBOX=1/ }),
    );

    await waitFor(() => expect(addIsSandbox).toHaveBeenCalledWith("backend-1"));
  });

  /** 补完给出确认态：浏览器读不回 env_json，这一下点击是它唯一的反馈来源。 */
  it("补键成功后按钮换成已配置", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([bypassRemoteBackend]),
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
        addIsSandbox: vi.fn().mockResolvedValue(undefined),
      }),
    );

    await user.click(await screen.findByLabelText("Edit Claude Code"));
    const dialog = await screen.findByRole("dialog");
    await user.click(
      await within(dialog).findByRole("button", { name: /Add IS_SANDBOX=1/ }),
    );

    expect(
      await within(dialog).findByText(/Configured in env_json/),
    ).toBeInTheDocument();
  });

  /** 宿主两条路都没有（既编辑不了整表、也没有这个端口）就别把按钮摆出来。 */
  it("宿主没有 addIsSandbox 端口且编辑不了 env 表时不出按钮", async () => {
    const user = userEvent.setup();
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([bypassRemoteBackend]),
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
      }),
    );

    await user.click(await screen.findByLabelText("Edit Claude Code"));
    const dialog = await screen.findByRole("dialog");

    expect(
      within(dialog).queryByRole("button", { name: /Add IS_SANDBOX=1/ }),
    ).toBeNull();
  });
});

/**
 * 保存只带「本次编辑里用户改过的字段」（规格 web 前端：未改动字段保持服务端当前值）。
 * 宿主拿 changedFields 决定 PATCH 里放哪些键；编辑器打开时那份整稿里其余的值可能
 * 早已被别的设备改掉，整份发回去就会把它们撤回。
 */
describe("engine settings backend save changedFields", () => {
  const claudeBackend = backendRow({
    syncId: "backend-1",
    name: "Claude Code",
    type: "claudecode",
    defaultPermissionMode: "acceptEdits",
    reasoningEffort: "high",
  });

  it("Given an existing backend, When only the default permission mode changes, Then the update names only config.defaultPermissionMode", async () => {
    const user = userEvent.setup();
    const updateBackend = vi.fn().mockResolvedValue(claudeBackend);
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([claudeBackend]),
        updateBackend,
      }),
    );

    await user.click(
      await screen.findByRole("button", { name: "Edit Claude Code" }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("combobox", {
        name: "Default Permission Mode",
      }),
    );
    await user.click(
      await screen.findByRole("option", { name: /plan · Read-only/ }),
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(updateBackend).toHaveBeenCalled());
    const input = updateBackend.mock.calls[0][1];
    expect(input.changedFields).toEqual(["config.defaultPermissionMode"]);
    expect(input.defaultPermissionMode).toBe("plan");
  });

  it("Given an existing backend, When the name is edited and then restored, Then the update names no field", async () => {
    const user = userEvent.setup();
    const updateBackend = vi.fn().mockResolvedValue(claudeBackend);
    renderPanel(
      createPorts({
        listBackends: vi.fn().mockResolvedValue([claudeBackend]),
        updateBackend,
      }),
    );

    await user.click(
      await screen.findByRole("button", { name: "Edit Claude Code" }),
    );
    const dialog = await screen.findByRole("dialog");
    const nameInput = within(dialog).getByDisplayValue("Claude Code");
    await user.type(nameInput, "X");
    expect(nameInput).toHaveValue("Claude CodeX");
    await user.type(nameInput, "{Backspace}");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(updateBackend).toHaveBeenCalled());
    expect(updateBackend.mock.calls[0][1].changedFields).toEqual([]);
  });

  it("Given an existing backend, When only the runtime device changes, Then the new device's stored CLI path is not reported as a change", async () => {
    const user = userEvent.setup();
    const updateBackend = vi.fn().mockResolvedValue(claudeBackend);
    renderPanel(
      createPorts({
        listAccountDevices: vi.fn().mockResolvedValue(ACCOUNT_DEVICES),
        listBackends: vi
          .fn()
          .mockResolvedValue([{ ...claudeBackend, deviceId: "fp-build" }]),
        cliPath: {
          get: vi.fn(async (_syncId: string, deviceId: string) =>
            deviceId === "fp-build"
              ? "/usr/bin/claude-build"
              : "/usr/bin/claude-laptop",
          ),
          set: vi.fn().mockResolvedValue(undefined),
        },
        updateBackend,
      }),
    );

    await user.click(
      await screen.findByRole("button", { name: "Edit Claude Code" }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByDisplayValue("/usr/bin/claude-build");
    await user.click(
      within(dialog).getByRole("combobox", { name: "Runtime Device" }),
    );
    await user.click(await screen.findByRole("option", { name: /laptop/ }));
    await within(dialog).findByDisplayValue("/usr/bin/claude-laptop");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(updateBackend).toHaveBeenCalled());
    expect(updateBackend.mock.calls[0][1].changedFields).toEqual(["deviceId"]);
  });

  it("Given a new backend, When it is created, Then changedFields names every populated field", async () => {
    const user = userEvent.setup();
    const createBackend = vi
      .fn()
      .mockResolvedValue(backendRow({ syncId: "backend-new" }));
    renderPanel(createPorts({ createBackend }));

    const dialog = await openCreateDialog(user);
    await user.type(within(dialog).getByLabelText(/name/i), "My Claude");
    await user.click(
      within(dialog).getByRole("combobox", {
        name: "Default Permission Mode",
      }),
    );
    await user.click(
      await screen.findByRole("option", { name: /plan · Read-only/ }),
    );
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(createBackend).toHaveBeenCalled());
    expect(createBackend.mock.calls[0][0].changedFields).toEqual([
      "type",
      "name",
      "config.defaultPermissionMode",
    ]);
  });
});

/**
 * 宿主在账号通道提示同步变化时递增 refreshSignal：面板经既有端口重拉清单，
 * 已打开的编辑弹窗里用户敲到一半的内容不能被冲掉。
 */
describe("engine settings refreshSignal", () => {
  function renderWithSignal(build: (signal: number) => React.ReactElement) {
    const i18n = createInstance();
    void i18n.use(initReactI18next).init({
      lng: "en",
      fallbackLng: "en",
      resources: { en: { [AGENTRE_UI_NAMESPACE]: agentreUiResources.en } },
      react: { useSuspense: false },
    });
    const view = render(
      <I18nextProvider i18n={i18n}>{build(1)}</I18nextProvider>,
    );
    return (signal: number) =>
      view.rerender(
        <I18nextProvider i18n={i18n}>{build(signal)}</I18nextProvider>,
      );
  }

  it("Given an open backend editor with typed values, When refreshSignal changes, Then the list is fetched again and the typed name stays", async () => {
    const user = userEvent.setup();
    const ports = createPorts();
    const setSignal = renderWithSignal((signal) => (
      <AgentBackendsPanel
        ports={ports}
        refreshSignal={signal}
        renderHeader={(actions) => <div>{actions}</div>}
      />
    ));

    const dialog = await openCreateDialog(user);
    await user.type(within(dialog).getByLabelText(/name/i), "Half typed");
    expect(ports.listBackends).toHaveBeenCalledTimes(1);

    setSignal(2);

    await waitFor(() => expect(ports.listBackends).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(ports.listProviders).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/name/i)).toHaveValue("Half typed");
  });

  it("Given an open provider form with typed values, When refreshSignal changes, Then providers are fetched again and the typed name stays", async () => {
    const user = userEvent.setup();
    const ports = createPorts();
    const setSignal = renderWithSignal((signal) => (
      <LlmProvidersPanel
        ports={ports}
        refreshSignal={signal}
        renderHeader={(actions) => <div>{actions}</div>}
      />
    ));

    await screen.findAllByText("Anthropic");
    await user.click(screen.getByRole("button", { name: "New Provider" }));
    const dialog = await screen.findByRole("dialog");
    const nameInput = within(dialog).getByPlaceholderText(
      "Example: production / local Ollama",
    );
    await user.type(nameInput, "Half typed");
    expect(ports.listProviders).toHaveBeenCalledTimes(1);

    setSignal(2);

    await waitFor(() => expect(ports.listProviders).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(nameInput).toHaveValue("Half typed");
  });
});
