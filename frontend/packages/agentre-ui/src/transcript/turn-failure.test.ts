import i18next from "i18next";
import { describe, expect, it } from "vitest";

import { AGENTRE_UI_NAMESPACE } from "../i18n";
import {
  classifyTurnFailure,
  turnFailureFact,
  type TurnFailureKind,
} from "./turn-failure";

// 样本取自 errorText 的真实来源：chat_svc 把 stopErr.Error() 原样落库
// （internal/service/chat_svc/turn_run.go），远端中断/断连换成 code 包里的中英
// 文案（internal/pkg/code/{zh_cn,en}.go），其余是各 CLI 的输出串。
const SAMPLES: Array<[TurnFailureKind, string]> = [
  // Anthropic 的 529 错误体；CLI 退出时它会被包在进程退出前缀里。
  [
    "overloaded",
    'API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}',
  ],
  [
    "overloaded",
    'claude: process exited with status 1: API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}',
  ],
  [
    "rateLimited",
    'API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed the rate limit for your organization of 50,000 input tokens per minute."}}',
  ],
  ["rateLimited", "Claude AI usage limit reached|1755680401"],
  [
    "rateLimited",
    "You've hit your usage limit. Upgrade to Pro (https://openai.com/chatgpt/pricing) or try again in 2 days 3 hours.",
  ],
  ["rateLimited", "exceeded retry limit, last status: 429 Too Many Requests"],
  // pkg/claudecode/session_test.go 的 403 合成帧。
  ["authExpired", "Failed to authenticate. API Error: 403"],
  [
    "authExpired",
    'API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired. Please obtain a new token or refresh your existing token."}}',
  ],
  ["authExpired", "Invalid API key · Please run /login"],
  [
    "authExpired",
    "codex: process exited: exit status 1: Your access token could not be refreshed because your refresh token has expired. Please log out and sign in again.",
  ],
  ["processExited", "piagent: process exited: process failure"],
  ["processExited", "codex: process exited unexpectedly"],
  ["processExited", "claudecode: exit 1: panic: runtime error"],
  [
    "processExited",
    "agentruntime/runtimes/claudecode: subprocess produced no events (likely exited on startup)",
  ],
  ["processExited", "acp agent process exited: exit status 1"],
  [
    "processExited",
    "本轮在这里被打断了：远端 agentred 重启，这条会话已中断，不是运行失败。以上内容已保留，直接发消息即可开始新的一轮",
  ],
  [
    "processExited",
    "This turn was interrupted here: the remote agentred restarted and the session was cut short — it did not fail. Everything above is kept; send a message to start a new turn.",
  ],
  [
    "network",
    "API Error: Connection closed mid-response. The response above may be incomplete.",
  ],
  ["network", "dial tcp 127.0.0.1:52401: connect: connection refused"],
  [
    "network",
    "stream error: stream disconnected before completion: error sending request for url (https://api.openai.com/v1/responses)",
  ],
  [
    "network",
    "本轮到此为止：连不上远端 agentred 了，多次重连都没成功，不是运行失败。以上内容已保留，等它恢复后可以继续",
  ],
  [
    "network",
    "This turn stops here: the remote agentred is unreachable and every reconnect attempt failed — it did not fail. Everything above is kept; you can continue once it is back.",
  ],
  // chat_test.go 的真实运行错误：原样透出，分不出类。
  ["unknown", "API error 500: upstream exploded"],
  ["unknown", "upstream failed"],
  // 「已过期」说的是 CLI 会话，不是登录 —— 不能被认成 authExpired。
  ["unknown", "CLI 会话已过期或已被清理，本对话已重置，请重新发送消息"],
  [
    "unknown",
    "claudecode: stream-json schema unrecognized; please update agentre",
  ],
  ["unknown", ""],
  ["unknown", "   "],
];

describe("classifyTurnFailure", () => {
  it.each(SAMPLES)(
    "Given a real %s errorText, When classified, Then it lands in that kind",
    (kind, errorText) => {
      expect(classifyTurnFailure(errorText)).toBe(kind);
    },
  );
});

describe("turnFailureFact", () => {
  const kinds: TurnFailureKind[] = [
    "overloaded",
    "rateLimited",
    "authExpired",
    "processExited",
    "network",
    "unknown",
  ];

  it.each(["zh-CN", "en"])(
    "Given the %s bundle, When each kind is described, Then every kind gets its own resolved sentence",
    (language) => {
      const t = i18next.getFixedT(language, AGENTRE_UI_NAMESPACE);
      const facts = kinds.map((kind) => turnFailureFact(kind, t));

      expect(new Set(facts).size).toBe(kinds.length);
      for (const fact of facts) {
        expect(fact.trim()).not.toBe("");
        expect(fact).not.toMatch(/^chat\./);
      }
    },
  );
});
