import { describe, expect, it } from "vitest";

import { makeStreamDecoder } from "../local-command/decode";

describe("makeStreamDecoder", () => {
  it("decodes PTY byte chunks", () => {
    const dec = makeStreamDecoder();
    expect(dec(new Uint8Array([0x68, 0x69]))).toBe("hi");
  });

  it("carries a multibyte sequence split across chunks", () => {
    // '─' = E2 94 80;PTY 会在任意字节位置切块,解码器必须跨块重组而不是吐 U+FFFD。
    const dec = makeStreamDecoder();
    expect(dec(new Uint8Array([0xe2, 0x94]))).toBe("");
    expect(dec(new Uint8Array([0x80]))).toBe("─");
  });
});
