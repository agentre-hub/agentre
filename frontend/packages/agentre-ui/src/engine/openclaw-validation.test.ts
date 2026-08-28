import { describe, expect, it } from "vitest";

import { agentreUiResources } from "../i18n";
import {
  OPENCLAW_ERROR_KEY_BY_CODE,
  openClawDraftIssue,
  openClawGatewayURLIssue,
} from "./openclaw-validation";

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

// Every error code must have copy in both locales, otherwise the UI falls
// back to showing the raw protocol string (or backend Chinese) — exactly the
// failure this table exists to prevent.
//
// This cannot be delegated to `src/i18n/i18n.test.tsx`: that guard walks the
// TS AST collecting literal `t("…")` call sites, while the keys here are
// resolved at RUNTIME through `OPENCLAW_ERROR_KEY_BY_CODE` — they never
// appear as a string-literal argument to `t(...)` anywhere in the AST. The
// bundle asserted against here is the package's own `agentreUiResources`
// (the `agentreUi` namespace), matching what `useUiTranslation` — used by
// `openclaw-backend-fields.tsx` — actually resolves against, not the host's
// `common` namespace.
describe("error code → locale coverage", () => {
  const locales: Array<[string, Record<string, unknown>]> = [
    ["en", agentreUiResources.en as unknown as Record<string, unknown>],
    [
      "zh-CN",
      agentreUiResources["zh-CN"] as unknown as Record<string, unknown>,
    ],
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
