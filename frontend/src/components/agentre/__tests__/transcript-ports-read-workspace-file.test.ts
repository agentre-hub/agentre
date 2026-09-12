import { beforeEach, describe, expect, it, vi } from "vitest";

// 打在生产路径上：桩的是真实 Wails 绑定，断言的是宿主适配器自己的转换。
const readFileMock = vi.fn();
vi.mock("../../../../wailsjs/go/app/App", () => ({
  WorkspaceFsReadFile: (sessionId: number, root: string, relPath: string) =>
    readFileMock(sessionId, root, relPath),
}));

import { desktopTranscriptPorts } from "../transcript-ports-desktop";

beforeEach(() => {
  readFileMock.mockReset();
});

describe("desktopTranscriptPorts.readWorkspaceFile", () => {
  it("passes a readable file through with its view flags", async () => {
    readFileMock.mockResolvedValue({
      content: "hello",
      contentType: "text/plain",
      binary: false,
      tooLarge: false,
    });

    await expect(
      desktopTranscriptPorts.readWorkspaceFile(7, "a.txt"),
    ).resolves.toMatchObject({ content: "hello", contentType: "text/plain" });
    expect(readFileMock).toHaveBeenCalledWith(7, "", "a.txt");
  });

  // 这个端口的消费者（转录里的内联图片）只需要「成不成」。服务层把读不到的原因
  // 改成视图字段之后，这里必须把它**还原成一次失败**，否则图片会拿到一个空 body
  // 当成功渲染 —— 预览那条路径的改动不该外溢到这里。
  it("still rejects when the file is gone", async () => {
    readFileMock.mockResolvedValue({ content: "", unavailable: "not-found" });

    await expect(
      desktopTranscriptPorts.readWorkspaceFile(7, "ghost.png"),
    ).rejects.toThrow();
  });

  it("still rejects when the peer is offline", async () => {
    readFileMock.mockResolvedValue({ content: "", unavailable: "offline" });

    await expect(
      desktopTranscriptPorts.readWorkspaceFile(7, "shot.png"),
    ).rejects.toThrow();
  });
});
