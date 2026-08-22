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

  it("wails 抛错时交 unknown 并把原文带上，不去猜是哪一类", async () => {
    // Go 那侧其实分好了类（code.RemoteFsPermDenied…），但跨 wails 只剩一句本地化
    // 文本，没有码可读。按文案反猜分类一改文案就静默失灵，所以如实说分不出。
    appMocks.RemoteFsListDir.mockRejectedValue(new Error("远端权限不足"));
    const outcome = await createRemoteFsPort().listDir("dev-1", "/root");
    expect(outcome.ok).toBe(false);
    if (outcome.ok) return;
    expect(outcome.failure.kind).toBe("unknown");
    expect(outcome.failure.message).toContain("远端权限不足");
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

  it("mkdir 失败同样交 unknown", async () => {
    appMocks.RemoteFsMkdir.mockRejectedValue(new Error("目标已存在"));
    const outcome = await createRemoteFsPort().mkdir(
      "dev-1",
      "/srv/work",
      "edge",
    );
    expect(outcome.ok).toBe(false);
    if (outcome.ok) return;
    expect(outcome.failure.kind).toBe("unknown");
  });
});
