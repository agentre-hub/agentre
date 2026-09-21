import { describe, expect, it } from "vitest";

import {
  formatPortForwardTarget,
  isExplicitHttpsTarget,
  isLikelyValidPortForwardTarget,
  parsePortForwardTarget,
} from "./target";

describe("parsePortForwardTarget", () => {
  it("Given a loopback target, When parsed, Then host/port/scheme come back", () => {
    expect(parsePortForwardTarget("http://127.0.0.1:3000")).toEqual({
      scheme: "http",
      host: "127.0.0.1",
      port: 3000,
    });
  });

  it("Given an https target without an explicit port, When parsed, Then the default port 443 is filled in", () => {
    expect(parsePortForwardTarget("https://example.com")).toEqual({
      scheme: "https",
      host: "example.com",
      port: 443,
    });
  });

  it("Given something unparseable, When parsed, Then it gives null instead of guessing", () => {
    expect(parsePortForwardTarget("not a url")).toBeNull();
  });

  it("Given a non-http(s) scheme, When parsed, Then it gives null", () => {
    expect(parsePortForwardTarget("ftp://example.com:21")).toBeNull();
  });
});

describe("formatPortForwardTarget", () => {
  it("Given a loopback target, When formatted, Then only the port shows", () => {
    expect(formatPortForwardTarget("http://127.0.0.1:3000")).toBe("3000");
  });

  // 只显示端口的只有纯端口简写展开出来的那一种(http://127.0.0.1:<端口>):同一台设备
  // 上 `8080`、`https://127.0.0.1:8080`、`http://[::1]:8080` 是三条互不冲突的映射,
  // 都缩成「8080」就分不出是哪一条了。
  it("Given a loopback target that is not the port shorthand, When formatted, Then the full target shows", () => {
    expect(formatPortForwardTarget("https://127.0.0.1:8443")).toBe(
      "https://127.0.0.1:8443",
    );
    expect(formatPortForwardTarget("http://[::1]:8080")).toBe(
      "http://[::1]:8080",
    );
  });

  it("Given a LAN target, When formatted, Then the full normalized target shows", () => {
    expect(formatPortForwardTarget("https://192.168.1.5:8443")).toBe(
      "https://192.168.1.5:8443",
    );
  });

  it("Given a target that fails to parse, When formatted, Then it is shown as-is", () => {
    expect(formatPortForwardTarget("garbage")).toBe("garbage");
  });
});

describe("isExplicitHttpsTarget", () => {
  it("Given an explicit https target, Then it is recognized regardless of case or surrounding whitespace", () => {
    expect(isExplicitHttpsTarget("https://example.com")).toBe(true);
    expect(isExplicitHttpsTarget("HTTPS://example.com")).toBe(true);
    expect(isExplicitHttpsTarget("  https://example.com  ")).toBe(true);
  });

  it("Given a target without an explicit https scheme, Then it is not recognized", () => {
    expect(isExplicitHttpsTarget("http://example.com")).toBe(false);
    expect(isExplicitHttpsTarget("3000")).toBe(false);
    expect(isExplicitHttpsTarget("example.com:3000")).toBe(false);
  });
});

describe("isLikelyValidPortForwardTarget", () => {
  it("Given a bare port, Then it is accepted", () => {
    expect(isLikelyValidPortForwardTarget("3000")).toBe(true);
  });

  it("Given a bare port out of range, Then it is rejected", () => {
    expect(isLikelyValidPortForwardTarget("70000")).toBe(false);
    expect(isLikelyValidPortForwardTarget("0")).toBe(false);
  });

  it("Given host:port, Then it is accepted", () => {
    expect(isLikelyValidPortForwardTarget("192.168.1.5:8080")).toBe(true);
  });

  it("Given host:port with an out-of-range port, Then it is rejected", () => {
    expect(isLikelyValidPortForwardTarget("192.168.1.5:99999")).toBe(false);
  });

  it("Given http(s)://host[:port] with the port omitted, Then it is accepted", () => {
    expect(isLikelyValidPortForwardTarget("https://example.com")).toBe(true);
    expect(isLikelyValidPortForwardTarget("http://example.com:8080")).toBe(
      true,
    );
  });

  it("Given a target with a path, query string or userinfo, Then it is rejected", () => {
    expect(isLikelyValidPortForwardTarget("http://example.com/path")).toBe(
      false,
    );
    expect(isLikelyValidPortForwardTarget("http://example.com?x=1")).toBe(
      false,
    );
    expect(isLikelyValidPortForwardTarget("http://user:pass@example.com")).toBe(
      false,
    );
  });

  it("Given a non-http(s) scheme, Then it is rejected", () => {
    expect(isLikelyValidPortForwardTarget("ftp://example.com")).toBe(false);
  });

  it("Given an empty string, Then it is rejected", () => {
    expect(isLikelyValidPortForwardTarget("")).toBe(false);
    expect(isLikelyValidPortForwardTarget("   ")).toBe(false);
  });
});
