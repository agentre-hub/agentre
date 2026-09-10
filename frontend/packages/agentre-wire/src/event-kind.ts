import { getOption, hasOption } from "@bufbuild/protobuf";

import {
  event_kind,
  RuntimeEventNotificationSchema,
} from "./gen/agentre/wire/wire_pb";
import type { EventKind } from "./event-kinds.gen";

/**
 * RuntimeEventNotification 的 oneof 分支名 → 转录判别值。
 *
 * 这张表从前是**手写**的:分支名与判别值不是同一套拼法,也没有可推导的规则
 * （`toolCall` → `tool_use_start`、`userAskRequest` → `ask_user_question`、
 * `usageUpdate` → `usage`），所以每个消费方各抄一份;抄错编译器发现不了,归约器的
 * switch 落进 default 分支,工具卡与提问卡整块不渲染 —— 四条错映射就这样上过线。
 *
 * 现在判别值写在 .proto 的 `(agentre.wire.event_kind)` 字段选项上,这里从生成的
 * descriptor 读出来。Go 侧读同一格（`pkg/wire/eventkind`），两侧不再各有一份。
 */
export const EVENT_KIND_BY_CASE: Readonly<Record<string, EventKind>> =
  Object.freeze(
    Object.fromEntries(
      RuntimeEventNotificationSchema.fields
        .filter((field) => field.oneof?.name === "event")
        .filter((field) => hasOption(field, event_kind))
        .map((field) => [
          field.localName,
          getOption(field, event_kind) as EventKind,
        ]),
    ),
  );

/** 上表的反向：判别值 → oneof 分支名。回放路径要把中间形状翻回 oneof。 */
export const EVENT_CASE_BY_KIND: Readonly<Record<string, string>> =
  Object.freeze(
    Object.fromEntries(
      Object.entries(EVENT_KIND_BY_CASE).map(([branch, kind]) => [
        kind,
        branch,
      ]),
    ),
  );

/**
 * 交出一条 oneof 分支的判别值。
 *
 * 认不出时原样交回分支名,而不是谎报成某个已知判别值:运行期照样可能来一个比本包新
 * 的 daemon,那时消费方的 default 分支会把它原样呈现。
 */
export function eventKindOfCase(eventCase: string): string {
  return EVENT_KIND_BY_CASE[eventCase] ?? eventCase;
}

/** 交出一个判别值对应的 oneof 分支名，认不出时原样交回。 */
export function eventCaseOfKind(kind: string): string {
  return EVENT_CASE_BY_KIND[kind] ?? kind;
}
