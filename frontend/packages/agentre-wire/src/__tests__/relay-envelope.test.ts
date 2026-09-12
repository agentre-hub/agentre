import { describe, expect, it } from "vitest";

import {
  MAX_CHANNEL_ID_BYTES,
  unwrapEnvelope,
  wrapEnvelope,
} from "../relay-envelope";

const bytes = (...values: number[]) => new Uint8Array(values);

describe("中继通道信封", () => {
  it("套上再拆开，通道 ID 与载荷原样回来", () => {
    const { channelId, frame } = unwrapEnvelope(
      wrapEnvelope("c0ffee", bytes(1, 2, 3)),
    );
    expect(channelId).toBe("c0ffee");
    expect(Array.from(frame)).toEqual([1, 2, 3]);
  });

  // 空载荷是合法的：它是「这条通道关了」的信号。
  it("空载荷活着穿过，它是通道关闭的信号", () => {
    const { channelId, frame } = unwrapEnvelope(
      wrapEnvelope("c0ffee", new Uint8Array(0)),
    );
    expect(channelId).toBe("c0ffee");
    expect(frame.length).toBe(0);
  });

  it.each([
    ["空", ""],
    ["超长", "a".repeat(MAX_CHANNEL_ID_BYTES + 1)],
  ])("拒绝%s的通道 ID", (_name, channelId) => {
    expect(() => wrapEnvelope(channelId, bytes(1))).toThrow();
  });

  it("刚好顶格的通道 ID 收下", () => {
    const channelId = "a".repeat(MAX_CHANNEL_ID_BYTES);
    expect(unwrapEnvelope(wrapEnvelope(channelId, bytes(1))).channelId).toBe(
      channelId,
    );
  });

  // 中继上收到的每一帧都是**别的设备**发来的字节。这一组是 Go 那侧
  // relayenvelope.Unwrap 的同一张表：从前这里只查截断，另外三条一条都不查。
  it.each([
    ["比信封头还短", bytes(0)],
    ["自报长度为 0", bytes(0, 0, 1, 2)],
    ["在通道 ID 中间被截断", bytes(0, 8, 97, 98)],
    [
      "自报长度超上限",
      new Uint8Array([
        0,
        MAX_CHANNEL_ID_BYTES + 1,
        ...new Array(MAX_CHANNEL_ID_BYTES + 1).fill(97),
      ]),
    ],
    ["通道 ID 不是合法 UTF-8", bytes(0, 2, 0xff, 0xfe, 1)],
  ])("拒绝%s的信封", (_name, envelope) => {
    expect(() => unwrapEnvelope(envelope)).toThrow();
  });
});
