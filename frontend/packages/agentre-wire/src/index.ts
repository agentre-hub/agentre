/**
 * @agentre-ai/agentre-wire —— agentre ↔ agentred wire 协议的 TypeScript 侧。
 *
 * 三层构成:
 *
 *   - `runtime.ts`   手写的稳定运行时:解码骨架 + 校验助手。
 *   - `envelope.ts`  手写的 JSON-RPC 2.0 信封(Go 真身在 wire 包之外,见该文件头)。
 *   - `*.gen.ts`     由 Go 侧单向生成的帧类型 / 编解码 / 协议常量 /
 *                    事件词表 / 块类型词表 / 视图块类型词表。
 *
 * 注意 `chat-block-types.gen.ts` 是个例外:它**不是 wire 协议的一部分**,而是
 * backend → 前端那一跳的视图 DTO(chat_svc.ChatBlock.type)的词表。它落在本包,
 * 只因为这里是本仓 Go → TS 单向生成唯一的那道缝。它与 `block-types.gen.ts`
 * (blocks.StoredBlock.type)不是同一张表,详见两份产物各自的文件头。
 *
 * 另有 `fixtures/*.json`:同一个 Go 生成器用真实 marshaler 产出的黄金样本,
 * 消费方可以直接 import 来给自己的解码路径做对照。
 */
export * from "./runtime";
export * from "./envelope";
export * from "./constants.gen";
export * from "./codec.gen";
export * from "./event-kinds.gen";
export * from "./block-types.gen";
export * from "./chat-block-types.gen";
