import {
  parseMentionXml,
  serializeMentionXml,
  type MentionKind,
} from "./mentions/xml";
import type {
  AIChatInputDraft,
  ProseMirrorLikeNode,
  TipTapDocNode,
  TipTapMentionNode,
  TipTapParagraphNode,
  TipTapTextNode,
} from "./types";

// 提取纯文本：text 原样保留；hardBreak / 段落边界统一用 \n。
// （TipTap doc 末尾必带空段落，最后再去掉行尾 \n。）
export function extractPlainText(doc: ProseMirrorLikeNode): string {
  let out = "";
  doc.descendants((node) => {
    if (node.type.name === "text") {
      out += node.text ?? "";
    } else if (node.type.name === "hardBreak") {
      out += "\n";
    } else if (node.type.name === "mention") {
      out += serializeMentionXml({
        kind: (node.attrs.kind as MentionKind) ?? "agent",
        refId: Number(node.attrs.refId ?? 0),
        label: String(node.attrs.label ?? ""),
        path: node.attrs.path ? String(node.attrs.path) : undefined,
        color: node.attrs.color ? String(node.attrs.color) : undefined,
      });
    } else if (node.type.name === "paragraph" && out.length > 0) {
      out += "\n";
    }
    return true;
  });
  return out.replace(/\n+$/g, "");
}

function normalizeDraftMessage(
  draft: string | AIChatInputDraft,
): AIChatInputDraft {
  if (typeof draft === "string") {
    return { content: draft };
  }
  return { content: draft.content ?? "" };
}

export function buildEditorDocFromMessage(
  message: string | AIChatInputDraft,
): TipTapDocNode {
  const { content } = normalizeDraftMessage(message);
  const paragraphs: TipTapParagraphNode[] = [];
  const segments = content.split("\n");
  for (const seg of segments) {
    const nodes: (TipTapTextNode | TipTapMentionNode)[] = [];
    for (const part of parseMentionXml(seg)) {
      if (part.type === "text") {
        if (part.value.length > 0)
          nodes.push({ type: "text", text: part.value });
      } else {
        nodes.push({
          type: "mention",
          attrs: {
            kind: part.ref.kind,
            refId: part.ref.refId,
            label: part.ref.label,
            path: part.ref.path ?? "",
            ...(part.ref.color ? { color: part.ref.color } : {}),
          },
        });
      }
    }
    paragraphs.push(
      nodes.length > 0
        ? { type: "paragraph", content: nodes }
        : { type: "paragraph" },
    );
  }
  if (paragraphs.length === 0) {
    paragraphs.push({ type: "paragraph" });
  }
  return { type: "doc", content: paragraphs };
}
