import { describe, expect, it } from "vitest";

import en from "@/i18n/locales/en";
import zh from "@/i18n/locales/zh-CN";
import {
  OPENCLAW_ERROR_KEY_BY_CODE,
  openClawDraftIssue,
  openClawGatewayURLIssue,
} from "../openclaw-validation";

describe("openClawGatewayURLIssue", () => {
  it("accepts loopback ws and any wss", () => {
    expect(openClawGatewayURLIssue("ws://127.0.0.1:18789")).toBeNull();
    expect(openClawGatewayURLIssue("ws://localhost:18789")).toBeNull();
    expect(openClawGatewayURLIssue("wss://gateway.example.com")).toBeNull();
  });

  it.each([
    ["", "OPENCLAW_URL_REQUIRED"],
    ["http://127.0.0.1:18789", "OPENCLAW_URL_SCHEME"],
    ["ws://user:secret@127.0.0.1:18789", "OPENCLAW_URL_CREDENTIALS"],
    ["ws://127.0.0.1:18789/?token=x", "OPENCLAW_URL_CREDENTIALS"],
    ["ws://127.0.0.1:18789/#frag", "OPENCLAW_URL_CREDENTIALS"],
    ["ws://example.com:18789", "OPENCLAW_URL_PLAINTEXT_REMOTE"],
    ["not a url", "OPENCLAW_URL_INVALID"],
  ])("rejects %s with %s", (input, expected) => {
    expect(openClawGatewayURLIssue(input)).toBe(expected);
  });
});

describe("openClawDraftIssue", () => {
  const valid = {
    name: "OpenClaw",
    gatewayURL: "ws://127.0.0.1:18789",
    sessionMode: "per-agentre-session",
  };

  it("passes a valid draft", () => {
    expect(openClawDraftIssue(valid)).toBeNull();
  });

  it("requires a name before anything else", () => {
    expect(openClawDraftIssue({ ...valid, name: "  " })).toBe(
      "OPENCLAW_NAME_REQUIRED",
    );
  });

  it("rejects an unsupported session mode", () => {
    expect(openClawDraftIssue({ ...valid, sessionMode: "per-agent" })).toBe(
      "OPENCLAW_SESSION_MODE_INVALID",
    );
  });
});

// 每个错误码都必须在两个 locale 里有对应文案,否则 UI 会退回显示原始英文协议串
// (或后端中文),这正是这次修复要根治的问题。
describe("error code → locale coverage", () => {
  const locales: Array<[string, Record<string, unknown>]> = [
    ["en", en as Record<string, unknown>],
    ["zh-CN", zh as Record<string, unknown>],
  ];

  it.each(locales)(
    "%s has copy for every mapped OpenClaw error code",
    (_name, locale) => {
      const errors = (
        (locale.agentBackends as Record<string, unknown>).openclaw as Record<
          string,
          unknown
        >
      ).errors as Record<string, string>;
      for (const key of Object.values(OPENCLAW_ERROR_KEY_BY_CODE)) {
        expect(errors[key], `missing errors.${key}`).toBeTruthy();
      }
    },
  );
});
