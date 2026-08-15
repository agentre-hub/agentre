// mention 是 inline atom 节点:承载 @ 引用的结构化数据(kind/refId/label/path),
// 以纯 DOM (renderHTML/parseHTML) 渲染成一个 pill,不使用 React node-view ——
// 保持可测 + 与仓库现有「只用 decoration 插件」的编辑器风格一致。
// color 用于显示着色，并随 XML 往返以保持发送前后的 chip 视觉一致。
import { Node, mergeAttributes } from "@tiptap/core";
import type { MentionKind } from "@agentre-ai/agentre-ui";

import { tokenToCssColor } from "../../session-avatar";

export const MENTION_NODE_NAME = "mention";

const MENTION_KINDS: readonly MentionKind[] = ["agent", "project"];

// parseHTML 入口做校验:未知 kind 回落 agent,非数字 refId 回落 0 ——
// 避免脏 HTML(粘贴)静默产出 NaN id / 非法 kind,并顺着序列化流到 XML。
function parseKind(raw: string | null): MentionKind {
  return MENTION_KINDS.includes(raw as MentionKind)
    ? (raw as MentionKind)
    : "agent";
}

function parseRefId(raw: string | null): number {
  const n = Number(raw ?? "0");
  return Number.isFinite(n) ? n : 0;
}

export const Mention = Node.create({
  name: MENTION_NODE_NAME,
  group: "inline",
  inline: true,
  atom: true,
  selectable: true,
  draggable: false,

  addAttributes() {
    return {
      kind: {
        default: "agent" as MentionKind,
        parseHTML: (el) => parseKind(el.getAttribute("data-mention-kind")),
        renderHTML: (attrs) => ({ "data-mention-kind": attrs.kind }),
      },
      refId: {
        default: 0,
        parseHTML: (el) => parseRefId(el.getAttribute("data-ref-id")),
        renderHTML: (attrs) => ({ "data-ref-id": String(attrs.refId) }),
      },
      label: {
        default: "",
        parseHTML: (el) => el.getAttribute("data-label") ?? "",
        renderHTML: (attrs) => ({ "data-label": attrs.label }),
      },
      path: {
        default: "",
        parseHTML: (el) => el.getAttribute("data-path") ?? "",
        renderHTML: (attrs) => ({ "data-path": attrs.path }),
      },
      color: {
        default: "",
        parseHTML: (el) => el.getAttribute("data-color") ?? "",
        renderHTML: (attrs) => ({ "data-color": attrs.color }),
      },
    };
  },

  parseHTML() {
    return [{ tag: "span[data-mention-kind]" }];
  },

  renderHTML({ node, HTMLAttributes }) {
    const css = tokenToCssColor(node.attrs.color as string);
    return [
      "span",
      mergeAttributes(HTMLAttributes, {
        class: "agentre-mention",
        ...(css ? { style: `--mention-color:${css}` } : {}),
      }),
      `@${node.attrs.label as string}`,
    ];
  },
});
