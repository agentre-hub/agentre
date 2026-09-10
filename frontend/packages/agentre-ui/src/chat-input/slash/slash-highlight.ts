// Slash command 高亮：用 TipTap/ProseMirror Decoration 在输入框中给“已注册命令”
// (token 完整等于某个注册名)的范围加 inline class，让用户敲完命令那一刻立刻
// 看到视觉反馈。
//
// 设计要点：
//   - 不修改 document model —— Decoration 是纯视觉层，拷贝出去的纯文本不带任何标记。
//   - 边界规则与 slash-commands/trigger.ts 的 detectSlashTrigger 一致：
//       / 左侧必须是行首或空白(避免 foo/bar)；
//       token 字符 [a-zA-Z][a-zA-Z0-9_-]*；
//       token 右侧必须是行尾或空白(完整匹配，/compactx 不亮)。
//   - 命令名大小写敏感(与 popover filterByQuery 不同：popover 是“补全建议”，
//     高亮是“已确认是这个命令”)。
//   - validNames 来自闭包 getter，extension 不持有副本；backendType 变化时通过
//     setSlashHighlightRefresh meta 强制重算。
//
// 纯函数 findValidSlashRanges 用于单测覆盖全部边界；Extension 只负责把它接到
// ProseMirror 的 doc 遍历 + DecorationSet 上。

import { Extension } from "@tiptap/core";
import type { Node as PMNode } from "@tiptap/pm/model";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";

// ProseMirror 的 textContent(等价于不传 leafText 的 textBetween)给非文本 leaf
// 节点(hardBreak/mention 等 atom)0 个字符,但每个 leaf 在文档里仍占 1 个位置——
// 于是「字符串下标」与「文档位置」按前面出现过的 leaf 个数逐个错位,decoration
// 会画到错误的区间上。给每个 leaf 恰好 1 个字符即可对齐:hardBreak → "\n"
// (语义即换行),其它 atom(如 mention chip)→ "￼"(不可见占位,视作词内字符)。
// 注:slash-commands/use-slash-menu.ts 与 chat-input/mentions/use-mention-menu.ts
// 各自持有一份同样的 helper —— 各模块刻意不互相依赖。
function leafText(node: PMNode): string {
  return node.type.name === "hardBreak" ? "\n" : "￼";
}

export type SlashRange = { from: number; to: number };

// SLASH_TOKEN_RE 匹配“/”起头的命令 token。全局匹配，按出现位置依次产出；
// 调用方再校验 ① 左边界(行首或空白) ② 右边界(行尾或空白) ③ name 在 validNames 中。
const SLASH_TOKEN_RE = /\/([a-zA-Z][a-zA-Z0-9_-]*)/g;

function isSpace(ch: string | undefined): boolean {
  if (!ch) return false;
  return /\s/.test(ch);
}

export function findValidSlashRanges(
  text: string,
  validNames: ReadonlySet<string>,
): SlashRange[] {
  if (!text || validNames.size === 0) return [];
  const out: SlashRange[] = [];
  SLASH_TOKEN_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = SLASH_TOKEN_RE.exec(text)) !== null) {
    const start = m.index;
    const end = start + m[0].length;
    const name = m[1];
    if (!validNames.has(name)) continue;
    const leftOk = start === 0 || isSpace(text[start - 1]);
    if (!leftOk) continue;
    const rightOk = end === text.length || isSpace(text[end]);
    if (!rightOk) continue;
    out.push({ from: start, to: end });
  }
  return out;
}

// ── TipTap Extension ─────────────────────────────────────────────────────────

const SLASH_HIGHLIGHT_PLUGIN_KEY = new PluginKey<DecorationSet>(
  "slashHighlight",
);
const REFRESH_META = "slashHighlight:refresh";

// 默认高亮 class —— 用项目主色 + 等宽字体，对齐 slash-popover 中命令文字风格。
const DEFAULT_HIGHLIGHT_CLASS = "text-primary font-mono";

type SlashHighlightOptions = {
  // 闭包返回最新合法命令集；extension 不在内部缓存，避免 chat-input 重建 editor。
  getValidNames: () => ReadonlySet<string>;
  // 允许调用方覆盖样式 class；不传则用 DEFAULT_HIGHLIGHT_CLASS。
  highlightClass?: string;
};

function buildDecorations(
  doc: import("@tiptap/pm/model").Node,
  validNames: ReadonlySet<string>,
  className: string,
): DecorationSet {
  if (validNames.size === 0) return DecorationSet.empty;
  const decos: Decoration[] = [];
  doc.descendants((node, pos) => {
    if (!node.isTextblock) return;
    const text = node.textBetween(0, node.content.size, undefined, leafText);
    if (!text) return;
    const ranges = findValidSlashRanges(text, validNames);
    // textblock 内部 text 的绝对位置 = pos + 1 + offsetInsideTextblock
    // (pos 指向 block node 本身，+1 进入其内容)。
    const base = pos + 1;
    for (const r of ranges) {
      decos.push(
        Decoration.inline(base + r.from, base + r.to, { class: className }),
      );
    }
  });
  return DecorationSet.create(doc, decos);
}

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    slashHighlight: {
      // 触发一次空 transaction 让 plugin 用最新 validNames 重算 decoration。
      // 用于 backendType / validNames 变化时刷新视图。
      setSlashHighlightRefresh: () => ReturnType;
    };
  }
}

export const SlashHighlight = Extension.create<SlashHighlightOptions>({
  name: "slashHighlight",

  addOptions() {
    return {
      getValidNames: () => new Set<string>(),
      highlightClass: DEFAULT_HIGHLIGHT_CLASS,
    };
  },

  addCommands() {
    return {
      setSlashHighlightRefresh:
        () =>
        ({ tr, dispatch }) => {
          if (dispatch) dispatch(tr.setMeta(REFRESH_META, true));
          return true;
        },
    };
  },

  addProseMirrorPlugins() {
    const opts = this.options;
    const className = opts.highlightClass ?? DEFAULT_HIGHLIGHT_CLASS;
    return [
      new Plugin<DecorationSet>({
        key: SLASH_HIGHLIGHT_PLUGIN_KEY,
        state: {
          init: (_config, state) =>
            buildDecorations(state.doc, opts.getValidNames(), className),
          apply: (tr, old) => {
            const refreshed = tr.getMeta(REFRESH_META) === true;
            if (!tr.docChanged && !refreshed) {
              return old.map(tr.mapping, tr.doc);
            }
            return buildDecorations(tr.doc, opts.getValidNames(), className);
          },
        },
        props: {
          decorations(state) {
            return SLASH_HIGHLIGHT_PLUGIN_KEY.getState(state) ?? null;
          },
        },
      }),
    ];
  },
});
