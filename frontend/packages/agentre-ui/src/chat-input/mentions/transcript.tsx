import * as React from "react";

import { useUiTranslation } from "../../i18n";
import { tokenToCssColor } from "../../lib/agent-color";
import type { MarkdownInlineDecorator } from "../../transcript/markdown-text";
import { useOptionalPort } from "../../transcript/ports-context";
import { parseMentionXml, type MentionRef } from "./xml";

// 私有区哨兵:markdown 无 rehype-raw 会吃掉 <agent>/<project> 原始标签,所以
// 先把标签替换成 {idx} 这种「对 markdown 无意义」的纯文本哨兵,
// 让它安全穿过 markdown 解析,再由 decorator 在 hast 文本节点层面还原成 chip。
const S = "";
const E = "";

export function prepareMentionText(raw: string): {
  text: string;
  refs: MentionRef[];
} {
  const refs: MentionRef[] = [];
  let text = "";
  for (const seg of parseMentionXml(raw)) {
    if (seg.type === "text") {
      text += seg.value;
    } else {
      text += `${S}${refs.length}${E}`;
      refs.push(seg.ref);
    }
  }
  return { text, refs };
}

export function MentionChip({
  refData,
}: {
  refData: MentionRef;
}): React.ReactElement {
  const { t } = useUiTranslation();
  // 能力探测:宿主没接 openMention 就没有「去处」,那就别渲染一个点了没反应的按钮。
  // @label 与颜色本身是有信息的,退化成纯文本时它们要留着。
  const openMention = useOptionalPort("openMention");
  const title =
    refData.kind === "agent"
      ? t("mentions.chip.agentTitle", { name: refData.label })
      : t("mentions.chip.projectTitle", { name: refData.label });
  const color = tokenToCssColor(refData.color ?? "");
  const style = color
    ? ({ "--mention-color": color } as React.CSSProperties)
    : undefined;

  if (!openMention) {
    return (
      <span title={title} className="agentre-mention" style={style}>
        @{refData.label}
      </span>
    );
  }

  return (
    <button
      type="button"
      title={title}
      onClick={() => openMention(refData)}
      className="agentre-mention cursor-pointer"
      style={style}
    >
      @{refData.label}
    </button>
  );
}

const SENTINEL_RE = /(\d+)/g;

export function makeMentionDecorator(
  refs: MentionRef[],
): MarkdownInlineDecorator<MentionRef> {
  return {
    tokenize(text) {
      const out: Array<
        { type: "text"; value: string } | { type: "token"; data: MentionRef }
      > = [];
      let last = 0;
      SENTINEL_RE.lastIndex = 0;
      let m: RegExpExecArray | null;
      while ((m = SENTINEL_RE.exec(text)) !== null) {
        if (m.index > last) {
          out.push({ type: "text", value: text.slice(last, m.index) });
        }
        const ref = refs[Number(m[1])];
        if (ref) out.push({ type: "token", data: ref });
        last = m.index + m[0].length;
      }
      if (last < text.length)
        out.push({ type: "text", value: text.slice(last) });
      return out;
    },
    render(data) {
      return <MentionChip refData={data} />;
    },
  };
}
