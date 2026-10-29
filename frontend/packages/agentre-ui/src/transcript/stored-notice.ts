/**
 * 落库的 `notice` 块（cago `blocks.NoticeBlock`，`{level, text}`）→ 本包的 notice 块。
 *
 * 会话设置切换（换供应商 / 换推理强度 / 换 CLI 模型）、供应商回退、CLI 实际模型偏离
 * 这几种持久 notice 由桌面端 `chat_svc` 落库，结构化负载编码在 `text` 里（Go 侧
 * `view.ProviderNotice`，NoticeBlock 是库类型加不了字段）。桌面端自己的会话由 Go 的
 * `noticeBlockToChatBlock` 解回 DTO 字段；拿帧渲染的面收到的是同一个块的原件
 * （`unrecognized_block` + `blockType:"notice"`），由这里解回**同一份** DTO ——
 * 渲染层只认 `noticeKind` 等字段，负载 JSON 原样漏到正文就是一行乱码。
 *
 * 判据逐条照 Go 的 `view.DecodeProviderNotice`：负载必须是 JSON 对象、已知字段必须
 * 是字符串（Go 的 Unmarshal 在类型不符时整体失败），且 `providerKey` 与 `kind`
 * 至少一个非空 —— 切回「跟随 Agent 绑定」时只剩 kind，所以不能只看 providerKey。
 * 不满足就是旧数据 / 自由文本，`text` 原样渲染。空串字段一律不带（Go 侧是 omitempty）。
 */
import type { TranscriptBlock } from "./dto";

/** 负载 JSON 键 → DTO 字段。两边同名，唯独 `kind` 落到 `noticeKind`。 */
const PAYLOAD_FIELDS = [
  ["providerKey", "providerKey"],
  ["providerName", "providerName"],
  ["modelKey", "modelKey"],
  ["modelName", "modelName"],
  ["kind", "noticeKind"],
  ["reasoningEffort", "reasoningEffort"],
  ["cliModelMode", "cliModelMode"],
  ["cliModel", "cliModel"],
  ["actualModel", "actualModel"],
] as const satisfies readonly (readonly [string, keyof TranscriptBlock])[];

type PayloadField = (typeof PAYLOAD_FIELDS)[number][1];

function decodePayload(
  text: string,
): Partial<Pick<TranscriptBlock, PayloadField>> | undefined {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return undefined;
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return undefined;
  }
  const source = parsed as Record<string, unknown>;
  const out: Partial<Pick<TranscriptBlock, PayloadField>> = {};
  for (const [key, field] of PAYLOAD_FIELDS) {
    const value = source[key];
    if (value === undefined || value === null) continue;
    if (typeof value !== "string") return undefined;
    if (value) out[field] = value;
  }
  return out.providerKey || out.noticeKind ? out : undefined;
}

/**
 * `data` 是落库块的原始 JSON（`{level, text}`）。读不出这一形状时返回 undefined，
 * 调用方照未知块如实呈现原件，而不是猜。
 */
export function storedNoticeBlock(data: unknown): TranscriptBlock | undefined {
  if (typeof data !== "object" || data === null || Array.isArray(data)) {
    return undefined;
  }
  const { level, text } = data as Record<string, unknown>;
  if (
    (level !== undefined && typeof level !== "string") ||
    (text !== undefined && typeof text !== "string")
  ) {
    return undefined;
  }
  const block: TranscriptBlock = { type: "notice" };
  if (level) block.level = level;
  const payload = text ? decodePayload(text) : undefined;
  if (payload) return Object.assign(block, payload);
  if (text) block.text = text;
  return block;
}
