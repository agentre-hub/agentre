import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  RemoteDeviceList: vi.fn(),
  RemoteDeviceAdd: vi.fn(),
  RemoteDeviceRemove: vi.fn(),
  RemoteDeviceUpdateTLS: vi.fn(),
  RemoteDeviceRefresh: vi.fn(),
  RemoteDeviceRename: vi.fn(),
  RemoteDeviceFingerprint: vi.fn(),
  ServerListDevices: vi.fn(),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: vi.fn(() => vi.fn()),
}));

import {
  RemoteDeviceList,
  RemoteDeviceFingerprint,
  ServerListDevices,
} from "../../../../wailsjs/go/app/App";
import { useDeviceMentionItems } from "./use-device-mentions";
import { useDeviceListStore } from "@/stores/device-list-store";

const mockList = RemoteDeviceList as unknown as ReturnType<typeof vi.fn>;
const mockFingerprint = RemoteDeviceFingerprint as unknown as ReturnType<
  typeof vi.fn
>;
const mockServerList = ServerListDevices as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  // 清单是进程内共享状态,不还原会让上一条用例的快照喂给下一条。
  useDeviceListStore.getState().__reset();
  mockList.mockReset();
  mockFingerprint.mockReset();
  mockServerList.mockReset();
  mockList.mockResolvedValue([
    {
      id: 1,
      name: "linux-srv",
      url: "ws://192.168.1.100:7456/rpc",
      daemonFingerprint: "sha256:lan",
      instanceUUID: "u1",
      tlsMode: "default",
      tlsCertPEM: "",
      pairedAt: 1,
      lastSeenAt: 1,
      lastError: "",
      online: true,
    },
  ]);
  mockServerList.mockRejectedValue(new Error("not logged in"));
  mockFingerprint.mockResolvedValue("sha256:self");
});

describe("useDeviceMentionItems", () => {
  it("Given the paired devices and this desktop's fingerprint, When both have arrived, Then the @ list leads with this machine", async () => {
    const { result } = renderHook(() => useDeviceMentionItems());

    await waitFor(() => expect(result.current).toHaveLength(2));
    expect(result.current[0]).toMatchObject({
      fp: "sha256:self",
      online: true,
    });
    expect(result.current[1]).toMatchObject({
      fp: "sha256:lan",
      name: "linux-srv",
      online: true,
    });
  });

  // chat-panel-host 把每个已打开 tab 都常驻挂载(隐藏而非卸载),每个 tab 各有一个
  // 输入框 —— 每个输入框自己拉一遍设备,就是 project-list-store 当年被咬过的那个
  // N 倍 IPC。清单归一份共享状态,第一个挂载的把它拉起来。
  it("Given several composers mounted at once, When they all ask for the device list, Then the devices are fetched once", async () => {
    const first = renderHook(() => useDeviceMentionItems());
    const second = renderHook(() => useDeviceMentionItems());

    await waitFor(() => expect(first.result.current).toHaveLength(2));
    await waitFor(() => expect(second.result.current).toHaveLength(2));

    expect(mockList).toHaveBeenCalledTimes(1);
    expect(mockFingerprint).toHaveBeenCalledTimes(1);
  });

  // 指纹取不到(尚未 provision / keychain 读失败)时,本机没有可写进正文的身份 ——
  // 那就只少这一行,别把整份清单也一起丢掉。
  it("Given the fingerprint lookup fails, When the devices have loaded, Then the paired devices are still offered without this machine", async () => {
    mockFingerprint.mockRejectedValue(new Error("keychain locked"));

    const { result } = renderHook(() => useDeviceMentionItems());

    await waitFor(() => expect(result.current).toHaveLength(1));
    expect(result.current[0]).toMatchObject({ fp: "sha256:lan" });
  });
});
