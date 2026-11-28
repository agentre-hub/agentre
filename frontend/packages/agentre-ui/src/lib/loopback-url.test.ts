import { describe, expect, it } from "vitest";

import { loopbackURLPort } from "./loopback-url";

describe("loopbackURLPort", () => {
  it.each([
    ["http://localhost:3000/x", 3000],
    ["http://127.0.0.1:5173/", 5173],
    ["http://[::1]:8080/", 8080],
    // 开发服务器常打印「绑定全部网卡」的地址，访问的仍是执行设备自己的端口。
    ["http://0.0.0.0:8000/", 8000],
    ["http://[::]:9000/", 9000],
    ["http://localhost/", 80],
    ["https://localhost/", 443],
  ])("Given %s, Then it is the loopback port %i", (url, port) => {
    expect(loopbackURLPort(url)).toBe(port);
  });

  it.each([
    "https://example.com/",
    "http://192.168.1.2:3000/",
    "ftp://localhost:21/",
    "file:///etc/hosts",
    "not a url",
  ])("Given %s, Then it is not a loopback http(s) URL", (url) => {
    expect(loopbackURLPort(url)).toBeNull();
  });
});
