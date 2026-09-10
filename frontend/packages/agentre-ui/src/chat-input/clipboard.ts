import {
  DOMParser as ProseMirrorDOMParser,
  DOMSerializer,
  type ResolvedPos,
  type Slice,
} from "@tiptap/pm/model";
import type { EditorView } from "@tiptap/pm/view";

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
