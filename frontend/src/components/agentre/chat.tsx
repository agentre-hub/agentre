// chat 模块的对外出口:转录区、composer、配额表、两张独立卡片。
//
// 实现按职责住在 chat/ 下(transcript / composer / quota / tool-call / approval-gate),
// 这里只做再导出,并且**原样保持既有对外名字** —— chat-panel、peer-panel 与几千行用例
// 都从 "./chat" 取这些名字,拆文件不该让任何调用方跟着改。
//
// 文件曾经是 1326 行:一个 600 行的 ChatTranscript,加上配额表、composer 和两张在那边
// 一处都没被用到的卡片。分成五个文件之后,每一块的读者只需要读那一块。
//
// 顺带也再导出几个共享包 @agentre-hub/agentre-ui 的组件(ChatMessage / CodeBlock /
// ErrorCard / MessageMeta)与 composer 的三个类型:它们本是那个包的东西,从这里转出去
// 只是历史形成的便利出口,别在这里加新的实现。
export { ApprovalGate } from "./chat/approval-gate";
export { ChatComposer } from "./chat/composer";
export { formatResetIn, QuotaMeter } from "./chat/quota";
export { ToolCall } from "./chat/tool-call";
export { ChatTranscript } from "./chat/transcript";
export type {
  ChatTranscriptHandle,
  TranscriptLiveContent,
} from "./chat/transcript";

export {
  ChatMessage,
  CodeBlock,
  ErrorCard,
  MessageMeta,
} from "@agentre-hub/agentre-ui";
export type {
  ChatComposerHandle,
  ChatComposerSubmit,
  ChatImageAttachment,
} from "@agentre-hub/agentre-ui";
