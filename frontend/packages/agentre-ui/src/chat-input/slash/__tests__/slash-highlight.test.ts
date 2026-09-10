import Document from "@tiptap/extension-document";
import HardBreak from "@tiptap/extension-hard-break";
import Paragraph from "@tiptap/extension-paragraph";
import Text from "@tiptap/extension-text";
import { Editor } from "@tiptap/core";
import type { DecorationSet } from "@tiptap/pm/view";
import { describe, expect, it } from "vitest";

import { findValidSlashRanges, SlashHighlight } from "../slash-highlight";

// findValidSlashRanges 在段落纯文本里找出 token 完整等于 validNames 中某项的
// /command 区间。边界规则与 detectSlashTrigger 一致：左侧行首或空白；右侧行尾
// 或空白；token 字符 [a-zA-Z][a-zA-Z0-9_-]*。
//
// 大小写敏感(与 popover filterByQuery 不同):popover 是“补全建议”可以模糊,
// 高亮是“已确认是这个命令”，必须严格等于注册名。
describe("findValidSlashRanges", () => {
  const valid = new Set(["compact"]);

  it("完整匹配命中：/compact 整段亮", () => {
    expect(findValidSlashRanges("/compact", valid)).toEqual([
      { from: 0, to: 8 },
    ]);
  });

  it("不完整不亮：/compac 比注册名短", () => {
    expect(findValidSlashRanges("/compac", valid)).toEqual([]);
  });

  it("token 后接其他字母不亮：/compactx", () => {
    expect(findValidSlashRanges("/compactx", valid)).toEqual([]);
  });

  it("词内 / 不当作命令：/foo/compact", () => {
    expect(findValidSlashRanges("/foo/compact", valid)).toEqual([]);
  });

  it("命令后跟空格 + 参数：只亮 /compact 部分", () => {
    expect(findValidSlashRanges("/compact arg", valid)).toEqual([
      { from: 0, to: 8 },
    ]);
  });

  it("同段落两次命中：返回两个 range", () => {
    expect(findValidSlashRanges("/compact /compact", valid)).toEqual([
      { from: 0, to: 8 },
      { from: 9, to: 17 },
    ]);
  });

  it("validNames 为空集 → 空", () => {
    expect(findValidSlashRanges("/compact", new Set())).toEqual([]);
  });

  it("未注册命令不亮：/unknown", () => {
    expect(findValidSlashRanges("/unknown", valid)).toEqual([]);
  });

  it("大小写敏感：/Compact 不亮", () => {
    expect(findValidSlashRanges("/Compact", valid)).toEqual([]);
  });

  it("命令前导文字：hello /compact", () => {
    expect(findValidSlashRanges("hello /compact", valid)).toEqual([
      { from: 6, to: 14 },
    ]);
  });

  it("命令前是 tab 也算空白边界", () => {
    expect(findValidSlashRanges("\t/compact", valid)).toEqual([
      { from: 1, to: 9 },
    ]);
  });

  it("空字符串 → 空", () => {
    expect(findValidSlashRanges("", valid)).toEqual([]);
  });

  it("仅一个 / 不算命令", () => {
    expect(findValidSlashRanges("/", valid)).toEqual([]);
  });
});

// 回归:buildDecorations 曾直接用 node.textContent 当字符串下标去建 Decoration。
// textContent 和 textBetween(不传 leafText)一样,给 hardBreak/mention 等 leaf
// 节点 0 个字符,但每个 leaf 在文档里仍占 1 个位置 —— 字符串下标和文档位置按
// 前面出现过的 leaf 数逐个错位,decoration 会画到错误的区间上。
// 见 use-slash-menu.ts / use-mention-menu.ts 的 leafText 注释,同一根因第三处。
describe("SlashHighlight decorations（文档位置对齐）", () => {
  it("hardBreak 之后的 /compact 高亮落在真实文档位置上，而不是 textContent 下标", () => {
    const editor = new Editor({
      extensions: [
        Document,
        Paragraph,
        Text,
        HardBreak,
        SlashHighlight.configure({
          getValidNames: () => new Set(["compact"]),
        }),
      ],
      content: {
        type: "doc",
        content: [
          {
            type: "paragraph",
            content: [
              { type: "text", text: " a" },
              { type: "hardBreak" },
              { type: "text", text: " /compact" },
            ],
          },
        ],
      },
    });

    const plugin = editor.state.plugins.find(
      (p) => typeof p.props.decorations === "function",
    );
    expect(plugin).toBeDefined();
    const decoSet = plugin!.getState(editor.state) as DecorationSet;
    const decos = decoSet.find();

    // 段落在 doc 中 pos=0，内容起点 base=1；" a"(2 字符)+hardBreak(1 位置)+
    // " /compact" 中，"/compact" 真实文档位置是 [5,13)。
    // 若仍用 node.textContent(丢掉 hardBreak 的 1 个字符)会算出 [4,12)，
    // 对应文档区间 " /compac"（少了 hardBreak 占的 1 个位置，多吞前一个空格
    // 且漏掉结尾的 "t"）。
    expect(decos).toHaveLength(1);
    expect(decos[0]!.from).toBe(5);
    expect(decos[0]!.to).toBe(13);

    editor.destroy();
  });
});
