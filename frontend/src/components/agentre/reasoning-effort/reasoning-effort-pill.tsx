import { ReasoningEffortPicker } from "@agentre-hub/agentre-ui";

import type { UseReasoningEffortPillReturn } from "./use-reasoning-effort-pill";

export type ReasoningEffortPillProps = UseReasoningEffortPillReturn;

/**
 * ReasoningEffortPill: composer 底栏 trailing 侧的会话级思考力度控件
 * (spec 2026-09-01 决策 6/9)。
 *
 * 视图与档位表整个住在共享包里(决策 8);宿主只把状态、传输与持久化接进去——
 * 这里只是把 useReasoningEffortPill 的返回值原样转给共享 Picker。
 */
export function ReasoningEffortPill({
  value,
  backendValue,
  onChange,
  error,
}: ReasoningEffortPillProps) {
  return (
    <ReasoningEffortPicker
      value={value}
      backendValue={backendValue}
      onChange={onChange}
      errorText={error ?? undefined}
      dataTestId="reasoning-effort-pill"
    />
  );
}
