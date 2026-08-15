import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  RemoteDeviceList: vi.fn(),
  RemoteDeviceAdd: vi.fn(),
  RemoteDeviceRemove: vi.fn(),
  RemoteDeviceUpdateTLS: vi.fn(),
  RemoteDeviceRefresh: vi.fn(),
  RemoteDeviceRename: vi.fn(),
  ServerListDevices: vi.fn(),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(() => vi.fn()),
}));

import {
  RemoteDeviceList,
  RemoteDeviceAdd,
  ServerListDevices,
} from "../../../../wailsjs/go/app/App";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";
import {
  useRemoteDevices,
  mergeDeviceSources,
  type DeviceView,
} from "./use-remote-devices";
import type { server_svc } from "../../../../wailsjs/go/models";

const mockList = RemoteDeviceList as unknown as ReturnType<typeof vi.fn>;
const mockAdd = RemoteDeviceAdd as unknown as ReturnType<typeof vi.fn>;
const mockServerList = ServerListDevices as unknown as ReturnType<typeof vi.fn>;
const mockEventsOn = EventsOn as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  mockList.mockReset();
  mockAdd.mockReset();
  mockEventsOn.mockReset();
  mockServerList.mockReset();
  mockEventsOn.mockImplementation(() => vi.fn()); // 默认返回 unsubscribe stub
  // 默认未登录:ServerListDevices 拒绝 → 账号来源 unknown,不判未认领。
  mockServerList.mockRejectedValue(new Error("not logged in"));
});

const lanDevice = (over: Partial<DeviceView> = {}): DeviceView => ({
  id: 1,
  name: "linux-srv",
  url: "ws://192.168.1.100:7456/rpc",
  daemonFingerprint: "fp-1",
  instanceUUID: "u1",
  tlsMode: "default",
  tlsCertPEM: "",
  pairedAt: 1,
  lastSeenAt: 1_700_000_000_000,
  lastError: "",
  online: true,
  daemonOutdated: false,
  ...over,
});

const accountDevice = (
  over: Partial<server_svc.Device> = {},
): server_svc.Device => ({
  ID: 10,
  Name: "linux-srv",
  Kind: "agentred",
  Platform: "linux",
  Version: "0.3.0",
  Fingerprint: "fp-1",
  LastSeenAt: 1_700_000_000_000,
  Status: 1, // ACTIVE
  Online: true, // 中继在线登记(R20)
  IsThisDevice: false,
  ...over,
});

describe("mergeDeviceSources (R15)", () => {
  it("merges LAN + account rows with the same fingerprint into one row", () => {
    const rows = mergeDeviceSources([lanDevice()], {
      known: true,
      devices: [accountDevice()],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0].name).toBe("linux-srv");
    expect(rows[0].account?.Fingerprint).toBe("fp-1");
    expect(rows[0].unclaimed).toBe(false);
    expect(rows[0].viaRelay).toBe(false);
    // LAN 在线 → 直连在用,中转可用。
    expect(rows[0].paths).toEqual([
      { kind: "lan", state: "in-use" },
      { kind: "relay", state: "available" },
    ]);
  });

  it("marks relay in-use and LAN dead when the LAN path is offline", () => {
    const rows = mergeDeviceSources([lanDevice({ online: false })], {
      known: true,
      devices: [accountDevice()],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0].viaRelay).toBe(true);
    expect(rows[0].paths).toEqual([
      { kind: "lan", state: "dead" },
      { kind: "relay", state: "in-use" },
    ]);
  });

  // 收编自账号的行没有 LAN 地址(url 为空 = 后端的 IsRelayOnly)。它确实有一行本地
  // 记录——那正是让它能被选成「运行设备」的东西——但它**没有** LAN 路径。照 LAN 行
  // 渲染会画出一条根本不存在的直连路径,把「本机从没配对过这台机器」说成「直连在用」。
  it("does not invent a LAN path for a row adopted from the account (no url)", () => {
    const rows = mergeDeviceSources([lanDevice({ url: "" })], {
      known: true,
      devices: [accountDevice()],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0].paths).toEqual([{ kind: "relay", state: "in-use" }]);
    expect(rows[0].viaRelay).toBe(true);
    expect(rows[0].online).toBe(true);
    expect(rows[0].unclaimed).toBe(false);
  });

  // 收编行 + 账号侧中继离线 = 这台机器此刻真的够不着,不能因为本地有一行就报在线。
  it("an adopted row whose relay presence is gone is offline with a dead relay path", () => {
    const rows = mergeDeviceSources([lanDevice({ url: "" })], {
      known: true,
      devices: [accountDevice({ Online: false })],
    });
    expect(rows[0].paths).toEqual([{ kind: "relay", state: "dead" }]);
    expect(rows[0].online).toBe(false);
  });

  it("marks a LAN-only device unclaimed when the account list is known", () => {
    const rows = mergeDeviceSources([lanDevice()], {
      known: true,
      devices: [],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0].unclaimed).toBe(true);
    expect(rows[0].account).toBeUndefined();
    expect(rows[0].paths).toEqual([{ kind: "lan", state: "in-use" }]);
  });

  // 「未认领」是一句断言:这台机器不在账号清单里。指纹为空的 LAN 行(还没握过手 /
  // 旧配对行)根本无从判断,accountByFp 也刻意不收空指纹键——拿空串去查必然 miss,
  // 于是一台**已认领**的机器被标成「未认领 · 其它设备看不到它」。缺少依据时不下结论。
  it("does not claim anything about a LAN row that carries no daemon fingerprint", () => {
    const rows = mergeDeviceSources([lanDevice({ daemonFingerprint: "" })], {
      known: true,
      devices: [accountDevice()],
    });
    const lanRow = rows.find((r) => r.lan?.id === 1);
    expect(lanRow?.account).toBeUndefined();
    expect(lanRow?.unclaimed).toBe(false);
    // 这条 LAN 行没有指纹,连不上任何账号行 —— 于是那台账号设备照样自己成一行。
    // 猜它俩是同一台机器需要一个我们没有的依据,而猜错的代价是把一台确实
    // 在线的机器藏起来,正是这次要修的毛病。
    expect(rows.map((r) => r.key)).toEqual(["lan:1", "account:10"]);
  });

  it("does not mark unclaimed when the account list is unknown (not logged in)", () => {
    const rows = mergeDeviceSources([lanDevice()], {
      known: false,
      devices: [],
    });
    expect(rows[0].unclaimed).toBe(false);
  });

  it("treats a daemon with no relay presence as a dead relay path", () => {
    const rows = mergeDeviceSources([lanDevice()], {
      known: true,
      devices: [accountDevice({ Online: false })],
    });
    expect(rows[0].viaRelay).toBe(false);
    expect(rows[0].paths).toEqual([
      { kind: "lan", state: "in-use" },
      { kind: "relay", state: "dead" },
    ]);
  });

  // R15:「该行呈现它的可达路径而非凭据来源」。账号侧的 Status 是授权标志
  // (ACTIVE / REVOKED),不是可达性 —— 一台关机的机器账号行仍是 ACTIVE,
  // 拿 Status 当路径状态会让面板宣称一条通向关机机器的中转路径。
  it("does not claim a relay path from the account authorization flag alone", () => {
    const rows = mergeDeviceSources([lanDevice({ online: false })], {
      known: true,
      devices: [accountDevice({ Status: 1, Online: false })],
    });
    expect(rows[0].viaRelay).toBe(false);
    expect(rows[0].paths).toEqual([
      { kind: "lan", state: "dead" },
      { kind: "relay", state: "dead" },
    ]);
  });

  // 反向:授权已撤销但机器此刻仍挂在中转上 —— 路径标记跟随可达性,
  // 撤销后 daemon 无法续期在线登记,Online 自然落回 false。
  it("reports a reachable relay path even when the account row is not ACTIVE", () => {
    const rows = mergeDeviceSources([lanDevice({ online: false })], {
      known: true,
      devices: [accountDevice({ Status: 2, Online: true })],
    });
    expect(rows[0].viaRelay).toBe(true);
    expect(rows[0].paths).toEqual([
      { kind: "lan", state: "dead" },
      { kind: "relay", state: "in-use" },
    ]);
  });

  it("keeps distinct rows per LAN fingerprint", () => {
    const rows = mergeDeviceSources(
      [
        lanDevice({ id: 1, daemonFingerprint: "fp-1" }),
        lanDevice({ id: 2, name: "pi", daemonFingerprint: "fp-2" }),
      ],
      { known: true, devices: [accountDevice()] },
    );
    expect(rows).toHaveLength(2);
    expect(rows.map((r) => r.name)).toEqual(["linux-srv", "pi"]);
  });

  // ── 账号独有的机器(按指纹全外连接)────────────────────────────────────────
  // 合并以 LAN 为左表做左连接时,一台只登记在账号里、本机从没 LAN 配对过的
  // agentred 一行都不产生 —— 用户在远端服务器上登录了同一个账号、中转也在线,
  // 面板却只看得见本机。两侧都要当左表。
  it("emits a row for an account-only agentred that was never paired over LAN", () => {
    const rows = mergeDeviceSources([], {
      known: true,
      devices: [accountDevice({ Fingerprint: "fp-cloud", Name: "cloud-box" })],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0].name).toBe("cloud-box");
    expect(rows[0].account?.Fingerprint).toBe("fp-cloud");
    // 没有本机配对行 —— 这一行在类型上就没有 LAN 来源。
    expect(rows[0].lan).toBeUndefined();
    // 只有中转一条可达路径。
    expect(rows[0].paths).toEqual([{ kind: "relay", state: "in-use" }]);
    expect(rows[0].viaRelay).toBe(true);
    expect(rows[0].online).toBe(true);
    // 「未认领」= 仅本机配对、账号清单里没有它。账号独有的行按定义不是未认领。
    expect(rows[0].unclaimed).toBe(false);
  });

  it("keeps an account-only machine listed while its relay presence is gone", () => {
    const rows = mergeDeviceSources([], {
      known: true,
      devices: [
        accountDevice({
          Fingerprint: "fp-cloud",
          Name: "cloud-box",
          Online: false,
        }),
      ],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0].paths).toEqual([{ kind: "relay", state: "dead" }]);
    expect(rows[0].online).toBe(false);
    expect(rows[0].viaRelay).toBe(false);
    expect(rows[0].lastSeenAt).toBe(1_700_000_000_000);
  });

  it("produces one row per machine across LAN-only, both, and account-only", () => {
    const rows = mergeDeviceSources(
      [
        lanDevice({ id: 1, name: "linux-srv", daemonFingerprint: "fp-1" }),
        lanDevice({ id: 2, name: "pi", daemonFingerprint: "fp-lan-only" }),
      ],
      {
        known: true,
        devices: [
          accountDevice({ ID: 10, Fingerprint: "fp-1" }),
          accountDevice({
            ID: 11,
            Name: "cloud-box",
            Fingerprint: "fp-acct-only",
          }),
        ],
      },
    );
    expect(rows.map((r) => r.name)).toEqual(["linux-srv", "pi", "cloud-box"]);
    expect(rows.map((r) => r.lan?.id)).toEqual([1, 2, undefined]);
    // 两边都有的那台只占一行,不因为参与合并而被复制成两行。
    expect(rows.filter((r) => r.account?.Fingerprint === "fp-1")).toHaveLength(
      1,
    );
    expect(rows[1].unclaimed).toBe(true);
    expect(rows[2].unclaimed).toBe(false);
  });

  // 账号清单里 kind=desktop 的机器由 DesktopDeviceRow 单独成行(R19),
  // 合并结果里再产生一行就是同一台机器出现两次。
  it("leaves the account's desktop entries to their own row shape", () => {
    const rows = mergeDeviceSources([], {
      known: true,
      devices: [
        accountDevice({
          ID: 12,
          Kind: "desktop",
          Name: "my-mac",
          Fingerprint: "fp-desktop",
          IsThisDevice: true,
        }),
      ],
    });
    expect(rows).toEqual([]);
  });

  // 配对行 id 与账号设备 ID 是两个自增序列,会撞号 —— 行键必须自带来源。
  it("gives LAN and account-only rows distinct keys when their ids collide", () => {
    const rows = mergeDeviceSources([lanDevice({ id: 10 })], {
      known: true,
      devices: [
        accountDevice({ ID: 10, Name: "cloud-box", Fingerprint: "fp-cloud" }),
      ],
    });
    expect(rows).toHaveLength(2);
    expect(new Set(rows.map((r) => r.key)).size).toBe(2);
  });

  // 账号清单未知(未登录 / 拉取失败)时没有账号来源,一行都不该凭空多出来。
  it("adds no account-only rows when the account list is unknown", () => {
    const rows = mergeDeviceSources([lanDevice()], {
      known: false,
      devices: [],
    });
    expect(rows).toHaveLength(1);
    expect(rows[0].lan?.id).toBe(1);
  });
});

describe("useRemoteDevices", () => {
  it("moves an initial load failure to error and a successful retry to ready", async () => {
    mockList.mockRejectedValueOnce(new Error("list unavailable"));
    const { result } = renderHook(() => useRemoteDevices());

    await waitFor(() => expect(result.current.loadState).toBe("error"));
    expect(result.current.devices).toEqual([]);

    mockList.mockResolvedValueOnce([lanDevice()]);
    await act(async () => {
      await result.current.reload();
    });

    expect(result.current.loadState).toBe("ready");
    expect(result.current.devices).toHaveLength(1);
  });

  it("keeps existing devices ready when a background focus reload fails", async () => {
    mockList.mockResolvedValueOnce([lanDevice()]);
    const { result } = renderHook(() => useRemoteDevices());
    await waitFor(() => expect(result.current.loadState).toBe("ready"));

    mockList.mockRejectedValueOnce(new Error("refresh unavailable"));
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
    });
    await waitFor(() => expect(mockList).toHaveBeenCalledTimes(2));

    expect(result.current.loadState).toBe("ready");
    expect(result.current.devices).toHaveLength(1);
    expect(result.current.devices[0].name).toBe("linux-srv");
  });

  it("keeps the newest result when overlapping reloads finish out of order", async () => {
    let resolveInitial!: (devices: DeviceView[]) => void;
    mockList
      .mockImplementationOnce(
        () =>
          new Promise<DeviceView[]>((resolve) => {
            resolveInitial = resolve;
          }),
      )
      .mockResolvedValueOnce([lanDevice({ id: 2, name: "newest" })]);

    const { result } = renderHook(() => useRemoteDevices());
    await waitFor(() => expect(mockList).toHaveBeenCalledTimes(1));

    await act(async () => {
      await result.current.reload();
    });
    expect(result.current.devices[0].name).toBe("newest");

    await act(async () => {
      resolveInitial([lanDevice({ name: "stale" })]);
      await Promise.resolve();
    });

    expect(result.current.devices[0].name).toBe("newest");
  });

  it("loads devices on mount", async () => {
    // url 必须写出来:它现在是「这一行有没有 LAN 路径」的标记,省略即表示收编行。
    mockList.mockResolvedValueOnce([{ id: 1, name: "a", url: "ws://a/rpc" }]);
    const { result } = renderHook(() => useRemoteDevices());
    await waitFor(() => expect(result.current.loadState).toBe("ready"));
    expect(result.current.devices[0].name).toBe("a");
    // 未登录 → 无账号来源,单行只有 LAN 直连路径。
    expect(result.current.devices[0].paths).toEqual([
      { kind: "lan", state: "dead" },
    ]);
    expect(result.current.devices[0].unclaimed).toBe(false);
  });

  it("reloads on window focus", async () => {
    mockList.mockResolvedValueOnce([]);
    renderHook(() => useRemoteDevices());
    await waitFor(() => expect(mockList).toHaveBeenCalledTimes(1));
    mockList.mockResolvedValueOnce([{ id: 2, name: "b" }]);
    await act(async () => {
      window.dispatchEvent(new Event("focus"));
    });
    await waitFor(() => expect(mockList).toHaveBeenCalledTimes(2));
  });

  it("add() calls binding then reloads", async () => {
    mockList.mockResolvedValue([]);
    mockAdd.mockResolvedValueOnce({ id: 3, name: "c" });
    const { result } = renderHook(() => useRemoteDevices());
    await waitFor(() => expect(result.current.loadState).toBe("ready"));
    await act(async () => {
      await result.current.add({
        url: "ws://h/rpc",
        pairingCode: "ABC2DE",
        displayName: "c",
        tlsMode: "default",
        tlsCertPEM: "",
      });
    });
    expect(mockAdd).toHaveBeenCalled();
    expect(mockList).toHaveBeenCalledTimes(2);
  });

  it("merges remote.device.state events into devices by id", async () => {
    mockList.mockResolvedValueOnce([
      {
        id: 1,
        name: "a",
        url: "ws://a/rpc",
        online: false,
        lastSeenAt: 0,
        lastError: "",
      },
      {
        id: 2,
        name: "b",
        url: "ws://b/rpc",
        online: false,
        lastSeenAt: 0,
        lastError: "",
      },
    ]);
    const handlers: Record<string, (p: unknown) => void> = {};
    mockEventsOn.mockImplementation(
      (name: string, fn: (p: unknown) => void) => {
        handlers[name] = fn;
        return () => {};
      },
    );

    const { result } = renderHook(() => useRemoteDevices());
    await waitFor(() => expect(result.current.loadState).toBe("ready"));

    await act(async () => {
      handlers["remote.device.state"]({
        id: 1,
        name: "a",
        online: true,
        lastSeenAt: 12345,
        lastError: "",
      });
    });

    const rowOf = (id: number) =>
      result.current.devices.find((d) => d.lan?.id === id);
    expect(rowOf(1)?.online).toBe(true);
    expect(rowOf(1)?.lastSeenAt).toBe(12345);
    // 在线态变化后路径重算:LAN 直连从 dead 翻成 in-use。
    expect(rowOf(1)?.paths).toEqual([{ kind: "lan", state: "in-use" }]);
    expect(rowOf(2)?.online).toBe(false);
  });

  it("ignores events for unknown id", async () => {
    mockList.mockResolvedValueOnce([
      { id: 1, name: "a", online: false, lastSeenAt: 0, lastError: "" },
    ]);
    const handlers: Record<string, (p: unknown) => void> = {};
    mockEventsOn.mockImplementation(
      (name: string, fn: (p: unknown) => void) => {
        handlers[name] = fn;
        return () => {};
      },
    );
    const { result } = renderHook(() => useRemoteDevices());
    await waitFor(() => expect(result.current.loadState).toBe("ready"));
    await act(async () => {
      handlers["remote.device.state"]({
        id: 999,
        name: "?",
        online: true,
        lastSeenAt: 1,
        lastError: "",
      });
    });
    expect(result.current.devices).toHaveLength(1);
    expect(result.current.devices[0].lan?.id).toBe(1);
  });
});
