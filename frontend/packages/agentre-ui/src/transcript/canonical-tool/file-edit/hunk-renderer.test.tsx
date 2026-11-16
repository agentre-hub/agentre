import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import { FileBlock } from "./hunk-renderer";
import type { FileEditPatch } from "../types";

// hunk-renderer 是 file.edit 的**正文**渲染器，活动行的就地展开体直接用它
// （spec 决策 13）。这里的用例原本挂在已删除的 FileEditCard 上 —— 卡壳没了，
// 但正文还活着，覆盖跟着正文走。
describe("FileBlock", () => {
  it("renders the file header with path and +/- counts", () => {
    const file = {
      path: "/root/app/x.ts",
      kind: "modified",
      hunks: [],
      plus: 3,
      minus: 1,
    } as unknown as FileEditPatch;
    render(<FileBlock file={file} showHeader />);
    expect(screen.getByText("/root/app/x.ts")).toBeDefined();
    expect(screen.getByText("+3")).toBeDefined();
    expect(screen.getByText("−1")).toBeDefined();
  });

  // Given 新建 / 删除 / 修改三种文件，When 画文件名条，Then 新文件前是 file-plus、
  // 删除的是 file-x 且路径划线，修改的不挂标记，英文胶囊都不再出现。
  describe("kind marks", () => {
    const mk = (kind: string) =>
      ({
        path: "/root/app/x.ts",
        kind,
        hunks: [],
        plus: 1,
        minus: 0,
      }) as unknown as FileEditPatch;

    it("created: file-plus mark before the path, no NEW pill", () => {
      render(<FileBlock file={mk("created")} showHeader />);
      const mark = screen.getByRole("img", { name: "New file" });
      expect(mark).toHaveAttribute("data-state", "fileNew");
      const path = screen.getByText("/root/app/x.ts");
      expect(
        mark.compareDocumentPosition(path) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
      expect(path.className).not.toContain("line-through");
      expect(screen.queryByText("NEW")).toBeNull();
    });

    it("deleted: file-x mark before a struck-through path, no DELETED pill", () => {
      render(<FileBlock file={mk("deleted")} showHeader />);
      const mark = screen.getByRole("img", { name: "Deleted" });
      expect(mark).toHaveAttribute("data-state", "fileDeleted");
      const path = screen.getByText("/root/app/x.ts");
      expect(
        mark.compareDocumentPosition(path) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
      expect(path.className).toContain("line-through");
      expect(screen.queryByText("DELETED")).toBeNull();
    });

    it("modified: no mark and the path is not struck", () => {
      render(<FileBlock file={mk("modified")} showHeader />);
      expect(screen.queryByRole("img")).toBeNull();
      expect(screen.getByText("/root/app/x.ts").className).not.toContain(
        "line-through",
      );
    });
  });

  // Given 一行超出容器宽度的 diff，When 渲染，Then diff 区可横向滚动
  // 且行盒撑到内容宽度（否则 +/- 背景条在滚动后断在容器边缘）。
  it("scrolls long diff lines horizontally instead of clipping them", () => {
    const longText = `const x = "${"y".repeat(400)}";`;
    const file = {
      path: "/root/app/x.ts",
      kind: "modified",
      plus: 1,
      minus: 0,
      hunks: [
        {
          oldStart: 1,
          oldLines: 0,
          newStart: 1,
          newLines: 1,
          lines: [{ op: "+", new: 1, text: longText }],
        },
      ],
    } as unknown as FileEditPatch;
    render(<FileBlock file={file} showHeader={false} />);

    const scroller = screen.getByTestId("file-edit-diff-scroll");
    expect(scroller.className).toContain("overflow-x-auto");

    const row = screen.getByText(longText).parentElement as HTMLElement;
    expect(scroller).toContainElement(row);
    expect(row.className).toContain("w-max");
    expect(row.className).toContain("min-w-full");
  });

  it("says so when a file carries no hunks", () => {
    const file = {
      path: "/a.ts",
      kind: "modified",
      hunks: [],
      plus: 0,
      minus: 0,
    } as unknown as FileEditPatch;
    render(<FileBlock file={file} showHeader={false} />);
    expect(screen.getByText("No actual changes")).toBeDefined();
  });
});
