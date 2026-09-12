import { describe, expect, it } from "vitest";

import { buildDeviceMentionItems } from "./mention-items";
import { mergeDeviceSources, type DeviceView } from "./use-remote-devices";
import type { server_svc } from "../../../../wailsjs/go/models";

const lanDevice = (over: Partial<DeviceView> = {}): DeviceView => ({
  id: 1,
  name: "linux-srv",
  url: "ws://192.168.1.100:7456/rpc",
  daemonFingerprint: "sha256:lan",
  instanceUUID: "u1",
  tlsMode: "default",
  tlsCertPEM: "",
  pairedAt: 1,
  lastSeenAt: 1_700_000_000_000,
  lastError: "",
  online: true,
  ...over,
});

const accountDevice = (
  over: Partial<server_svc.Device> = {},
): server_svc.Device => ({
  id: 10,
  name: "linux-srv",
  kind: "agentred",
  platform: "linux",
  version: "0.3.0",
  fingerprint: "sha256:lan",
  lastSeenAt: 1_700_000_000_000,
  status: 1,
  online: true,
  isThisDevice: false,
  ...over,
});

describe("buildDeviceMentionItems", () => {
  it("Given this desktop, paired agentreds and a peer desktop, When building the @ list, Then this machine leads and every machine is named by fingerprint", () => {
    const rows = mergeDeviceSources(
      [
        lanDevice(),
        lanDevice({
          id: 2,
          name: "NAS",
          daemonFingerprint: "sha256:nas",
          online: false,
        }),
      ],
      {
        known: true,
        devices: [
          accountDevice(),
          accountDevice({
            id: 11,
            name: "MacBook",
            kind: "desktop",
            fingerprint: "sha256:self",
            isThisDevice: true,
          }),
          accountDevice({
            id: 12,
            name: "Studio",
            kind: "desktop",
            fingerprint: "sha256:peer",
            online: true,
          }),
        ],
      },
    );

    expect(
      buildDeviceMentionItems({
        rows,
        accountDevices: [
          accountDevice(),
          accountDevice({
            id: 11,
            name: "MacBook",
            kind: "desktop",
            fingerprint: "sha256:self",
            isThisDevice: true,
          }),
          accountDevice({
            id: 12,
            name: "Studio",
            kind: "desktop",
            fingerprint: "sha256:peer",
            online: true,
          }),
        ],
        selfFingerprint: "sha256:self",
        selfFallbackName: "本机",
      }),
    ).toEqual([
      { fp: "sha256:self", name: "MacBook", online: true },
      { fp: "sha256:lan", name: "linux-srv", online: true },
      { fp: "sha256:nas", name: "NAS", online: false },
      { fp: "sha256:peer", name: "Studio", online: true },
    ]);
  });

  // 没登录账号时拿不到本机的账号名,但这台机器仍然是可 @ 的 —— 指纹在本地就有。
  it("Given no account list, When building the @ list, Then this machine still leads under the fallback name", () => {
    expect(
      buildDeviceMentionItems({
        rows: mergeDeviceSources([lanDevice()], { known: false, devices: [] }),
        accountDevices: [],
        selfFingerprint: "sha256:self",
        selfFallbackName: "本机",
      }),
    ).toEqual([
      { fp: "sha256:self", name: "本机", online: true },
      { fp: "sha256:lan", name: "linux-srv", online: true },
    ]);
  });

  // 指纹是设备提及在正文里的**唯一**身份(见 agentre-ui 的 MentionRef.fp)。
  // 没有指纹就写不出一个指得回这台机器的引用,与其发一个 fp="" 的空壳,不如不列。
  it("Given a machine with no fingerprint, When building the @ list, Then it is left out instead of being referenced by an empty identity", () => {
    expect(
      buildDeviceMentionItems({
        rows: mergeDeviceSources([lanDevice({ daemonFingerprint: "" })], {
          known: false,
          devices: [],
        }),
        accountDevices: [],
        selfFingerprint: "",
        selfFallbackName: "本机",
      }),
    ).toEqual([]);
  });
});
