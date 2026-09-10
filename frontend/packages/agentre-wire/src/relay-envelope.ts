/**
 * 中继通道信封：2 字节大端通道 ID 长度 + 通道 ID（UTF-8）+ 载荷。
 *
 * 中继本身只路由不透明字节，信封是它认得的**唯一**结构——它据此知道一条物理连接
 * 上的这一帧属于哪条虚拟通道。
 *
 * Go 那一侧的对应物是 `pkg/wire/relayenvelope`，同一格式、同一套校验。从前三个宿主
 * 各写一份解析，三套校验互不相同：本侧这份只查截断，长度 0 照收（通道 ID 成空串、
 * 整段载荷当帧交出去），非法 UTF-8 被 TextDecoder 静默换成 U+FFFD，长度上限还比
 * daemon 宽 512 倍。中继上的每一帧都是**别的设备**发来的字节，而三份解析里最松的
 * 那一份决定了实际的下限。
 *
 * 空载荷是合法的：它是「这条通道关了」的信号。
 */

/** 长度前缀占的字节数。 */
export const ENVELOPE_HEADER_BYTES = 2;

/**
 * 通道 ID 的字节上限。
 *
 * 远宽于实际：服务端发的是 16 字节的 base64url（22 字符），daemon 发的是 16 字节的
 * hex（32 字符），浏览器一个都不自己造——它只回送服务端发来的那个。
 */
export const MAX_CHANNEL_ID_BYTES = 128;

/** 信封头最多占多少，链路读上限里给它留的就是这一份余量。 */
export const MAX_ENVELOPE_BYTES = ENVELOPE_HEADER_BYTES + MAX_CHANNEL_ID_BYTES;

const encoder = new TextEncoder();
// fatal：非法字节序列要抛，不要静默换成 U+FFFD——那会把一个坏掉的通道 ID 当成一个
// 合法但对不上号的通道 ID，帧被静静丢掉而没人知道为什么。
const decoder = new TextDecoder("utf-8", { fatal: true });

export function wrapEnvelope(channelId: string, frame: Uint8Array): Uint8Array {
  const id = encoder.encode(channelId);
  if (id.length === 0) throw new Error("relay: 通道 ID 不能为空");
  if (id.length > MAX_CHANNEL_ID_BYTES) throw new Error("relay: 通道 ID 过长");
  const envelope = new Uint8Array(
    ENVELOPE_HEADER_BYTES + id.length + frame.length,
  );
  envelope[0] = (id.length >> 8) & 0xff;
  envelope[1] = id.length & 0xff;
  envelope.set(id, ENVELOPE_HEADER_BYTES);
  envelope.set(frame, ENVELOPE_HEADER_BYTES + id.length);
  return envelope;
}

export function unwrapEnvelope(envelope: Uint8Array): {
  channelId: string;
  frame: Uint8Array;
} {
  if (envelope.length < ENVELOPE_HEADER_BYTES) {
    throw new Error("relay: 信封比通道 ID 长度还短");
  }
  const length = (envelope[0] << 8) | envelope[1];
  if (length === 0 || length > MAX_CHANNEL_ID_BYTES) {
    throw new Error("relay: 信封的通道 ID 长度不合法");
  }
  const start = ENVELOPE_HEADER_BYTES + length;
  if (envelope.length < start) throw new Error("relay: 信封被截断");
  let channelId: string;
  try {
    channelId = decoder.decode(envelope.subarray(ENVELOPE_HEADER_BYTES, start));
  } catch {
    throw new Error("relay: 信封的通道 ID 不是合法 UTF-8");
  }
  return { channelId, frame: envelope.subarray(start) };
}
