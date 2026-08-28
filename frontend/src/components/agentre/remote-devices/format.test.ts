// frontend/src/components/agentre/remote-devices/format.test.ts
import { describe, it, expect } from "vitest";
import type { TFunction } from "i18next";

import {
  relativeTime,
  deriveDeviceName,
  friendlyLastError,
  hostOf,
  friendlyLoginError,
  formatCountdown,
} from "./format";

const t = ((key: string, params?: Record<string, unknown>) => {
  const values: Record<string, string> = {
    // 相对时间的文案已搬进共享包的 agentreUi bundle，key 因此带 namespace 前缀，
    // 插值变量也统一成 i18next 的 count。
    "agentreUi:relativeTime.never": "从未",
    "agentreUi:relativeTime.justNow": "刚刚",
    "agentreUi:relativeTime.minutesAgo": `${params?.count} 分钟前`,
    "agentreUi:relativeTime.hoursAgo": `${params?.count} 小时前`,
    "agentreUi:relativeTime.daysAgo": `${params?.count} 天前`,
    "remoteDevices.errors.tofuMismatch":
      "服务端身份指纹已变化，请确认安全后重新配对",
    "remoteDevices.errors.unauthorized": "凭据已失效，请重新配对",
    "remoteDevices.errors.dialFailed": `连接失败：${params?.message}`,
    "remoteDevices.errors.protocolUnsupported":
      "这台机器上的 agentred 不认识桌面端的通信协议 —— 请重新部署 agentred，让它与桌面端同版本。",
    "remoteDevices.errors.protocolMismatch":
      "这台机器上的 agentred 与桌面端的通信协议版本不一致 —— 请重新部署 agentred，让它与桌面端同版本。",
    "remoteDevices.login.errors.unreachable":
      "无法连接服务器，请检查地址后重试。",
    "remoteDevices.login.errors.accessDenied": "登录已被拒绝。",
    "remoteDevices.login.errors.expired": "验证码已过期，请重新登录。",
    "remoteDevices.login.errors.inProgress": "已有登录流程正在进行。",
    "remoteDevices.login.errors.generic": "登录失败。",
  };
  return values[key] ?? key;
}) as TFunction;

describe("relativeTime", () => {
  it("returns '刚刚' for sub-minute deltas", () => {
    const now = 1_000_000_000_000;
    expect(relativeTime(now - 5_000, now, t)).toBe("刚刚");
  });
  it("formats minutes", () => {
    const now = 1_000_000_000_000;
    expect(relativeTime(now - 3 * 60_000, now, t)).toBe("3 分钟前");
  });
  it("formats days", () => {
    const now = 1_000_000_000_000;
    expect(relativeTime(now - 2 * 86_400_000, now, t)).toBe("2 天前");
  });
  it("returns '从未' for zero", () => {
    expect(relativeTime(0, 1, t)).toBe("从未");
  });
});

describe("deriveDeviceName", () => {
  it("returns hostname segment for FQDN", () => {
    expect(deriveDeviceName("ws://linux-srv.local:7456/rpc", [])).toBe(
      "linux-srv",
    );
  });
  it("returns agentred-N for IP host", () => {
    expect(deriveDeviceName("ws://192.168.1.100:7456/rpc", [])).toBe(
      "agentred-1",
    );
  });
  it("increments N past existing agentred-N names", () => {
    expect(
      deriveDeviceName("ws://10.0.0.5:7456/rpc", [
        { name: "agentred-1" },
        { name: "agentred-2" },
        { name: "other" },
      ]),
    ).toBe("agentred-3");
  });
  it("returns empty for invalid URL", () => {
    expect(deriveDeviceName("garbage", [])).toBe("");
  });
});

describe("friendlyLastError", () => {
  it("translates known sentinels", () => {
    expect(friendlyLastError("tofu_mismatch", t)).toMatch(/fingerprint|身份/);
    expect(friendlyLastError("unauthorized", t)).toMatch(
      /credential|凭据|授权/i,
    );
  });
  it("strips dial_failed prefix", () => {
    expect(friendlyLastError("dial_failed:ECONNREFUSED", t)).toContain(
      "ECONNREFUSED",
    );
  });
  it("returns empty for empty", () => {
    expect(friendlyLastError("", t)).toBe("");
  });
  // 协议不一致与网络失败在面板上必须说两句不同的话:一句让人去重装远端
  // agentred,一句让人去查网络。落到 raw token 上就等于什么都没说。
  it("explains a protocol disagreement instead of echoing the raw token", () => {
    expect(friendlyLastError("protocol_unsupported", t)).toContain("agentred");
    expect(friendlyLastError("protocol_mismatch", t)).toContain("agentred");
    expect(friendlyLastError("protocol_unsupported", t)).not.toBe(
      "protocol_unsupported",
    );
    expect(friendlyLastError("protocol_mismatch", t)).not.toBe(
      "protocol_mismatch",
    );
  });
});

describe("formatCountdown", () => {
  it("formats whole minutes", () => {
    expect(formatCountdown(900)).toBe("15:00");
  });
  it("pads seconds below a minute", () => {
    expect(formatCountdown(65)).toBe("1:05");
    expect(formatCountdown(59)).toBe("0:59");
  });
  it("floors at zero and ignores negatives", () => {
    expect(formatCountdown(0)).toBe("0:00");
    expect(formatCountdown(-5)).toBe("0:00");
  });
  it("rounds fractional seconds down", () => {
    expect(formatCountdown(899.9)).toBe("14:59");
  });
});

describe("hostOf", () => {
  it("extracts host from a full URL", () => {
    expect(hostOf("https://hub.example.com/rpc")).toBe("hub.example.com");
  });
  it("keeps a port suffix", () => {
    expect(hostOf("http://localhost:8080")).toBe("localhost:8080");
  });
  it("falls back to the raw string when parsing fails", () => {
    expect(hostOf("not a url")).toBe("not a url");
  });
});

describe("friendlyLoginError (login error surfacing)", () => {
  it("translates known server_svc sentinel errors", () => {
    expect(friendlyLoginError(new Error("server: unreachable"), t)).toBe(
      "无法连接服务器，请检查地址后重试。",
    );
    expect(friendlyLoginError(new Error("server: access denied"), t)).toBe(
      "登录已被拒绝。",
    );
    expect(
      friendlyLoginError(new Error("server: device code expired"), t),
    ).toBe("验证码已过期，请重新登录。");
    expect(
      friendlyLoginError(new Error("server: login already in progress"), t),
    ).toBe("已有登录流程正在进行。");
  });
  it("falls back to the raw message for unknown errors", () => {
    expect(friendlyLoginError(new Error("boom"), t)).toBe("boom");
  });
  it("falls back to a generic message when there is no message at all", () => {
    expect(friendlyLoginError({}, t)).toBe("登录失败。");
  });
});
