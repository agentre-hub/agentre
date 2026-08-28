import { describe, expect, it } from "vitest";

import {
  reasonToDisplayStatus,
  reasonToPillText,
  strongestAttentionTone,
} from "../attention-display";
import type { AttentionReason } from "@/stores/attention-store";

describe("reasonToDisplayStatus", () => {
  it("needs_attention / unread → waiting", () => {
    expect(reasonToDisplayStatus("needs_attention", "idle")).toBe("waiting");
    expect(reasonToDisplayStatus("unread", "idle")).toBe("waiting");
  });
  it("running → running", () => {
    expect(reasonToDisplayStatus("running", "idle")).toBe("running");
  });
  it("error → error", () => {
    expect(reasonToDisplayStatus("error", "idle")).toBe("error");
  });
  it("null → fallback", () => {
    expect(reasonToDisplayStatus(null, "running")).toBe("running");
    expect(reasonToDisplayStatus(null, "idle")).toBe("idle");
  });
});

describe("reasonToPillText", () => {
  it.each<[AttentionReason, string | null]>([
    ["needs_attention", "Approval"],
    ["error", "Error"],
    ["unread", "Unread"],
    ["running", null],
  ])("%s → %s", (reason, expected) => {
    expect(reasonToPillText(reason)).toBe(expected);
  });
  it("null → null", () => {
    expect(reasonToPillText(null)).toBeNull();
  });
});

import i18n from "@/i18n";

it("maps bg_running to the running display color", () => {
  expect(reasonToDisplayStatus("bg_running", "idle")).toBe("running");
});
it("pill text for bg_running is the background label", () => {
  expect(reasonToPillText("bg_running")).toBe(i18n.t("attention.background"));
});

/**
 * 组头那一枚记号的档位。
 *
 * 它必须与组里那些行**同源同色**：此前组头写死绿色，可它统计的是全部 attention 条数
 * ——3 条未读的项目显示绿色「3」，而那三行自己画的是琥珀点，组头和它自己的行对不上。
 *
 * 优先级刻意**不**沿用 `computeAttention` 的会话内顺序（needs_attention > running >
 * error > …）：那是单条会话选 reason 的顺序，把 error 排在 running 之后；拿来做组级
 * 取色会让一条出错被三条在跑盖成绿色。这里按「谁更需要你动手」排。
 */
describe("strongestAttentionTone", () => {
  it("空集合没有记号可画", () => {
    expect(strongestAttentionTone([])).toBeNull();
  });

  it("idle 不参与——它按定义就不是需要关注的行", () => {
    expect(strongestAttentionTone(["idle", "idle"])).toBeNull();
  });

  it("单一档位原样返回", () => {
    expect(strongestAttentionTone(["running", "running"])).toBe("running");
    expect(strongestAttentionTone(["waiting"])).toBe("waiting");
    expect(strongestAttentionTone(["error"])).toBe("error");
  });

  it("error > waiting > running：一条出错不会被三条在跑盖住", () => {
    expect(strongestAttentionTone(["running", "running", "error"])).toBe(
      "error",
    );
    expect(strongestAttentionTone(["running", "waiting"])).toBe("waiting");
    expect(strongestAttentionTone(["waiting", "error"])).toBe("error");
  });

  it("顺序不影响结果", () => {
    expect(strongestAttentionTone(["error", "waiting", "running"])).toBe(
      "error",
    );
    expect(strongestAttentionTone(["running", "error", "waiting"])).toBe(
      "error",
    );
  });
});
