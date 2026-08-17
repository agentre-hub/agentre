/**
 * @agentre-ai/agentre-wire —— agentre ↔ agentred wire 协议的 TypeScript 侧。
 *
 * 三层构成:
 *
 *   - `runtime.ts`   手写的稳定运行时:解码骨架 + 校验助手。
 *   - `envelope.ts`  手写的 JSON-RPC 2.0 信封(Go 真身在 wire 包之外,见该文件头)。
 *   - `*.gen.ts`     由 Go 侧单向生成的帧类型 / 编解码 / 协议常量 / 事件词表。
 *
 * 另有 `fixtures/*.json`:同一个 Go 生成器用真实 marshaler 产出的黄金样本,
 * 消费方可以直接 import 来给自己的解码路径做对照。
 */
export * from "./runtime";
export * from "./envelope";
export * from "./constants.gen";
export * from "./codec.gen";
export * from "./event-kinds.gen";
