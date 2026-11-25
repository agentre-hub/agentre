import type * as React from "react";
import { ShieldAlert } from "lucide-react";

import type { TranscriptBlock } from "../../dto";
import { Button } from "../../../ui/button";
import { Spinner } from "../../../ui/spinner";
import { cn } from "../../../lib/utils";
import { useUiTranslation } from "../../../i18n";

import { ToolInputBody } from "./card";
import { useToolPermissionDecision } from "./use-tool-permission-decision";

export type ToolPermissionDockProps = {
  /** 待决的 `canonical.tool.permission` 块;已决或不是工具权限时什么都不画。 */
  toolBlock: TranscriptBlock;
  sessionId: number;
  /** 本条请求所属的 assistant 消息 id,乐观更新用;与审批卡同义。 */
  messageId?: number;
  /** 副标题里工具名后面的工作目录;不传只写工具名。 */
  cwd?: string;
  /** 停止槽:画在决策按钮下面,由宿主给(比如「■ 停止」)。 */
  stop?: React.ReactNode;
  /**
   * 对话的执行端连接还没就绪。传了这个入参(true / false 都算)就打开宿主级反馈:
   * true 时三键禁用并在上方写「正在连接」;点下的那颗单独转圈;失败给「重试」;
   * 超时写「没收到确认」。不传:保持原样。
   */
  connecting?: boolean;
  className?: string;
};

// 触屏命中区 ≥ 44px;并排的两格允许文案折行,窄屏上不溢出。
const TARGET_CLASS = "min-h-11 h-auto whitespace-normal py-2";

// ToolPermissionDock 是工具权限审批的停靠形态:放在输入框的位置,「允许」整宽,
// 「本会话始终允许」与「拒绝」并排,下面可挂停止槽。提交与审批卡走同一个
// useToolPermissionDecision;触屏形态,不画键帽、不接管 Enter / Esc。
// 宿主用它时给转录的渲染上下文传 decisionPlacement="docked",转录就不再画这张卡。
export function ToolPermissionDock({
  toolBlock,
  sessionId,
  messageId,
  cwd,
  stop,
  connecting,
  className,
}: ToolPermissionDockProps) {
  const { t } = useUiTranslation();
  const {
    payload,
    isResolved,
    submitting,
    pending,
    error,
    errorDetail,
    timedOut,
    submit,
    retry,
  } = useToolPermissionDecision({ toolBlock, sessionId, messageId });
  const managed = connecting !== undefined;
  const notReady = !!connecting;
  const busy = submitting || notReady;
  const spin = (allow: boolean, always: boolean) =>
    managed &&
    pending?.allow === allow &&
    pending.alwaysAllowSession === always ? (
      <Spinner className="size-4" />
    ) : null;

  if (!payload || !payload.requestId || isResolved) return null;

  const input = payload.toolInput as Record<string, unknown> | undefined;
  const title =
    payload.toolName === "Bash"
      ? t("canonical.toolPermission.dock.titleCommand")
      : t("canonical.toolPermission.dock.titleTool", {
          tool: payload.toolName,
        });
  const subtitle = [payload.toolName, cwd].filter(Boolean).join(" · ");

  return (
    <section
      data-testid="tool-permission-dock"
      className={cn("flex flex-col gap-3 text-card-foreground", className)}
    >
      <div className="flex items-center gap-1.5 text-sm font-medium text-status-waiting-text">
        <ShieldAlert className="h-4 w-4 shrink-0 text-status-waiting" />
        {t("canonical.toolPermission.dock.eyebrow")}
      </div>
      <div className="flex min-w-0 flex-col gap-0.5">
        <p className="text-base font-semibold">{title}</p>
        <p className="truncate text-aux text-muted-foreground">{subtitle}</p>
      </div>
      {input ? (
        <div className="max-h-40 overflow-y-auto">
          <ToolInputBody toolName={payload.toolName} input={input} />
        </div>
      ) : null}
      {notReady ? (
        <p
          data-testid="tool-permission-dock-connecting"
          className="flex items-center gap-2 text-aux text-muted-foreground"
        >
          <Spinner className="size-3.5" />
          {t("canonical.toolPermission.dock.connecting")}
        </p>
      ) : null}
      <Button
        className={cn(TARGET_CLASS, "w-full")}
        disabled={busy}
        onClick={() => submit(true, false)}
      >
        {spin(true, false)}
        {t("canonical.toolPermission.allowOnce")}
      </Button>
      <div className="grid grid-cols-2 gap-2">
        <Button
          variant="secondary"
          className={TARGET_CLASS}
          disabled={busy}
          onClick={() => submit(true, true)}
        >
          {spin(true, true)}
          {t("canonical.toolPermission.allowSession")}
        </Button>
        <Button
          variant="secondary"
          className={cn(TARGET_CLASS, "text-destructive-text")}
          disabled={busy}
          onClick={() => submit(false, false)}
        >
          {spin(false, false)}
          {t("common.reject")}
        </Button>
      </div>
      {error ? (
        <p
          role="alert"
          title={errorDetail}
          className="text-aux text-destructive-text"
        >
          {managed && timedOut
            ? t("canonical.toolPermission.dock.timedOut")
            : error}
        </p>
      ) : null}
      {managed && error ? (
        <Button
          variant="outline"
          className={TARGET_CLASS}
          disabled={busy}
          onClick={retry}
        >
          {t("common.retry")}
        </Button>
      ) : null}
      {stop}
    </section>
  );
}
