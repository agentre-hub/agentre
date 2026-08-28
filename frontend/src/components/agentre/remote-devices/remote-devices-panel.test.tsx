import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

const ZERO_SERVER_STATE = {
  ID: 1,
  ServerURL: "",
  DeviceID: 0,
  DeviceFingerprint: "",
  ServerUserID: 0,
  KeychainAccount: "",
  Updatetime: 0,
};

const LOGGED_IN_SERVER_STATE = {
  ID: 1,
  ServerURL: "https://hub.example.com",
  DeviceID: 7,
  DeviceFingerprint: "sha256:abc",
  ServerUserID: 42,
  KeychainAccount: "agentre.server.refresh_token",
  Updatetime: 1_700_000_000_000,
};

vi.mock("../../../../wailsjs/go/app/App", () => ({
  RemoteDeviceList: vi.fn().mockResolvedValue([]),
  RemoteDeviceAdd: vi.fn(),
  RemoteDeviceRemove: vi.fn(),
  RemoteDeviceUpdateTLS: vi.fn(),
  RemoteDeviceRefresh: vi.fn(),
  RemoteDeviceRename: vi.fn(),
  // 默认未登录:账号来源 unknown。R15 合并用例在测试里单独覆盖成已登录。
  ServerListDevices: vi.fn().mockRejectedValue(new Error("not logged in")),
  ServerGetState: vi.fn(),
  ServerCheckURL: vi.fn(),
  ServerStartLogin: vi.fn(),
  ServerPollLoginToken: vi.fn(),
  ServerCancelLogin: vi.fn(),
  ServerLogout: vi.fn(),
  ServerOffline: vi.fn().mockResolvedValue(false),
  // 账号清单里 kind=desktop 的行(R19)展开时才会调它;这里只为让模块解析得到。
  PeerListSessions: vi.fn().mockResolvedValue({ sessions: [] }),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(() => vi.fn()),
  BrowserOpenURL: vi.fn(),
}));

import {
  RemoteDeviceList,
  RemoteDeviceAdd,
  RemoteDeviceRemove,
  RemoteDeviceRename,
  ServerListDevices,
  ServerGetState,
  ServerCheckURL,
  ServerStartLogin,
  ServerPollLoginToken,
  ServerLogout,
  ServerOffline,
} from "../../../../wailsjs/go/app/App";
import { RemoteDevicesPanel } from "./remote-devices-panel";
import type { DeviceView } from "./use-remote-devices";

const mockList = RemoteDeviceList as unknown as ReturnType<typeof vi.fn>;
const mockAdd = RemoteDeviceAdd as unknown as ReturnType<typeof vi.fn>;
const mockRemove = RemoteDeviceRemove as unknown as ReturnType<typeof vi.fn>;
const mockRename = RemoteDeviceRename as unknown as ReturnType<typeof vi.fn>;
const mockServerList = ServerListDevices as unknown as ReturnType<typeof vi.fn>;
const mockGetState = ServerGetState as unknown as ReturnType<typeof vi.fn>;
const mockCheckURL = ServerCheckURL as unknown as ReturnType<typeof vi.fn>;
const mockStartLogin = ServerStartLogin as unknown as ReturnType<typeof vi.fn>;
const mockPollLoginToken = ServerPollLoginToken as unknown as ReturnType<
  typeof vi.fn
>;
const mockLogout = ServerLogout as unknown as ReturnType<typeof vi.fn>;
const mockOffline = ServerOffline as unknown as ReturnType<typeof vi.fn>;

describe("RemoteDevicesPanel", () => {
  beforeEach(() => {
    mockList.mockReset();
    mockAdd.mockReset();
    mockServerList.mockReset();
    mockServerList.mockRejectedValue(new Error("not logged in"));
    mockGetState.mockReset();
    mockGetState.mockResolvedValue(ZERO_SERVER_STATE);
    mockCheckURL.mockReset();
    mockCheckURL.mockResolvedValue("0.3.0");
    mockStartLogin.mockReset();
    mockPollLoginToken.mockReset();
    mockLogout.mockReset();
    mockLogout.mockResolvedValue(undefined);
    mockOffline.mockReset();
    mockOffline.mockResolvedValue(false);
  });

  it("shows an accessible loading state instead of a blank page", () => {
    mockList.mockReturnValueOnce(new Promise(() => {}));

    render(<RemoteDevicesPanel />);

    expect(
      screen.getByRole("status", { name: "Loading remote devices" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Install agentred" }),
    ).not.toBeInTheDocument();
  });

  it("shows an initial load error instead of the onboarding and retries", async () => {
    const user = userEvent.setup();
    mockList.mockRejectedValueOnce(new Error("list unavailable"));

    render(<RemoteDevicesPanel />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Couldn't load remote devices");
    expect(alert).toHaveClass("border-status-error/40", "bg-destructive-soft");
    expect(
      screen.queryByRole("heading", { name: "Install agentred" }),
    ).not.toBeInTheDocument();

    mockList.mockResolvedValueOnce([]);
    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(
      await screen.findByRole("heading", { name: "Install agentred" }),
    ).toBeInTheDocument();
    expect(mockList).toHaveBeenCalledTimes(2);
  });

  it("shows the three-step agentred onboarding when the ready device list is empty", async () => {
    mockList.mockResolvedValueOnce([]);

    render(<RemoteDevicesPanel />);

    expect(
      await screen.findByRole("heading", { name: "Install agentred" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Start the remote service")).toBeInTheDocument();
    expect(screen.getByText("Pair and verify")).toBeInTheDocument();
    expect(
      screen.getByText(
        "curl -fsSL https://github.com/agentre-hub/agentre/releases/latest/download/install.sh | sh",
      ),
    ).toHaveAttribute("data-selectable-text", "true");
    // 零设备:引导本身就是这一页,没有可回退的地方 —— 不给收起控件,
    // 也不再同屏并列一个「添加 agentred」按钮(它的唯一职责是打开引导)。
    expect(
      screen.queryByRole("button", { name: "Collapse guide" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add agentred" }),
    ).not.toBeInTheDocument();
  });

  // 决策 1/3:第 N 台从唯一入口召唤出同一份引导,列表保持可见,可收起。
  it("summons the same guide from the single entry point once devices exist", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValueOnce([
      {
        id: 1,
        name: "linux-srv",
        url: "ws://linux-srv.local:7456/rpc",
        tlsMode: "default",
        online: true,
        lastSeenAt: Date.now(),
      },
    ] as Partial<DeviceView>[]);

    render(<RemoteDevicesPanel />);
    await screen.findByTestId("device-row");

    expect(
      screen.queryByRole("heading", { name: "Install agentred" }),
    ).not.toBeInTheDocument();
    // 全页只剩一个添加入口:列表底部那个「+ 继续添加 agentred(LAN)」已删除。
    expect(
      screen.getAllByRole("button", { name: "Add agentred" }),
    ).toHaveLength(1);
    // 这一轮把 remoteDevices.actions.continueAddLan 从两份 locale 里一并删掉了,所以
    // 按钮若被重新加回 JSX,渲染出来的是**原始 key**而不是译文;译文断言只在「key 也
    // 被加回来」时才可能命中,单靠它挡不住另一半。同 lanAll 那条,两边都要。
    expect(
      screen.queryByRole("button", {
        name: "remoteDevices.actions.continueAddLan",
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Add another agentred/ }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add agentred" }));

    // 与零设备时同形:第 1 步的安装命令一字不差。
    expect(
      screen.getByRole("heading", { name: "Install agentred" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "curl -fsSL https://github.com/agentre-hub/agentre/releases/latest/download/install.sh | sh",
      ),
    ).toHaveAttribute("data-selectable-text", "true");
    expect(screen.getByTestId("device-row")).toBeInTheDocument();
    // 引导开着就不该再有第二个打开它的入口。
    expect(
      screen.queryByRole("button", { name: "Add agentred" }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Collapse guide" }));

    expect(
      screen.queryByRole("heading", { name: "Install agentred" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add agentred" }),
    ).toBeInTheDocument();
  });

  // 账号下的桌面端(R19)也是页面上实打实的一行。「零设备」这个判断此前只数 LAN
  // 配对行,于是登录后一屏设备摆在那里、引导却钉死在上面且没有收起控件。
  it("summons the guide from the entry point when the only devices are other desktops of the account", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValue([]);
    mockGetState.mockResolvedValue(LOGGED_IN_SERVER_STATE);
    mockServerList.mockResolvedValue([
      {
        id: 11,
        name: "studio-mac",
        kind: "desktop",
        platform: "darwin",
        version: "0.3.0",
        fingerprint: "fp-desktop-2",
        lastSeenAt: 1_700_000_000_000,
        status: 1,
        online: true,
        isThisDevice: false,
      },
    ]);

    render(
      <MemoryRouter>
        <RemoteDevicesPanel />
      </MemoryRouter>,
    );
    await screen.findByTestId("desktop-device-row");

    expect(
      screen.queryByRole("heading", { name: "Install agentred" }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add agentred" }));

    expect(
      screen.getByRole("heading", { name: "Install agentred" }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("desktop-device-row")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Collapse guide" }),
    ).toBeInTheDocument();
  });

  // 本机自己那一行也是一行:页面不空白,引导就不自作主张展开。
  it("stays collapsed when this desktop is the only listed device", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValue([]);
    mockGetState.mockResolvedValue(LOGGED_IN_SERVER_STATE);
    mockServerList.mockResolvedValue([
      {
        id: 7,
        name: "this-mac",
        kind: "desktop",
        platform: "darwin",
        version: "0.3.0",
        fingerprint: "sha256:abc",
        lastSeenAt: 1_700_000_000_000,
        status: 1,
        online: true,
        isThisDevice: true,
      },
    ]);

    render(
      <MemoryRouter>
        <RemoteDevicesPanel />
      </MemoryRouter>,
    );
    await screen.findByTestId("desktop-device-row");

    expect(
      screen.queryByRole("heading", { name: "Install agentred" }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add agentred" }));

    expect(
      screen.getByRole("heading", { name: "Install agentred" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Collapse guide" }),
    ).toBeInTheDocument();
  });

  // 决策 7:衔接提示不再只有第一台能看到。
  it("shows the Agent Backends follow-up after pairing a further device too", async () => {
    const user = userEvent.setup();
    const onOpenAgentBackends = vi.fn();
    const first = {
      id: 1,
      name: "linux-srv",
      url: "ws://linux-srv.local:7456/rpc",
      tlsMode: "default",
      online: true,
      lastSeenAt: Date.now(),
    } as Partial<DeviceView>;
    mockList.mockResolvedValueOnce([first] as Partial<DeviceView>[]);
    mockList.mockResolvedValueOnce([
      first,
      {
        id: 2,
        name: "build-box",
        url: "ws://build-box.local:7456/rpc",
        tlsMode: "default",
        online: true,
        lastSeenAt: Date.now(),
      },
    ] as Partial<DeviceView>[]);
    mockAdd.mockResolvedValueOnce(undefined);

    render(<RemoteDevicesPanel onOpenAgentBackends={onOpenAgentBackends} />);
    await screen.findByTestId("device-row");

    await user.click(screen.getByRole("button", { name: "Add agentred" }));
    await user.click(screen.getByRole("button", { name: "Installed, next" }));
    await user.click(
      screen.getByRole("button", { name: "Service is running" }),
    );
    await user.type(
      screen.getByLabelText("Address"),
      "ws://build-box.local:7456/rpc",
    );
    await user.type(screen.getByLabelText("Pairing Code"), "ABC2DE");
    await user.click(screen.getByRole("button", { name: "Pair and verify" }));

    await waitFor(() =>
      expect(screen.getAllByTestId("device-row")).toHaveLength(2),
    );
    // 配对成功后引导收起,唯一入口回到页头。
    expect(
      screen.queryByRole("heading", { name: "Connect this remote machine" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add agentred" }),
    ).toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Configure Agent Backends" }),
    );
    expect(onOpenAgentBackends).toHaveBeenCalledOnce();
  });

  // 失败不算「配对成功」:引导不收起、输入不丢、也不冒出衔接提示。
  it("keeps the summoned guide open on step 3 when pairing the further device fails", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValueOnce([
      {
        id: 1,
        name: "linux-srv",
        url: "ws://linux-srv.local:7456/rpc",
        tlsMode: "default",
        online: true,
        lastSeenAt: Date.now(),
      },
    ] as Partial<DeviceView>[]);
    mockAdd.mockRejectedValueOnce(new Error("Pairing code expired"));

    render(<RemoteDevicesPanel />);
    await screen.findByTestId("device-row");

    await user.click(screen.getByRole("button", { name: "Add agentred" }));
    await user.click(screen.getByRole("button", { name: "Installed, next" }));
    await user.click(
      screen.getByRole("button", { name: "Service is running" }),
    );
    await user.type(
      screen.getByLabelText("Address"),
      "ws://build-box.local:7456/rpc",
    );
    await user.type(screen.getByLabelText("Pairing Code"), "abc2de");
    await user.click(screen.getByRole("button", { name: "Pair and verify" }));

    expect(await screen.findByText("Pairing code expired")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Connect this remote machine" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Address")).toHaveValue(
      "ws://build-box.local:7456/rpc",
    );
    expect(screen.getByLabelText("Pairing Code")).toHaveValue("ABC2DE");
    expect(
      screen.queryByRole("button", { name: "Configure Agent Backends" }),
    ).not.toBeInTheDocument();
  });

  // 收起会把整份引导连同第 3 步的表单一起卸载,而失败原因只存在于那张表单里 ——
  // 提交在途时放行收起,配对失败就会被无声吞掉:设备没加上,用户却什么也看不到。
  it("keeps a mid-submit pairing failure visible even if the user tries to collapse the guide", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValueOnce([
      {
        id: 1,
        name: "linux-srv",
        url: "ws://linux-srv.local:7456/rpc",
        tlsMode: "default",
        online: true,
        lastSeenAt: Date.now(),
      },
    ] as Partial<DeviceView>[]);
    let rejectAdd: (reason: Error) => void = () => {};
    mockAdd.mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          rejectAdd = reject;
        }),
    );

    render(<RemoteDevicesPanel />);
    await screen.findByTestId("device-row");

    await user.click(screen.getByRole("button", { name: "Add agentred" }));
    await user.click(screen.getByRole("button", { name: "Installed, next" }));
    await user.click(
      screen.getByRole("button", { name: "Service is running" }),
    );
    await user.type(
      screen.getByLabelText("Address"),
      "ws://build-box.local:7456/rpc",
    );
    await user.type(screen.getByLabelText("Pairing Code"), "ABC2DE");
    await user.click(screen.getByRole("button", { name: "Pair and verify" }));

    await user.click(screen.getByRole("button", { name: "Collapse guide" }));

    rejectAdd(new Error("Pairing code expired"));

    expect(await screen.findByText("Pairing code expired")).toBeInTheDocument();
    expect(screen.getByLabelText("Address")).toHaveValue(
      "ws://build-box.local:7456/rpc",
    );
  });

  it("switches installer and service commands through the approved three-step flow", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValueOnce([]);

    render(<RemoteDevicesPanel />);
    await screen.findByRole("heading", { name: "Install agentred" });

    await user.click(screen.getByRole("radio", { name: "macOS" }));
    expect(
      screen.getByText(
        "curl -fsSL https://github.com/agentre-hub/agentre/releases/latest/download/install.sh | sh",
      ),
    ).toHaveAttribute("data-selectable-text", "true");

    await user.click(screen.getByRole("radio", { name: "Windows" }));
    expect(
      screen.getByText(
        "irm https://github.com/agentre-hub/agentre/releases/latest/download/install.ps1 | iex",
      ),
    ).toHaveAttribute("data-selectable-text", "true");

    await user.click(screen.getByRole("button", { name: "Installed, next" }));
    expect(
      screen.getByText("agentred service install --start"),
    ).toHaveAttribute("data-selectable-text", "true");
    expect(screen.getByText("Daemon running")).toBeInTheDocument();

    await user.click(
      screen.getByRole("radio", { name: "Temporary foreground" }),
    );
    expect(screen.getByText("agentred run")).toHaveAttribute(
      "data-selectable-text",
      "true",
    );

    await user.click(
      screen.getByRole("button", { name: "Service is running" }),
    );
    expect(
      screen.getByRole("heading", { name: "Connect this remote machine" }),
    ).toBeInTheDocument();
    expect(screen.getByText("agentred pair")).toHaveAttribute(
      "data-selectable-text",
      "true",
    );
  });

  it("keeps pairing input and shows the concrete error when onboarding pairing fails", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValueOnce([]);
    mockAdd.mockRejectedValueOnce(new Error("Pairing code expired"));

    render(<RemoteDevicesPanel />);
    await screen.findByRole("heading", { name: "Install agentred" });
    await user.click(screen.getByRole("button", { name: "Installed, next" }));
    await user.click(
      screen.getByRole("button", { name: "Service is running" }),
    );
    await user.type(
      screen.getByLabelText("Address"),
      "ws://linux-srv.local:7456/rpc",
    );
    await user.type(screen.getByLabelText("Pairing Code"), "abc2de");
    await user.click(screen.getByRole("button", { name: "Pair and verify" }));

    expect(await screen.findByText("Pairing code expired")).toBeInTheDocument();
    expect(screen.getByLabelText("Address")).toHaveValue(
      "ws://linux-srv.local:7456/rpc",
    );
    expect(screen.getByLabelText("Pairing Code")).toHaveValue("ABC2DE");
  });

  it("keeps the device list and opens Agent Backends after onboarding pairing succeeds", async () => {
    const user = userEvent.setup();
    const onOpenAgentBackends = vi.fn();
    mockList.mockResolvedValueOnce([]).mockResolvedValueOnce([
      {
        id: 1,
        name: "linux-srv",
        url: "ws://linux-srv.local:7456/rpc",
        tlsMode: "default",
        online: true,
        lastSeenAt: Date.now(),
      },
    ] as Partial<DeviceView>[]);
    mockAdd.mockResolvedValueOnce(undefined);

    render(<RemoteDevicesPanel onOpenAgentBackends={onOpenAgentBackends} />);
    await screen.findByRole("heading", { name: "Install agentred" });
    await user.click(screen.getByRole("button", { name: "Installed, next" }));
    await user.click(
      screen.getByRole("button", { name: "Service is running" }),
    );
    await user.type(
      screen.getByLabelText("Address"),
      "ws://linux-srv.local:7456/rpc",
    );
    await user.type(screen.getByLabelText("Pairing Code"), "ABC2DE");
    await user.click(screen.getByRole("button", { name: "Pair and verify" }));

    expect(await screen.findByTestId("device-row")).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Configure Agent Backends" }),
    );
    expect(onOpenAgentBackends).toHaveBeenCalledOnce();
  });

  it("renders a row per device + counters", async () => {
    mockList.mockResolvedValueOnce([
      {
        id: 1,
        name: "mac",
        url: "ws://h1/rpc",
        tlsMode: "default",
        online: true,
        lastSeenAt: Date.now(),
      },
      {
        id: 2,
        name: "pi",
        url: "ws://h2/rpc",
        tlsMode: "default",
        online: false,
        lastSeenAt: 0,
      },
    ] as Partial<DeviceView>[]);
    render(<RemoteDevicesPanel />);
    await waitFor(() =>
      expect(screen.getAllByTestId("device-row")).toHaveLength(2),
    );
    expect(screen.getByText("2 paired · 1 online")).toBeInTheDocument();
  });

  // 决策 12:移除那个形似筛选器的独立标签 —— 它看上去在等一个兄弟标签,
  // 但设备通常一到三台,按路径筛选没有意义。
  it("no longer renders the filter-like LAN tag", async () => {
    mockList.mockResolvedValueOnce([
      {
        id: 1,
        name: "mac",
        url: "ws://h1/rpc",
        tlsMode: "default",
        online: true,
        lastSeenAt: Date.now(),
      },
    ] as Partial<DeviceView>[]);
    render(<RemoteDevicesPanel />);
    await waitFor(() =>
      expect(screen.getAllByTestId("device-row")).toHaveLength(1),
    );
    // 这一轮把 remoteDevices.panel.lanAll 从两份 locale 里一并删掉了,所以标签
    // 若被重新加回 JSX,渲染出来的是**原始 key**而不是译文。译文断言只在「key 也
    // 被加回来」时才可能命中,单靠它挡不住另一半;两条都要。
    // (原先还有一条 /LAN 直连 · 全部/ —— setup.ts 在每个用例前强制 en,中文文案
    // 永远不会被渲染,那条断言恒真、挡不住任何东西,已删。)
    expect(
      screen.queryByText("remoteDevices.panel.lanAll"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/LAN direct · All/i)).not.toBeInTheDocument();
  });

  // R15 测试接缝:同一指纹的两个来源合并为一行且路径标记正确。
  it("merges same-fingerprint LAN + account devices into one row with path markers", async () => {
    mockList.mockResolvedValueOnce([
      {
        id: 1,
        name: "home-server",
        url: "ws://192.168.1.50:7456/rpc",
        daemonFingerprint: "fp-1",
        tlsMode: "default",
        online: false,
        lastSeenAt: 1_700_000_000_000,
      },
    ] as Partial<DeviceView>[]);
    mockServerList.mockResolvedValueOnce([
      {
        id: 10,
        name: "home-server",
        kind: "agentred",
        platform: "linux",
        version: "0.3.0",
        fingerprint: "fp-1",
        lastSeenAt: 1_700_000_000_000,
        status: 1,
        // 中转路径可达 = daemon 的中继在线登记(R20),不是账号侧授权标志。
        online: true,
        isThisDevice: false,
      },
    ]);
    render(<RemoteDevicesPanel />);
    await waitFor(() =>
      expect(screen.getAllByTestId("device-row")).toHaveLength(1),
    );
    // LAN 离线 → 直连失效(带文字),中转在用(带文字),地址显示「经中转」。
    expect(screen.getByText("Direct · Unreachable")).toBeInTheDocument();
    expect(screen.getByLabelText("Relay · In use")).toBeInTheDocument();
    expect(screen.getByText(/Via relay/)).toBeInTheDocument();
    expect(screen.queryByText(/192\.168\.1\.50/)).not.toBeInTheDocument();
  });

  // 真实场景:远端服务器上跑着 agentred,已登录同一个账号、中转在线,但这台桌面
  // 从没跟它 LAN 配对过 —— 它以前一行都不产生,面板里只看得见本机。
  it("lists an account-only agentred this desktop never paired over LAN", async () => {
    mockList.mockResolvedValueOnce([]);
    mockServerList.mockResolvedValueOnce([
      {
        id: 21,
        name: "cloud-box",
        kind: "agentred",
        platform: "linux",
        version: "0.3.0",
        fingerprint: "fp-cloud",
        lastSeenAt: 1_700_000_000_000,
        status: 1,
        online: true,
        isThisDevice: false,
      },
    ]);

    render(<RemoteDevicesPanel />);

    expect(await screen.findByTestId("device-row")).toBeInTheDocument();
    expect(screen.getByText("cloud-box")).toBeInTheDocument();
    expect(screen.getByLabelText("Relay · In use")).toBeInTheDocument();
    expect(screen.getByText(/Via relay/)).toBeInTheDocument();
    // 没有配对行 → 不给那组作用在配对行上的动作,也没有 TLS 徽章。
    expect(screen.queryByLabelText("More actions")).not.toBeInTheDocument();
    expect(screen.queryByText("OS Default")).not.toBeInTheDocument();
    // 它在账号清单里 → 不是「未认领」。
    expect(screen.queryByText("Unclaimed")).not.toBeInTheDocument();
  });

  it("lists an account-only agentred alongside the LAN-paired ones", async () => {
    mockList.mockResolvedValueOnce([
      {
        id: 1,
        name: "linux-srv",
        url: "ws://192.168.1.50:7456/rpc",
        daemonFingerprint: "fp-lan",
        tlsMode: "default",
        online: true,
        lastSeenAt: 1_700_000_000_000,
      },
    ] as Partial<DeviceView>[]);
    mockServerList.mockResolvedValueOnce([
      {
        id: 21,
        name: "cloud-box",
        kind: "agentred",
        platform: "linux",
        version: "0.3.0",
        fingerprint: "fp-cloud",
        lastSeenAt: 1_700_000_000_000,
        status: 1,
        online: true,
        isThisDevice: false,
      },
    ]);

    render(<RemoteDevicesPanel />);

    await waitFor(() =>
      expect(screen.getAllByTestId("device-row")).toHaveLength(2),
    );
    expect(screen.getByText("linux-srv")).toBeInTheDocument();
    expect(screen.getByText("cloud-box")).toBeInTheDocument();
    // LAN 那台仍留着自己的地址位与配对动作。
    expect(screen.getByText(/192\.168\.1\.50/)).toBeInTheDocument();
    expect(screen.getByLabelText("More actions")).toBeInTheDocument();
  });

  // 账号收编来的那一行(paired_agentred_entity.IsRelayOnly)有配对行、却没有 LAN
  // 地址:「刷新直连」拨不出去,「TLS 信任」也没有可信任的直连端点。判据是 url 有没有
  // 值,不是 lan 这一行在不在 —— 后者对收编行恒为真,正是它让这两个动作出现在一台
  // 没有直连地址的机器上,点「刷新直连」只会得到一个无意义的失败。
  it("hides the direct-connection actions on an adopted row that has no LAN address", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValueOnce([
      {
        id: 5,
        name: "cloud-box",
        url: "",
        daemonFingerprint: "fp-cloud",
        tlsMode: "default",
        online: true,
        lastSeenAt: 1_700_000_000_000,
      },
    ] as Partial<DeviceView>[]);
    mockServerList.mockResolvedValueOnce([
      {
        id: 21,
        name: "cloud-box",
        kind: "agentred",
        platform: "linux",
        version: "0.3.0",
        fingerprint: "fp-cloud",
        lastSeenAt: 1_700_000_000_000,
        status: 1,
        online: true,
        isThisDevice: false,
      },
    ]);

    render(<RemoteDevicesPanel />);

    await waitFor(() =>
      expect(screen.getAllByTestId("device-row")).toHaveLength(1),
    );
    await user.click(screen.getByLabelText("More actions"));
    // 改名与解除配对作用在这一行本身,收编行照样要有。
    expect(await screen.findByText("Rename")).toBeInTheDocument();
    expect(screen.getByText("Unpair")).toBeInTheDocument();
    expect(screen.queryByText("Refresh Status")).not.toBeInTheDocument();
    expect(screen.queryByText("Edit TLS Trust")).not.toBeInTheDocument();
  });

  it("keeps the direct-connection actions on a row that really has a LAN address", async () => {
    const user = userEvent.setup();
    mockList.mockResolvedValueOnce([
      {
        id: 6,
        name: "linux-srv",
        url: "ws://192.168.1.50:7456/rpc",
        daemonFingerprint: "fp-lan",
        tlsMode: "default",
        online: true,
        lastSeenAt: 1_700_000_000_000,
      },
    ] as Partial<DeviceView>[]);

    render(<RemoteDevicesPanel />);

    await waitFor(() =>
      expect(screen.getAllByTestId("device-row")).toHaveLength(1),
    );
    await user.click(screen.getByLabelText("More actions"));
    expect(await screen.findByText("Refresh Status")).toBeInTheDocument();
    expect(screen.getByText("Edit TLS Trust")).toBeInTheDocument();
  });

  describe("unpair & rename use dialogs, never native window.*", () => {
    const device = {
      id: 1,
      name: "linux-srv",
      url: "ws://h1/rpc",
      tlsMode: "default",
      online: true,
      lastSeenAt: Date.now(),
    } as Partial<DeviceView>;

    beforeEach(() => {
      mockList.mockReset();
      mockList.mockResolvedValueOnce([device] as Partial<DeviceView>[]);
      mockRemove.mockReset();
      mockRename.mockReset();
    });

    async function renderOneRow(user: ReturnType<typeof userEvent.setup>) {
      render(<RemoteDevicesPanel />);
      await waitFor(() =>
        expect(screen.getAllByTestId("device-row")).toHaveLength(1),
      );
      await user.click(screen.getByLabelText("More actions"));
    }

    it("clicking Unpair opens a confirm dialog with the blast-radius copy instead of removing right away", async () => {
      const user = userEvent.setup();
      await renderOneRow(user);
      await user.click(await screen.findByText("Unpair"));
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      expect(screen.getByText("linux-srv")).toBeInTheDocument();
      expect(
        screen.getByText(
          /This desktop will clear the token and fingerprint pin/,
        ),
      ).toBeInTheDocument();
      expect(mockRemove).not.toHaveBeenCalled();
    });

    it("confirming the Unpair dialog removes the device", async () => {
      const user = userEvent.setup();
      mockRemove.mockResolvedValue(undefined);
      await renderOneRow(user);
      await user.click(await screen.findByText("Unpair"));
      await user.click(screen.getByRole("button", { name: "Unpair" }));
      expect(mockRemove).toHaveBeenCalledWith(1);
    });

    it("cancelling the Unpair dialog does not remove the device", async () => {
      const user = userEvent.setup();
      await renderOneRow(user);
      await user.click(await screen.findByText("Unpair"));
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      expect(mockRemove).not.toHaveBeenCalled();
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    it("clicking Rename opens a dialog prefilled with the current name", async () => {
      const user = userEvent.setup();
      await renderOneRow(user);
      await user.click(await screen.findByText("Rename"));
      expect(screen.getByRole("dialog")).toBeInTheDocument();
      expect(screen.getByLabelText("Rename to")).toHaveValue("linux-srv");
    });

    it("submitting the Rename dialog calls RemoteDeviceRename with the trimmed name", async () => {
      const user = userEvent.setup();
      mockRename.mockResolvedValue(undefined);
      await renderOneRow(user);
      await user.click(await screen.findByText("Rename"));
      const input = screen.getByLabelText("Rename to");
      await user.clear(input);
      await user.type(input, "new-name  ");
      await user.click(screen.getByRole("button", { name: "Save" }));
      expect(mockRename).toHaveBeenCalledWith(1, "new-name");
    });
  });

  // 规格「界面与交互 › 登录」:设备面板是账号登录的入口。
  describe("account login", () => {
    it("shows a Sign in entry point when not connected to an account", async () => {
      mockList.mockResolvedValueOnce([]);
      render(<RemoteDevicesPanel />);
      await waitFor(() =>
        expect(
          screen.getByRole("button", { name: "Sign in" }),
        ).toBeInTheDocument(),
      );
      expect(screen.queryByText(/Signed in to/)).not.toBeInTheDocument();
    });

    // d) the logged-in state shows the account/server identity and offers logout.
    it("shows the account identity and Sign out when connected", async () => {
      mockList.mockResolvedValueOnce([]);
      mockGetState.mockResolvedValue(LOGGED_IN_SERVER_STATE);
      render(<RemoteDevicesPanel />);
      await waitFor(() =>
        expect(
          screen.getByText("Signed in to hub.example.com"),
        ).toBeInTheDocument(),
      );
      expect(
        screen.getByRole("button", { name: "Sign out" }),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "Sign in" }),
      ).not.toBeInTheDocument();
    });

    // 服务端够不着时后端不再清登录(bootstrap/server.go 曾经一律清)。界面要说的是
    // 「服务端离线,正在重试」,身份仍然摆在那儿 —— 而不是把用户推回登录入口。
    it("says the server is offline while keeping the signed-in identity", async () => {
      mockList.mockResolvedValueOnce([]);
      mockGetState.mockResolvedValue(LOGGED_IN_SERVER_STATE);
      mockOffline.mockResolvedValue(true);

      render(<RemoteDevicesPanel />);

      await waitFor(() =>
        expect(screen.getByText("Server offline")).toBeInTheDocument(),
      );
      expect(
        screen.getByText("Signed in to hub.example.com"),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "Sign in" }),
      ).not.toBeInTheDocument();
    });

    it("driving the full device flow through the dialog updates the panel to signed-in without further action", async () => {
      mockList.mockResolvedValueOnce([]);
      mockStartLogin.mockResolvedValueOnce({
        deviceCode: "device-abc",
        userCode: "ABCD-1234",
        verificationURI: "https://hub.example.com/device",
        verificationURIComplete:
          "https://hub.example.com/device?code=ABCD-1234",
        // Short interval keeps this wiring test fast and deterministic
        // without fake timers (timing precision is covered separately in
        // login-dialog.test.tsx).
        interval: 1,
        expiresIn: 900,
      });
      mockPollLoginToken.mockResolvedValueOnce(true);
      // Second GetState call (after onLoggedIn refresh) reports signed-in.
      mockGetState.mockResolvedValueOnce(ZERO_SERVER_STATE);
      mockGetState.mockResolvedValueOnce(LOGGED_IN_SERVER_STATE);

      render(<RemoteDevicesPanel />);
      await waitFor(() =>
        expect(
          screen.getByRole("button", { name: "Sign in" }),
        ).toBeInTheDocument(),
      );

      fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
      // 未记住自建地址 → 对话框默认官方云,无需输入,直接继续。
      fireEvent.click(screen.getByRole("button", { name: "Continue" }));

      await waitFor(() =>
        expect(screen.getByText("ABCD-1234")).toBeInTheDocument(),
      );

      await waitFor(
        () =>
          expect(
            screen.getByText("Signed in to hub.example.com"),
          ).toBeInTheDocument(),
        { timeout: 8_000 },
      );
      expect(mockPollLoginToken).toHaveBeenCalledWith("device-abc");
    }, 10_000);

    it("signing out returns to the Sign in entry point", async () => {
      mockList.mockResolvedValueOnce([]);
      mockGetState.mockResolvedValueOnce(LOGGED_IN_SERVER_STATE);
      mockGetState.mockResolvedValueOnce(ZERO_SERVER_STATE);
      render(<RemoteDevicesPanel />);
      await waitFor(() =>
        expect(
          screen.getByRole("button", { name: "Sign out" }),
        ).toBeInTheDocument(),
      );

      fireEvent.click(screen.getByRole("button", { name: "Sign out" }));

      expect(mockLogout).toHaveBeenCalled();
      await waitFor(() =>
        expect(
          screen.getByRole("button", { name: "Sign in" }),
        ).toBeInTheDocument(),
      );
    });
  });
});
