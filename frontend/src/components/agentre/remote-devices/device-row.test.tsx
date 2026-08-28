// frontend/src/components/agentre/remote-devices/device-row.test.tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { DeviceRow } from "./device-row";
import type { DeviceRowModel, DeviceView } from "./use-remote-devices";

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
  unclaimed: false,
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

  // ── 未认领标注 + 认领动作 ──────────────────────────────────────────────────
  it("marks an unclaimed device and explains the consequence", () => {
    const d: DeviceRowModel = { ...baseDevice, unclaimed: true };
    renderRow(d);
    expect(screen.getByText("Unclaimed")).toBeInTheDocument();
    expect(screen.getByText(/other devices can't see it/i)).toBeInTheDocument();
  });

  it("does not show the unclaimed marking on a claimed device", () => {
    renderRow(baseDevice);
    expect(screen.queryByText("Unclaimed")).not.toBeInTheDocument();
  });

  it("reveals claim instructions when the claim action is pressed", async () => {
    const user = userEvent.setup();
    const d: DeviceRowModel = { ...baseDevice, unclaimed: true };
    renderRow(d);
    expect(screen.queryByText(/agentred login/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Claim to account" }));
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
      unclaimed: false,
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
});
