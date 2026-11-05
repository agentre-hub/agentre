// chat 模块的对外出口:转录区、composer、配额表、审批卡片。
//
// 实现按职责住在 chat/ 下(transcript / composer / quota / approval-gate),
// 这里只做再导出,并且**原样保持既有对外名字** —— chat-panel、peer-panel 与几千行用例
// 都从 "./chat" 取这些名字,拆文件不该让任何调用方跟着改。
//
// 顺带也再导出共享包 @agentre-hub/agentre-ui 里 composer 的三个类型:它们本是那个包的
// 东西,从这里转出去只是历史形成的便利出口,别在这里加新的实现。
export { ApprovalGate } from "./chat/approval-gate";
export { ChatComposer } from "./chat/composer";
export { formatResetIn, QuotaMeter } from "./chat/quota";
export { ChatTranscript } from "./chat/transcript";
export type {
  ChatTranscriptHandle,
  TranscriptLiveContent,
} from "./chat/transcript";

export type {
  ChatComposerHandle,
  ChatComposerSubmit,
  ChatImageAttachment,
} from "@agentre-hub/agentre-ui";
