import { describe, expect, it } from "vitest";

import {
  EventImage,
  EventUnrecognizedBlock,
  EventUserMessage,
} from "../event-kinds.gen";
import { reduceFrames, type TranscriptFrame } from "./frames";

/**
 * 一张图落到哪 —— 归宿是**用户消息上的图块**,不是助手名下的一段 base64。
 *
 * 此前没有 image 这个 kind:宿主投影一条 role=user 的 image 块时认不出来,走 R8
 * 兜底发 `unrecognized_block`,而那一支的落点是 `openAssistant(...)` 里的一条
 * `notice`,正文是 `JSON.stringify(载荷, null, 2)`。于是一张 3MB 的图在转录里铺开
 * 成四百万字符,还署名成助手说的。
 *
 * 这份用例钉住三件事,它们各自都能单独坏掉:
 *
 *  1. **并进当前那条用户消息。** 帧是块级的(一块一帧),而两个宿主的块序都是
 *     [文本, 附件...] —— 所以 `[user_message, image]` 是**一条**消息的两帧。
 *     `pushUserMessage` 每次都新建一条并把 `st.open` 置空,不合并的话同一句话会
 *     画成两个气泡。
 *  2. **只贴图那一档要起一条新的。** 没有文本就没有 `user_message` 帧,image 帧
 *     是这条消息的首帧;沿用「并进当前那条」会把它挂到上一条提问上。
 *  3. **老形态与新形态同落点。** `session.pull` 原样重放 agentred 日志里**当时**
 *     那一份,而那份日志永久保存 —— 升级前发的图永远是 `unrecognized_block`
 *     形态。不认它,那些图就永远停在 notice,不是「等大家升级就好了」。
 */

const SID = 7;

/** 一张 1×1 png 的字节,base64 之后就是宿主放进 `inline` 那一格的东西。 */
const PNG_B64 = "iVBORw0KGgoAAAANSUhEUg";

function frame(event: Record<string, unknown>): TranscriptFrame {
  return { sessionId: SID, createtime: 0, event } as TranscriptFrame;
}

/** 新形态:宿主投影 image 块发出来的那一帧。 */
function imageFrame(inline = PNG_B64, mediaType = "image/png") {
  return frame({ kind: EventImage, mediaType, source: { inline } });
}

/** 老形态:认不出 image 块时 R8 兜底发出来的那一帧(载荷是块的原始 JSON)。 */
function legacyImageFrame(inline = PNG_B64, mediaType = "image/png") {
  return frame({
    kind: EventUnrecognizedBlock,
    blockType: "image",
    data: { media_type: mediaType, source: { inline } },
  });
}

describe("一张图归约到哪", () => {
  it("[user_message, image] 并成一条用户消息:一句话加一张图,图在文本之后", () => {
    const messages = reduceFrames(
      [frame({ kind: EventUserMessage, text: "这张图哪里不对" }), imageFrame()],
      SID,
    );

    expect(messages).toHaveLength(1);
    expect(messages[0].role).toBe("user");
    expect(messages[0].blocks).toEqual([
      { type: "text", text: "这张图哪里不对" },
      {
        type: "image",
        image: {
          dataUrl: `data:image/png;base64,${PNG_B64}`,
          mediaType: "image/png",
        },
      },
    ]);
  });

  it("只有 image 帧时起一条新的用户消息,图是它的第一块", () => {
    const messages = reduceFrames([imageFrame()], SID);

    expect(messages).toHaveLength(1);
    expect(messages[0].role).toBe("user");
    expect(messages[0].blocks).toEqual([
      {
        type: "image",
        image: {
          dataUrl: `data:image/png;base64,${PNG_B64}`,
          mediaType: "image/png",
        },
      },
    ]);
  });

  it("老形态的 unrecognized_block 与新事件落同一处,画出同一张图", () => {
    const fresh = reduceFrames([imageFrame()], SID);
    const legacy = reduceFrames([legacyImageFrame()], SID);

    expect(legacy[0].role).toBe("user");
    expect(legacy[0].blocks).toEqual(fresh[0].blocks);
  });

  it("blockType 不是 image 的 unrecognized_block 一字不改,仍落助手名下的 notice", () => {
    const messages = reduceFrames(
      [
        frame({
          kind: EventUnrecognizedBlock,
          blockType: "future_block",
          data: { keep: true },
        }),
      ],
      SID,
    );

    expect(messages[0].role).toBe("assistant");
    expect(messages[0].blocks[0].type).toBe("notice");
  });

  it("助手帧到达即关闭当前那条用户消息:此后的图不再并进上一条提问", () => {
    const messages = reduceFrames(
      [
        frame({ kind: EventUserMessage, text: "先问一句" }),
        frame({ kind: "text_delta", text: "在答了" }),
        imageFrame(),
      ],
      SID,
    );

    // 用户一条、助手一条、图另起一条 —— 图绝不该追加到那条已经被答过的提问上。
    expect(messages.map((m) => m.role)).toEqual(["user", "assistant", "user"]);
    expect(messages[0].blocks).toEqual([{ type: "text", text: "先问一句" }]);
    expect(messages[2].blocks[0].type).toBe("image");
  });

  it("两格来源都空时不静默跳过:落成 R8 那样的 notice,如实说这是一张取不到的图", () => {
    const messages = reduceFrames(
      [frame({ kind: EventImage, mediaType: "image/png", source: {} })],
      SID,
    );

    expect(messages).toHaveLength(1);
    expect(messages[0].role).toBe("user");
    expect(messages[0].blocks).toHaveLength(1);
    // 不留一个没有 dataUrl 的 image 块:ImageBlockView 读不到 dataUrl 就 `return
    // null`,那样的块在屏幕上什么都不是 —— 转录里照样凭空少一块,只是少在渲染这一
    // 步而不是归约这一步。所以按 R8 的同一条纪律落成 notice,读者看得见这里本来
    // 有一张图。归在**用户**名下:取不到的那张仍是用户贴的。
    expect(messages[0].blocks[0].type).toBe("notice");
    expect(messages[0].blocks[0].text).toContain("image");
    expect(messages[0].blocks[0].text).toContain("image/png");
  });

  it("老形态两格都空时同样落 notice,与新事件同一处", () => {
    const messages = reduceFrames(
      [
        frame({
          kind: EventUnrecognizedBlock,
          blockType: "image",
          data: { media_type: "image/jpeg", source: {} },
        }),
      ],
      SID,
    );

    expect(messages[0].role).toBe("user");
    expect(messages[0].blocks[0].type).toBe("notice");
    expect(messages[0].blocks[0].text).toContain("image/jpeg");
  });
});
