import { describe, expect, it } from "vitest";

import { Capabilities } from "../capability/types";
import { deriveComposerCapabilities } from "./composer-capabilities";

function capsWith(names: string[]) {
  return new Capabilities(new Set(names), {
    allowedModes: [],
    defaultMode: "",
    switchableDuringTurn: false,
    order: [],
  });
}

describe("deriveComposerCapabilities", () => {
  // Hermes Stage 1 只声明了 abort：steer / permission mode / 反向提问 / 图片 /
  // 思考力度 / 压缩都还没实现，UI 必须诚实地一并关掉。
  it("Given the hermes runtime advertises only abort, Then every unfinished control stays off", () => {
    const caps = deriveComposerCapabilities(capsWith(["abort"]));

    expect(caps).toEqual({
      canSteer: false,
      canSetPermissionMode: false,
      canAnswerUserAsk: false,
      supportsReasoningEffort: false,
      supportsImageInput: false,
      canStopBackgroundTask: false,
      supportsCompact: false,
    });
  });

  it("Given a claudecode capability set, Then the controls it implements are on", () => {
    const caps = deriveComposerCapabilities(
      capsWith([
        "abort",
        "steer",
        "set_permission_mode",
        "answer_user_ask",
        "image_input",
        "reasoning_effort",
        "compact",
      ]),
    );

    expect(caps.canSteer).toBe(true);
    expect(caps.canSetPermissionMode).toBe(true);
    expect(caps.canAnswerUserAsk).toBe(true);
    expect(caps.supportsCompact).toBe(true);
  });

  // caps 还没到时不能凭空断言「不支持」：compact 沿用既有回落（codex / piagent
  // 走自家压缩通路），steer 也不拦（确知不支持才拦），其余控件保守关闭。
  it("Given capabilities have not loaded yet, Then compact keeps the legacy codex/piagent fallback", () => {
    expect(deriveComposerCapabilities(null, "codex").supportsCompact).toBe(
      true,
    );
    expect(deriveComposerCapabilities(null, "piagent").supportsCompact).toBe(
      true,
    );
    expect(deriveComposerCapabilities(null, "claudecode").supportsCompact).toBe(
      false,
    );
    expect(deriveComposerCapabilities(null, "claudecode").canSteer).toBe(true);
  });
});
