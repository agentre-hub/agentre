/**
 * 块类型词表:blocks.StoredBlock 的 type 判别值的全部取值。
 *
 * 本文件由 Go 生成器产出,**不要手改** —— 手改会被下一次重新生成覆盖,
 * 而且 TestGeneratedTSFresh 会立刻变红。
 *
 * 真理源:  github.com/cago-frame/agents/agent/blocks 的块注册表
 *          (运行时枚举 + 本仓注册点的 AST 扫描,两者取并集)
 * 生成器:  internal/pkg/agentruntime/runtimes/remote/wire/tsgen_test.go
 * 重新生成:
 *
 *   WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec
 *
 * 边界例外:codec / constants 两份产物守的规矩是「wire 包之外的类型一律
 * unknown」,这一份与 event-kinds.gen.ts 是仅有的两处刻意例外
 * (chat-block-types.gen.ts 不在此列 —— 它讲的根本不是 wire)。理由:
 *
 * StoredBlock 在 Go 侧是 {type, data},data 是 json.RawMessage —— 载荷对
 * wire 完全不透明,生成器对它只能给出 unknown。HistoryMessageWire.blocks
 * 与 RunParams.userBlocks 这两条链路上唯一有类型意义的东西就是 type 判别值,
 * 而块注册表包正是 wire.go 的直接依赖(那两个字段的元素类型就是它的
 * StoredBlock),追它不打穿分层。
 *
 * 与 event-kinds.gen.ts 的一处不同:那张表是编译期常量,AST 扫源码就能穷举;
 * 这一份是运行时注册表,判别值散在各类型的 Type() 方法里、由各包的 init()
 * 填进注册表。完整性守卫因此形态不同,见生成器里的 TestTSGenCoversBlockTypes。
 *
 * 格式:生成器直接输出 Prettier(printWidth 80,本仓默认配置)的形态,
 * 与手写代码同一套 ESLint 规则,没有整文件豁免。格式化是产物的一部分 ——
 * 若放到生成之后当外部工序,「重新生成 → 逐字节比对」的守卫会永久误报。
 */

export const BlockTypeCompactBoundary = "compact_boundary";

export const BlockTypeDisplayText = "display_text";

export const BlockTypeExecApproval = "exec_approval";

export const BlockTypeImage = "image";

export const BlockTypeNestedToolResult = "nested_tool_result";

export const BlockTypeNestedToolUse = "nested_tool_use";

export const BlockTypeNotice = "notice";

export const BlockTypePermissionModeChange = "permission_mode_change";

export const BlockTypePlan = "plan";

export const BlockTypeRef = "ref";

export const BlockTypeSubagentState = "subagent_state";

export const BlockTypeSummary = "summary";

export const BlockTypeText = "text";

export const BlockTypeThinking = "thinking";

export const BlockTypeToolApproval = "tool_approval";

export const BlockTypeToolPermission = "tool_permission";

export const BlockTypeToolResult = "tool_result";

export const BlockTypeToolUse = "tool_use";

export const BlockTypeUserAsk = "user_ask";

/**
 * 全部块类型判别值的联合类型(= Go 的 blocks.StoredBlock.type 取值域)。
 *
 * 消费方把手上的 type 收窄成这个类型之后,在 switch 的 default 分支写一句
 * const _: never = type,「上游新增了一个块类型」就成了消费方的编译期错误。块的
 * data 是 json.RawMessage、无从校验,type 是这条链路上唯一能被类型系统接住的东西。
 */
export type BlockType =
  | typeof BlockTypeCompactBoundary
  | typeof BlockTypeDisplayText
  | typeof BlockTypeExecApproval
  | typeof BlockTypeImage
  | typeof BlockTypeNestedToolResult
  | typeof BlockTypeNestedToolUse
  | typeof BlockTypeNotice
  | typeof BlockTypePermissionModeChange
  | typeof BlockTypePlan
  | typeof BlockTypeRef
  | typeof BlockTypeSubagentState
  | typeof BlockTypeSummary
  | typeof BlockTypeText
  | typeof BlockTypeThinking
  | typeof BlockTypeToolApproval
  | typeof BlockTypeToolPermission
  | typeof BlockTypeToolResult
  | typeof BlockTypeToolUse
  | typeof BlockTypeUserAsk;
