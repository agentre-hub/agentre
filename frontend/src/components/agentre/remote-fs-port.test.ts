import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  RemoteFsListDir: vi.fn(),
  RemoteFsMkdir: vi.fn(),
}));

vi.mock("../../../wailsjs/go/app/App", () => appMocks);

import { createRemoteFsPort } from "./remote-fs-port";

/**
 * 桌面端这一侧的 adapter（规格 2026-08-22 D 段）。
 *
 * 共用的那份选择器已经在包里测过了；这里只测**接缝**——wails 那两个绑定翻成
 * `ProjectFsPort` 时有没有翻对。包的测试全绿也证明不了这一层接对了
 * （`docs/frontend.md`：a green package suite alone does not prove either host wired
 * the ports correctly）。
 */
describe("桌面端的 ProjectFsPort", () => {
  beforeEach(() => vi.clearAllMocks());

  it("`.git` 是「当前目录是仓库」的判据，不进可选列表", async () => {
    appMocks.RemoteFsListDir.mockResolvedValue({
      path: "/srv/work/atlas",
      truncated: false,
      entries: [
        { name: ".git", isDir: true, size: 0, mtime: 0 },
        { name: "cmd", isDir: true, size: 0, mtime: 0 },
      ],
    });
    const outcome = await createRemoteFsPort().listDir(
      "dev-1",
      "/srv/work/atlas",
    );
    expect(outcome.ok).toBe(true);
    if (!outcome.ok) return;
    expect(outcome.result.isGitRepo).toBe(true);
    expect(outcome.result.entries.map((e) => e.name)).toEqual(["cmd"]);
  });

  it("没有 .git 就不是仓库", async () => {
    appMocks.RemoteFsListDir.mockResolvedValue({
      path: "/srv/work",
      truncated: false,
      entries: [{ name: "atlas", isDir: true, size: 0, mtime: 0 }],
    });
    const outcome = await createRemoteFsPort().listDir("dev-1", "/srv/work");
    expect(outcome.ok && outcome.result.isGitRepo).toBe(false);
  });

  it("符号链接与截断照实带过去", async () => {
    appMocks.RemoteFsListDir.mockResolvedValue({
      path: "/srv",
      truncated: true,
      entries: [
        { name: "shared", isDir: true, symlink: true, size: 0, mtime: 0 },
      ],
    });
    const outcome = await createRemoteFsPort().listDir("dev-1", "/srv");
    expect(outcome.ok).toBe(true);
    if (!outcome.ok) return;
    expect(outcome.result.truncated).toBe(true);
    expect(outcome.result.entries[0].symlink).toBe(true);
  });

  /*
    Go 那侧一直分好了类（code.RemoteFsPermDenied…），只是码过不了 wails 那座桥
    —— `httputils.Error.Error()` 只返回 Msg。现在 internal/app 那层把它写成
    `agentre-code:<码> <原文>`（coded_error.go），这一侧照码分类。

    认的是**码**不是文案：反猜文案一改就静默失灵，而且中英要各猜一遍。
  */
  const CODED: [number, string][] = [
    [20600, "refused"],
    [20601, "denied"],
    [20602, "notFound"],
    [20603, "notDir"],
    [20604, "disconnected"],
    [20605, "exists"],
    [20606, "invalidName"],
  ];

  for (const [code, kind] of CODED) {
    it(`带码 ${code} 的错误落到 ${kind}，出路才说得具体`, async () => {
      appMocks.RemoteFsListDir.mockRejectedValue(
        new Error(`agentre-code:${code} 远端某句话`),
      );
      const outcome = await createRemoteFsPort().listDir("dev-1", "/root");
      expect(outcome.ok).toBe(false);
      if (outcome.ok) return;
      expect(outcome.failure.kind).toBe(kind);
    });
  }

  it("认不出的码仍交 unknown，并把原文带上 —— 编一个类比说「不知道」更糟", async () => {
    appMocks.RemoteFsListDir.mockRejectedValue(
      new Error("agentre-code:19999 某个还没映射的码"),
    );
    const outcome = await createRemoteFsPort().listDir("dev-1", "/root");
    expect(outcome.ok).toBe(false);
    if (outcome.ok) return;
    expect(outcome.failure.kind).toBe("unknown");
    expect(outcome.failure.message).toContain("某个还没映射的码");
  });

  it("没有前缀的错误照旧交 unknown + 原文", async () => {
    appMocks.RemoteFsListDir.mockRejectedValue(new Error("dial tcp: refused"));
    const outcome = await createRemoteFsPort().listDir("dev-1", "/root");
    expect(outcome.ok).toBe(false);
    if (outcome.ok) return;
    expect(outcome.failure.kind).toBe("unknown");
    expect(outcome.failure.message).toContain("dial tcp: refused");
  });

  it("前缀被剥掉，用户看不到 agentre-code 这种内部记号", async () => {
    appMocks.RemoteFsListDir.mockRejectedValue(
      new Error("agentre-code:19999 某个还没映射的码"),
    );
    const outcome = await createRemoteFsPort().listDir("dev-1", "/root");
    if (outcome.ok) return;
    expect(outcome.failure.message).not.toContain("agentre-code");
  });

  it("mkdir 把三个参数原样递给那台机器", async () => {
    appMocks.RemoteFsMkdir.mockResolvedValue({});
    const outcome = await createRemoteFsPort().mkdir(
      "dev-1",
      "/srv/work",
      "edge",
    );
    expect(appMocks.RemoteFsMkdir).toHaveBeenCalledWith(
      "dev-1",
      "/srv/work",
      "edge",
    );
    expect(outcome.ok).toBe(true);
  });

  it("mkdir 重名带回 exists —— 那一档的出路是换个名字，不是重试", async () => {
    appMocks.RemoteFsMkdir.mockRejectedValue(
      new Error("agentre-code:20605 同名目录已存在"),
    );
    const outcome = await createRemoteFsPort().mkdir(
      "dev-1",
      "/srv/work",
      "edge",
    );
    expect(outcome.ok).toBe(false);
    if (outcome.ok) return;
    expect(outcome.failure.kind).toBe("exists");
  });
});
