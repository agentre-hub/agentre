import { describe, expect, it, vi } from "vitest";

/**
 * desktop 端口把 BackendItem 显式挑成 BackendView：漏一个字段不会报错，
 * 只会让编辑弹窗把已经存进 config_json 的值显示成空（写进去读不回）。
 */
const mocks = vi.hoisted(() => ({
  listAgentBackends: vi.fn(),
  listOverlays: vi.fn(),
  fingerprint: vi.fn(),
  listHermesProviders: vi.fn(),
  loginHermes: vi.fn(),
  logoutHermes: vi.fn(),
}));

vi.mock("../../../wailsjs/go/app/App", () => ({
  ListAgentBackends: mocks.listAgentBackends,
  ListAgentBackendCLIOverlays: mocks.listOverlays,
  RemoteDeviceFingerprint: mocks.fingerprint,
  ListHermesAuthProviders: mocks.listHermesProviders,
  LoginHermesBackend: mocks.loginHermes,
  LogoutHermesBackend: mocks.logoutHermes,
}));

import { createDesktopEngineSettingsPorts } from "../engine-ports-desktop";

describe("desktop 后端的 hermes 字段", () => {
  it("Server URL 要穿过端口映射回到 UI", async () => {
    mocks.listAgentBackends.mockResolvedValue({
      items: [
        {
          id: 1,
          syncId: "01M2JEDJZ7RDRWHV8DXB8VAZMQ",
          name: "local-hermes",
          type: "hermes",
          hermesUrl: "http://127.0.0.1:9119",
        },
      ],
    });
    mocks.listOverlays.mockResolvedValue({ items: [] });
    mocks.fingerprint.mockResolvedValue("sha256:test");

    const rows = await createDesktopEngineSettingsPorts().listBackends();

    expect(rows[0].hermesUrl).toBe("http://127.0.0.1:9119");
  });

  // 与 hermesUrl 同一族的 write-read 回归：登录成功后写进 config_json 的
  // hermesAuthProvider / hermesUserId 必须能被显式映射读回来。
  it("认证 provider 与 userId 要穿过端口映射回到 UI", async () => {
    mocks.listAgentBackends.mockResolvedValue({
      items: [
        {
          id: 2,
          syncId: "01M2JEDJZ7RDRWHV8DXB8VAZMR",
          name: "gated-hermes",
          type: "hermes",
          hermesUrl: "http://10.0.0.8:9119",
          hermesAuthProvider: "basic",
          hermesUserId: "user-7",
        },
      ],
    });
    mocks.listOverlays.mockResolvedValue({ items: [] });
    mocks.fingerprint.mockResolvedValue("sha256:test");

    const rows = await createDesktopEngineSettingsPorts().listBackends();

    expect(rows[0].hermesAuthProvider).toBe("basic");
    expect(rows[0].hermesUserId).toBe("user-7");
  });

  it("登录与退出登录走对应绑定", async () => {
    mocks.loginHermes.mockResolvedValue({
      provider: "basic",
      userId: "user-7",
    });
    mocks.logoutHermes.mockResolvedValue({});
    const ports = createDesktopEngineSettingsPorts();

    const result = await ports.loginHermesBackend?.({
      url: "http://10.0.0.8:9119",
      provider: "basic",
      username: "alice",
      password: "secret",
    });
    expect(result).toEqual({ provider: "basic", userId: "user-7" });

    await ports.logoutHermesBackend?.({ id: 2, url: "http://10.0.0.8:9119" });
    expect(mocks.logoutHermes).toHaveBeenCalledTimes(1);
  });

  it("列出认证 provider 去掉 Wails 包装", async () => {
    mocks.listHermesProviders.mockResolvedValue({
      providers: [
        { name: "basic", displayName: "Basic", supportsPassword: true },
      ],
    });

    const providers =
      await createDesktopEngineSettingsPorts().listHermesAuthProviders?.(
        "http://10.0.0.8:9119",
      );

    expect(providers).toEqual([
      { name: "basic", displayName: "Basic", supportsPassword: true },
    ]);
  });
});
