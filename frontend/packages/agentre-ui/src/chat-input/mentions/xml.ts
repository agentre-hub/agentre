// Mention 的 XML 序列化/解析核心 —— 纯函数,单测覆盖。
// 序列化产出进入 SendRequest.text;解析用于草稿回填与 transcript chip 渲染。

export type MentionKind = "agent" | "project" | "device";

export type MentionRef = {
  kind: MentionKind;
  // agent / project 的自增主键。device 恒为 0 —— 一台机器没有全局自增 id 可用:
  // LAN 配对行与账号设备行是两个各自自增的序列(会撞号),两个宿主的 id 含义也不同。
  // 设备的身份是 fp,见下。
  refId: number;
  label: string;
  color?: string;
  // 仅 project 有;agent / device 省略。
  path?: string;
  // 仅 device 有:设备指纹(agentred 的 daemon_fingerprint / 账号设备的 fingerprint,
  // 也就是 backend 的 device_fingerprint 词汇)。跨来源、跨宿主唯一,所以正文里存的是它 ——
  // 消息会同步到别的机器上重放,那时候只有指纹还指得回同一台机器。
  fp?: string;
};

export type MentionSegment =
  | { type: "text"; value: string }
  | { type: "mention"; ref: MentionRef };

function xmlEscape(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function xmlUnescape(s: string): string {
  return s
    .replace(/&quot;/g, '"')
    .replace(/&gt;/g, ">")
    .replace(/&lt;/g, "<")
    .replace(/&amp;/g, "&");
}

export function serializeMentionXml(ref: MentionRef): string {
  const label = xmlEscape(ref.label);
  const color = ref.color ? ` color="${xmlEscape(ref.color)}"` : "";
  if (ref.kind === "project") {
    const path = xmlEscape(ref.path ?? "");
    return `<project id="${ref.refId}" path="${path}"${color}>${label}</project>`;
  }
  if (ref.kind === "device") {
    return `<device fp="${xmlEscape(ref.fp ?? "")}"${color}>${label}</device>`;
  }
  return `<agent id="${ref.refId}"${color}>${label}</agent>`;
}

// 同时匹配 <agent …>…</agent>、<project …>…</project> 与 <device …>…</device>。
// 属性顺序固定(agent/project 先 id,project 再 path;device 只有 fp),但解析用独立
// 属性正则,不依赖顺序。
const TAG_RE = /<(agent|project|device)\b([^>]*)>([\s\S]*?)<\/\1>/g;
const ID_RE = /\bid="(\d+)"/;
const PATH_RE = /\bpath="([^"]*)"/;
const COLOR_RE = /\bcolor="([^"]*)"/;
const FP_RE = /\bfp="([^"]*)"/;

export function parseMentionXml(text: string): MentionSegment[] {
  const out: MentionSegment[] = [];
  let last = 0;
  TAG_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = TAG_RE.exec(text)) !== null) {
    if (m.index > last) {
      out.push({ type: "text", value: text.slice(last, m.index) });
    }
    const kind = m[1] as MentionKind;
    const attrs = m[2];
    const id = Number(ID_RE.exec(attrs)?.[1] ?? "0");
    const label = xmlUnescape(m[3]);
    const ref: MentionRef = { kind, refId: id, label };
    const color = COLOR_RE.exec(attrs)?.[1];
    if (color) ref.color = xmlUnescape(color);
    if (kind === "project") {
      ref.path = xmlUnescape(PATH_RE.exec(attrs)?.[1] ?? "");
    }
    if (kind === "device") {
      ref.fp = xmlUnescape(FP_RE.exec(attrs)?.[1] ?? "");
    }
    out.push({ type: "mention", ref });
    last = m.index + m[0].length;
  }
  if (last < text.length) {
    out.push({ type: "text", value: text.slice(last) });
  }
  if (out.length === 0) out.push({ type: "text", value: text });
  return out;
}

// mentionsToDisplayText 把消息正文里的 @ 提及 XML 还原成可读文本(`@label`),
// 供只能显示纯文本的地方用(如右侧 outline 列表)。转录区不要用它 —— 那里渲染真 chip。
export function mentionsToDisplayText(raw: string): string {
  return parseMentionXml(raw)
    .map((s) => (s.type === "text" ? s.value : `@${s.ref.label}`))
    .join("");
}
