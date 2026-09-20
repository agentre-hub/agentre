import { describe, expect, it } from "vitest";

import { credentialDeviceGate, credentialGateMessage } from "./credential-gate";

const t = (key: string) => key;

describe("credentialDeviceGate", () => {
  it("Given a host with a local machine, When the bound device is local, Then the gate clears", () => {
    expect(
      credentialDeviceGate({
        hasLocalDevice: true,
        deviceId: "",
        selectedDeviceValue: "__local_device__",
        localSelectValue: "__local_device__",
        deviceOptions: [],
      }),
    ).toBe("");
  });

  it("Given a host without a local machine, When no device is selected, Then the gate is deviceRequired", () => {
    expect(
      credentialDeviceGate({
        hasLocalDevice: false,
        deviceId: "",
        selectedDeviceValue: "",
        localSelectValue: "",
        deviceOptions: [{ value: "fp-a", name: "a", online: true }],
      }),
    ).toBe("deviceRequired");
  });

  it("Given a selected device absent from the account's device list, Then the gate is deviceUnknown", () => {
    expect(
      credentialDeviceGate({
        hasLocalDevice: false,
        deviceId: "fp-revoked",
        selectedDeviceValue: "fp-revoked",
        localSelectValue: "",
        deviceOptions: [{ value: "fp-a", name: "a", online: true }],
      }),
    ).toBe("deviceUnknown");
  });

  it("Given a selected device that is offline, Then the gate is deviceOffline", () => {
    expect(
      credentialDeviceGate({
        hasLocalDevice: false,
        deviceId: "fp-a",
        selectedDeviceValue: "fp-a",
        localSelectValue: "",
        deviceOptions: [{ value: "fp-a", name: "a", online: false }],
      }),
    ).toBe("deviceOffline");
  });

  it("Given a selected device that is online, Then the gate clears", () => {
    expect(
      credentialDeviceGate({
        hasLocalDevice: false,
        deviceId: "fp-a",
        selectedDeviceValue: "fp-a",
        localSelectValue: "",
        deviceOptions: [{ value: "fp-a", name: "a", online: true }],
      }),
    ).toBe("");
  });
});

describe("credentialGateMessage", () => {
  it("maps each gate reason to non-empty shared copy, and the clear gate to nothing", () => {
    expect(credentialGateMessage("deviceRequired", t)).not.toBe("");
    expect(credentialGateMessage("deviceOffline", t)).not.toBe("");
    expect(credentialGateMessage("deviceUnknown", t)).not.toBe("");
    expect(credentialGateMessage("", t)).toBe("");
  });
});
