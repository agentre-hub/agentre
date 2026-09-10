import { describe, expect, it } from "vitest";

import { resolveRowOpenAction } from "../file-open-action";

const ABS = "/Users/me/proj/README.md";
const REL = "README.md";

describe("resolveRowOpenAction", () => {
  describe("能拼出绝对路径时（本地会话 + 有 cwd）", () => {
    it("allowlist 内 + 设置为内置预览 → 预览", () => {
      expect(
        resolveRowOpenAction({
          openAction: "preview",
          previewPath: REL,
          absPath: ABS,
        }),
      ).toBe("preview");
    });

    it("allowlist 内 + 设置为外部应用 → 外部打开", () => {
      expect(
        resolveRowOpenAction({
          openAction: "external",
          previewPath: REL,
          absPath: ABS,
        }),
      ).toBe("external");
    });

    it("allowlist 外 → 一律外部打开，与设置无关", () => {
      for (const openAction of ["preview", "external"] as const) {
        expect(
          resolveRowOpenAction({ openAction, previewPath: null, absPath: ABS }),
        ).toBe("external");
      }
    });
  });

  describe("拼不出绝对路径时（远端会话，或会话没有 cwd）", () => {
    it("allowlist 内 + 设置为外部应用 → 退回预览", () => {
      expect(
        resolveRowOpenAction({
          openAction: "external",
          previewPath: REL,
          absPath: null,
        }),
      ).toBe("preview");
    });

    it("allowlist 内 + 设置为内置预览 → 仍是预览", () => {
      expect(
        resolveRowOpenAction({
          openAction: "preview",
          previewPath: REL,
          absPath: null,
        }),
      ).toBe("preview");
    });

    it("allowlist 外 → 没有任何一端能打开它", () => {
      for (const openAction of ["preview", "external"] as const) {
        expect(
          resolveRowOpenAction({
            openAction,
            previewPath: null,
            absPath: null,
          }),
        ).toBe("none");
      }
    });
  });
});
