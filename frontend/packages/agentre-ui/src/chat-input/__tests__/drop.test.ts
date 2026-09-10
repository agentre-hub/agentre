import { describe, expect, it } from "vitest";

import {
  classifyDroppedPaths,
  formatPathsForInput,
  resolveDroppedPaths,
  type DroppedImageItem,
} from "../drop";

describe("classifyDroppedPaths", () => {
  it("按扩展名分流图片与其余(含文件夹)", () => {
    const { imageCandidates, plainPaths } = classifyDroppedPaths([
      "/a/x.PNG",
      "/a/y.jpeg",
      "/a/doc.pdf",
      "/a/project", // 文件夹,无扩展名
      "/a/archive.tar.gz",
    ]);
    expect(imageCandidates).toEqual(["/a/x.PNG", "/a/y.jpeg"]);
    expect(plainPaths).toEqual([
      "/a/doc.pdf",
      "/a/project",
      "/a/archive.tar.gz",
    ]);
  });
});

describe("formatPathsForInput", () => {
  it("空数组返回空串", () => {
    expect(formatPathsForInput([])).toBe("");
  });
  it("含空格的路径加双引号,空格分隔,末尾补空格", () => {
    expect(formatPathsForInput(["/a/b.txt", "/c d/e.txt"])).toBe(
      `/a/b.txt "/c d/e.txt" `,
    );
  });
});

describe("resolveDroppedPaths", () => {
  const imageItem = (path: string): DroppedImageItem => ({
    path,
    kind: "image",
    name: path.split("/").pop(),
    mediaType: "image/png",
    dataUrl: "data:image/png;base64,AAAA",
  });

  it("allowImages=false 时图片也降级为路径", async () => {
    const readImages = async () => [];
    const res = await resolveDroppedPaths(["/a/x.png", "/a/y.pdf"], {
      allowImages: false,
      remainingImageSlots: 4,
      readImages,
    });
    expect(res.attachments).toHaveLength(0);
    expect(res.text).toBe(`/a/y.pdf /a/x.png `);
  });

  it("图片在配额内 → 附件,不进插入文本", async () => {
    const res = await resolveDroppedPaths(["/a/x.png", "/a/doc.pdf"], {
      allowImages: true,
      remainingImageSlots: 4,
      readImages: async (p) => p.map(imageItem),
    });
    expect(res.attachments).toEqual([
      {
        dataUrl: "data:image/png;base64,AAAA",
        mediaType: "image/png",
        name: "x.png",
      },
    ]);
    expect(res.text).toBe(`/a/doc.pdf `);
  });

  it("后端把图片判成 path → 降级插入路径", async () => {
    const res = await resolveDroppedPaths(["/a/x.png"], {
      allowImages: true,
      remainingImageSlots: 4,
      readImages: async (p) =>
        p.map((path) => ({ path, kind: "path" as const })),
    });
    expect(res.attachments).toHaveLength(0);
    expect(res.text).toBe(`/a/x.png `);
  });

  it("配额溢出的图片降级为路径", async () => {
    const res = await resolveDroppedPaths(
      ["/a/1.png", "/a/2.png", "/a/3.png"],
      {
        allowImages: true,
        remainingImageSlots: 2,
        readImages: async (p) => p.map(imageItem),
      },
    );
    expect(res.attachments).toHaveLength(2);
    expect(res.text).toBe(`/a/3.png `);
  });
});
