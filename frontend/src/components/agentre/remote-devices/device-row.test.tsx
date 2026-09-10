// frontend/src/components/agentre/remote-devices/device-row.test.tsx
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  RemoteDeviceUpgrade: vi.fn(),
  RemoteDeviceGet: vi.fn(),
}));

const mockCopy = vi.fn();
vi.mock("@agentre-hub/agentre-ui", async () => {
  const actual = await vi.importActual<
    typeof import("@agentre-hub/agentre-ui")
  >("@agentre-hub/agentre-ui");
  return {
    ...actual,
    copyTextWithToast: (...args: unknown[]) => mockCopy(...args),
  };
});

import {
  RemoteDeviceUpgrade,
  RemoteDeviceGet,
} from "../../../../wailsjs/go/app/App";
import { DeviceRow } from "./device-row";
import type { DeviceRowModel, DeviceView } from "./use-remote-devices";

const mockUpgrade = RemoteDeviceUpgrade as unknown as ReturnType<typeof vi.fn>;
const mockGet = RemoteDeviceGet as unknown as ReturnType<typeof vi.fn>;

const baseLan: DeviceView = {
  id: 1,
  name: "linux-srv",
  url: "ws://192.168.1.100:7456/rpc",
  daemonFingerprint: "fp",
  instanceUUID: "u",
  tlsMode: "default",
  tlsCertPEM: "",
  pairedAt: 1,
  lastSeenAt: 0,
  lastError: "",
  online: false,
};

const baseDevice: DeviceRowModel = {
  key: "lan:1",
  name: "linux-srv",
  online: false,
  lastSeenAt: 0,
  lan: baseLan,
  account: undefined,
  paths: [{ kind: "lan", state: "dead" }],
  signedOut: false,
  viaRelay: false,
};

const noopActions = {
  onRefresh: () => {},
  onRename: () => {},
  onEditTLS: () => {},
  onRemove: () => {},
};

function renderRow(device: DeviceRowModel) {
  return render(
    <DeviceRow device={device} now={1_000_000} actions={noopActions} />,
  );
}

describe("DeviceRow", () => {
  it("renders name + URL", () => {
    renderRow(baseDevice);
    expect(screen.getByText("linux-srv")).toBeInTheDocument();
    expect(screen.getByText(/192\.168\.1\.100/)).toBeInTheDocument();
  });
  it("shows OS 默认 badge for default mode", () => {
    renderRow(baseDevice);
    expect(screen.getByText("OS Default")).toBeInTheDocument();
  });
  it("renders 尚未连接 when lastSeenAt = 0", () => {
    renderRow(baseDevice);
    expect(screen.getByText(/Never connected/)).toBeInTheDocument();
  });
  it("renders friendly error for tofu_mismatch in destructive style", () => {
    const d = {
      ...baseDevice,
      lan: { ...baseLan, lastError: "tofu_mismatch" },
    };
    renderRow(d);
    expect(
      screen.getByText(/identity fingerprint changed/),
    ).toBeInTheDocument();
  });
  it("fires onRemove from action menu", async () => {
    const user = userEvent.setup();
    const onRemove = vi.fn();
    render(
      <DeviceRow
        device={baseDevice}
        now={1_000_000}
        actions={{ ...noopActions, onRemove }}
      />,
    );
    await user.click(screen.getByLabelText("More actions"));
    await user.click(await screen.findByText("Unpair"));
    expect(onRemove).toHaveBeenCalled();
  });

  // ── 远端 agentred 的版本与升级出口 ─────────────────────────────────────────
  // 版本挂在副行（决策 17）：标题行已经有状态点、TLS 徽章与路径 chip，再加一枚
  // 就把设备名挤没了。
  describe("the remote agentred's build", () => {
    const releaseBuild: DeviceRowModel = {
      ...baseDevice,
      online: true,
      lan: { ...baseLan, daemonVersion: "0.5.2", daemonCommit: "a1b2c3d" },
    };

    it("renders the version the remote agentred reported", () => {
      render(
        <DeviceRow
          device={releaseBuild}
          now={1_000_000}
          actions={noopActions}
        />,
      );
      expect(screen.getByText("0.5.2")).toBeInTheDocument();
    });

    it("badges a release build that is older than the latest known version", () => {
      render(
        <DeviceRow
          device={releaseBuild}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.6.0"
        />,
      );
      expect(screen.getByText("Upgradable to 0.6.0")).toBeInTheDocument();
    });

    it("leaves a release build that is already current unbadged", () => {
      render(
        <DeviceRow
          device={releaseBuild}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.5.2"
        />,
      );
      expect(screen.queryByText(/Upgradable to/)).not.toBeInTheDocument();
      expect(screen.getByText("0.5.2")).toBeInTheDocument();
    });

    // 决策 5：未注入版本的构建自称 1.0.0，比任何 0.x 正式版都「新」。短 commit
    // 为空是唯一判据 —— 它显示为开发构建，且永不劝升。
    it("shows a build with no short commit as a development build and never nudges it", () => {
      render(
        <DeviceRow
          device={{
            ...baseDevice,
            online: true,
            lan: { ...baseLan, daemonVersion: "1.0.0", daemonCommit: "" },
          }}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.6.0"
        />,
      );
      expect(screen.getByText("Development build")).toBeInTheDocument();
      expect(screen.queryByText(/Upgradable to/)).not.toBeInTheDocument();
      expect(screen.queryByText("1.0.0")).not.toBeInTheDocument();
    });

    // 协议不匹配是强提示：一键升级够不着（握手都没过），出口只能是可复制的命令卡。
    it("turns a protocol-version rejection into the strong state with a copyable command", () => {
      render(
        <DeviceRow
          device={{
            ...baseDevice,
            lan: { ...baseLan, lastError: "protocol_mismatch" },
          }}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.6.0"
        />,
      );
      expect(screen.getByText("Too old to connect")).toBeInTheDocument();
      expect(screen.getByText("agentred update")).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: /Copy Run on that machine/ }),
      ).toBeInTheDocument();
    });
  });

  // ── R15 可达路径 chips ─────────────────────────────────────────────────────
  it("renders the in-use LAN path highlighted and the relay path available", () => {
    const d: DeviceRowModel = {
      ...baseDevice,
      online: true,
      account: {
        id: 10,
        name: "linux-srv",
        kind: "agentred",
        platform: "linux",
        version: "0.3.0",
        fingerprint: "fp",
        lastSeenAt: 1,
        status: 1,
        online: true,
        isThisDevice: false,
      },
      paths: [
        { kind: "lan", state: "in-use" },
        { kind: "relay", state: "available" },
      ],
    };
    renderRow(d);
    const lan = screen.getByLabelText("Direct · In use");
    expect(lan).toBeInTheDocument();
    expect(screen.getByText("Relay")).toBeInTheDocument();
    // 在用路径高亮:主色文本。
    expect(lan.className).toMatch(/font-semibold/);
  });

  it("labels an unreachable path with text, not styling alone", () => {
    const d: DeviceRowModel = {
      ...baseDevice,
      paths: [{ kind: "lan", state: "dead" }],
    };
    renderRow(d);
    // 失效态除样式(划线/淡出)外另有文字表达。
    expect(screen.getByText("Direct · Unreachable")).toBeInTheDocument();
  });

  it("shows 经中转 as the address when the relay path is in use", () => {
    const d: DeviceRowModel = {
      ...baseDevice,
      account: {
        id: 10,
        name: "linux-srv",
        kind: "agentred",
        platform: "linux",
        version: "0.3.0",
        fingerprint: "fp",
        lastSeenAt: 1,
        status: 1,
        online: true,
        isThisDevice: false,
      },
      viaRelay: true,
      paths: [
        { kind: "lan", state: "dead" },
        { kind: "relay", state: "in-use" },
      ],
    };
    renderRow(d);
    expect(screen.getByText(/Via relay/)).toBeInTheDocument();
    expect(screen.queryByText(/192\.168\.1\.100/)).not.toBeInTheDocument();
  });

  // ── 未登录账号标注 + 登录指引 ──────────────────────────────────────────────
  it("marks a signed-out device and explains the consequence", () => {
    const d: DeviceRowModel = { ...baseDevice, signedOut: true };
    renderRow(d);
    expect(screen.getByText("Not signed in")).toBeInTheDocument();
    expect(screen.getByText(/other devices can't see it/i)).toBeInTheDocument();
  });

  it("does not show the signed-out marking on a signed-in device", () => {
    renderRow(baseDevice);
    expect(screen.queryByText("Not signed in")).not.toBeInTheDocument();
  });

  it("reveals sign-in instructions when the action is pressed", async () => {
    const user = userEvent.setup();
    const d: DeviceRowModel = { ...baseDevice, signedOut: true };
    renderRow(d);
    expect(screen.queryByText(/agentred login/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "How to sign in" }));
    expect(screen.getByText(/agentred login/)).toBeInTheDocument();
  });

  // ── 账号独有的一行 ─────────────────────────────────────────────────────────
  // 这台机器只在账号里,本机没有 paired_agentreds 那一行 —— TLS 徽章 / LAN 地址 /
  // 那组作用在配对行上的动作(刷新直连、改名、改 TLS、删配对)统统无处落脚。
  describe("a row with no LAN pairing", () => {
    const accountOnly: DeviceRowModel = {
      key: "account:21",
      name: "cloud-box",
      online: true,
      lastSeenAt: 1_700_000_000_000,
      account: {
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
      paths: [{ kind: "relay", state: "in-use" }],
      signedOut: false,
      viaRelay: true,
    };

    it("names the machine and shows 经中转 in the address slot", () => {
      render(<DeviceRow device={accountOnly} now={1_700_000_060_000} />);
      expect(screen.getByText("cloud-box")).toBeInTheDocument();
      expect(screen.getByText(/Via relay/)).toBeInTheDocument();
      expect(screen.getByLabelText("Relay · In use")).toBeInTheDocument();
    });

    it("offers none of the LAN-only affordances", () => {
      render(<DeviceRow device={accountOnly} now={1_700_000_060_000} />);
      // TLS 信任模式是配对行上的字段。
      expect(screen.queryByText("OS Default")).not.toBeInTheDocument();
      // 刷新直连 / 改名 / 改 TLS / 删配对 全都作用在配对行上。
      expect(screen.queryByLabelText("More actions")).not.toBeInTheDocument();
    });

    it("keeps the address slot on the relay even while the relay is unreachable", () => {
      render(
        <DeviceRow
          device={{
            ...accountOnly,
            online: false,
            viaRelay: false,
            paths: [{ kind: "relay", state: "dead" }],
          }}
          now={1_700_000_060_000}
        />,
      );
      expect(screen.getByText(/Via relay/)).toBeInTheDocument();
      expect(screen.getByText("Relay · Unreachable")).toBeInTheDocument();
    });
  });

  // ── 远程一键升级(spec「远程一键升级」+「桌面端呈现」)───────────────────
  // 全程用 fireEvent + 手工 flush(而不是 userEvent)驱动交互:这组用例要用假
  // 时钟推进 5 分钟超时,userEvent 自带的内部延时与假时钟搭配在这个环境里会
  // 挂住 —— 与 login-dialog.test.tsx 的轮询用例同一个规避方式。
  describe("remote upgrade action", () => {
    const upgradable: DeviceRowModel = {
      ...baseDevice,
      online: true,
      lan: { ...baseLan, daemonVersion: "0.5.2", daemonCommit: "a1b2c3d" },
    };

    beforeEach(() => {
      mockUpgrade.mockReset();
      mockGet.mockReset();
      vi.useFakeTimers({
        toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval"],
      });
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    async function flush() {
      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });
    }

    async function openMenuAndClickUpgrade(label = "Upgrade agentred") {
      fireEvent.pointerDown(screen.getByLabelText("More actions"), {
        button: 0,
      });
      await flush();
      fireEvent.click(screen.getByText(label));
      await flush();
    }

    it("enters the upgrading state once the daemon accepts the click, and resolves to success on reconnect with a new version", async () => {
      mockUpgrade.mockResolvedValue({ accepted: true, targetVersion: "0.6.0" });
      render(
        <DeviceRow
          device={upgradable}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.6.0"
        />,
      );

      await openMenuAndClickUpgrade();

      expect(mockUpgrade).toHaveBeenCalledWith(1, "", false);
      expect(screen.getByText(/Upgrading to 0.6.0/)).toBeInTheDocument();

      mockGet.mockResolvedValue({ daemonVersion: "0.6.0" });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });

      expect(screen.getByText("Upgrade complete")).toBeInTheDocument();
      expect(screen.getByText("0.5.2 → 0.6.0")).toBeInTheDocument();
    });

    // 受理判定要在那台机器上把解析发布、下载、校验、替换全跑完才应答,能长达几分钟。
    // 这一段沉默会被读成「没点上」,而再点一次只会撞上那台机器的并发闸门 —— 那句
    // 「已经有一次升级在跑」是界面自己招来的。
    it("says it is preparing while the acceptance call is still in flight", async () => {
      mockUpgrade.mockReturnValue(new Promise(() => {}));
      render(
        <DeviceRow
          device={upgradable}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.6.0"
        />,
      );

      await openMenuAndClickUpgrade();

      expect(screen.getByText("Preparing the upgrade")).toBeInTheDocument();
      // 菜单项这时也点不动了。
      fireEvent.pointerDown(screen.getByLabelText("More actions"), {
        button: 0,
      });
      await flush();
      expect(
        screen.getByText("Preparing…").closest("[role='menuitem']"),
      ).toHaveAttribute("aria-disabled", "true");
    });

    it("resolves to a timeout failure after 5 minutes without a version change", async () => {
      mockUpgrade.mockResolvedValue({ accepted: true, targetVersion: "0.6.0" });
      mockGet.mockRejectedValue(new Error("dial failed"));
      render(
        <DeviceRow
          device={upgradable}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.6.0"
        />,
      );

      await openMenuAndClickUpgrade();
      expect(screen.getByText(/Upgrading to 0.6.0/)).toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
      });

      expect(screen.getByText("It didn't come back")).toBeInTheDocument();
    });

    // daemon 受理之后就重启,watcher 下一次拨号必然失败并把 lastError 落成
    // dial_failed —— 也就是说「升级中」那一整段时间里这一行必然带着一条 lastError。
    // 反馈位要是被它盖掉,升级中/成功/超时三态在真实流程里一次都画不出来,而超时那
    // 一态的命令卡恰恰是「它没回来」时唯一的出口。
    it("keeps the upgrade feedback visible while the restarting daemon is unreachable", async () => {
      mockUpgrade.mockResolvedValue({ accepted: true, targetVersion: "0.6.0" });
      mockGet.mockRejectedValue(new Error("dial failed"));
      render(
        <DeviceRow
          device={{
            ...upgradable,
            lan: {
              ...upgradable.lan!,
              lastError: "dial_failed:connection refused",
            },
          }}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.6.0"
        />,
      );

      await openMenuAndClickUpgrade();
      expect(screen.getByText(/Upgrading to 0.6.0/)).toBeInTheDocument();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
      });

      expect(screen.getByText("It didn't come back")).toBeInTheDocument();
      expect(screen.getByText("agentred update")).toBeInTheDocument();
    });

    it("on a device with running turns, keeps the primary action enabled with the 仍要升级 wording, and only sends force after the confirm step", async () => {
      mockUpgrade.mockResolvedValueOnce({
        accepted: false,
        rejectReason: "active_turns",
        message:
          "this machine has 2 running conversation(s); upgrading would interrupt them",
        activeTurns: 2,
      });
      render(
        <DeviceRow
          device={upgradable}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.6.0"
        />,
      );

      await openMenuAndClickUpgrade();
      expect(mockUpgrade).toHaveBeenCalledTimes(1);
      expect(mockUpgrade).toHaveBeenLastCalledWith(1, "", false);

      // 选中一项会关掉菜单(Radix 默认行为);拒绝之后主动作重新可点、文案改口,
      // 不是禁用态 —— 重新打开菜单看到的就是这个新态。
      fireEvent.pointerDown(screen.getByLabelText("More actions"), {
        button: 0,
      });
      await flush();
      const forceItem = screen.getByText("Upgrade anyway");
      expect(forceItem.closest('[role="menuitem"]')).not.toHaveAttribute(
        "data-disabled",
      );

      // 点这一下只打开确认,尚未发出第二次调用。
      fireEvent.click(forceItem);
      await flush();
      expect(mockUpgrade).toHaveBeenCalledTimes(1);
      const dialog = screen.getByRole("dialog");
      expect(
        within(dialog).getByText(
          "this machine has 2 running conversation(s); upgrading would interrupt them",
        ),
      ).toBeInTheDocument();

      mockUpgrade.mockResolvedValueOnce({
        accepted: true,
        targetVersion: "0.6.0",
      });
      fireEvent.click(within(dialog).getByText("Upgrade anyway"));
      await flush();

      expect(mockUpgrade).toHaveBeenCalledTimes(2);
      expect(mockUpgrade).toHaveBeenLastCalledWith(1, "", true);
    });

    // 决策 18:一键升级与可复制的命令**始终并列**在同一处,不按状态二选一 ——
    // 协议不匹配那一态下一键升级必然够不着(握手都没过),命令是唯一出口;始终并列
    // 则只有一套布局,用户也能自己选。spec「桌面端呈现」把桌面端的那一处点名为动作
    // 菜单:「菜单里同时给出可复制的 agentred update 命令,作为一键升级不可用时的
    // 兜底」。因此这条断言走的是「有新版本」与「已是最新」两态 —— 命令只在协议不
    // 匹配/超时那两个坏结局才出现,就正是它要挡住的「按状态二选一」。
    it.each([
      ["a device with an upgrade available", "0.6.0"],
      ["a device that is already up to date", "0.5.2"],
    ])(
      "offers the copyable upgrade command next to the one-click upgrade on %s",
      async (_name, latestVersion) => {
        mockCopy.mockReset();
        render(
          <DeviceRow
            device={upgradable}
            now={1_000_000}
            actions={noopActions}
            latestVersion={latestVersion}
          />,
        );

        fireEvent.pointerDown(screen.getByLabelText("More actions"), {
          button: 0,
        });
        await flush();
        fireEvent.click(screen.getByText("Copy the upgrade command"));
        await flush();

        expect(mockCopy).toHaveBeenCalledWith(
          "agentred update",
          expect.anything(),
        );
      },
    );

    it("keeps the menu item present but disabled, naming the version, when the device is already up to date", async () => {
      render(
        <DeviceRow
          device={upgradable}
          now={1_000_000}
          actions={noopActions}
          latestVersion="0.5.2"
        />,
      );

      fireEvent.pointerDown(screen.getByLabelText("More actions"), {
        button: 0,
      });
      await flush();
      const item = screen.getByText("Up to date (0.5.2)");
      expect(item.closest('[role="menuitem"]')).toHaveAttribute(
        "data-disabled",
      );
      expect(mockUpgrade).not.toHaveBeenCalled();
    });
  });
});
