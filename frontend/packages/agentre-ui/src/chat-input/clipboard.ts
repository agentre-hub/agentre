import {
  DOMParser as ProseMirrorDOMParser,
  DOMSerializer,
  type ResolvedPos,
  type Slice,
} from "@tiptap/pm/model";
import type { EditorView } from "@tiptap/pm/view";

import { MENTION_DOM_SELECTOR } from "./mentions/mention-node";

/**
 * Parse plain clipboard text without ProseMirror's default collapsing of
 * consecutive line endings. Each source line becomes one paragraph, including
 * empty lines, while the current selection marks are retained.
 */
export function parsePlainTextClipboard(
  text: string,
  $context: ResolvedPos,
  _plain: boolean,
  view: EditorView,
): Slice {
  const { schema } = view.state;
  const serializer = DOMSerializer.fromSchema(schema);
  const marks = $context.marks();
  const container = document.createElement("div");

  for (const line of text.split(/\r\n?|\n/)) {
    const paragraph = container.appendChild(document.createElement("p"));
    if (line) {
      paragraph.appendChild(serializer.serializeNode(schema.text(line, marks)));
    }
  }

  return ProseMirrorDOMParser.fromSchema(schema).parseSlice(container, {
    context: $context,
    preserveWhitespace: true,
  });
}

function carriesMention(html: string): boolean {
  return (
    new DOMParser()
      .parseFromString(html, "text/html")
      .querySelector(MENTION_DOM_SELECTOR) !== null
  );
}

/**
 * 剪贴板同时带 HTML 与纯文本时,选哪一份。
 *
 * 这个编辑器的文档只有「文本 + 换行 + @ 提及」。别处来的 HTML(IDE 的高亮代码、
 * 网页段落、表格)里它留得住的只有文字,而排版一压平,空行和缩进反而对不上原文;
 * 同一次复制里的纯文本那份才是原样。所以默认走纯文本,只在 HTML 带着提及 pill
 * (也就是从本编辑器复制出来的)时才解析 HTML —— 那是纯文本唯一表达不了的东西。
 *
 * 返回 true 表示已处理;false 交还给 ProseMirror 的默认粘贴。
 */
export function pastePlainTextOverForeignHtml(
  view: EditorView,
  event: ClipboardEvent,
): boolean {
  // 组词中的粘贴 ProseMirror 自己也不接,保持一致。
  if (view.composing) return false;
  const data = event.clipboardData;
  if (!data) return false;
  const html = data.getData("text/html");
  const text = data.getData("text/plain");
  if (!html || !text || carriesMention(html)) return false;
  if (!view.pasteText(text, event)) return false;
  event.preventDefault();
  return true;
}
