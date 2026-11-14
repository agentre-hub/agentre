import * as React from "react";
import { useEffect, useRef } from "react";
import { Check, ChevronDown, ShieldAlert, X } from "lucide-react";

import { Button } from "../../../ui/button";
import { StatusMark } from "../../../ui/status-mark";
import { CollapsibleCode } from "../../collapsible-code";
import {
  TranscriptCard,
  TranscriptCardBody,
  TranscriptCardHeader,
} from "../../transcript-card";
import { cn } from "../../../lib/utils";
import { shouldIgnoreClickForSelection } from "../../../lib/copyable-text";
import { useCollapsible } from "../../../hooks/use-collapsible";
import { useTranscriptBooleanState } from "../../transcript-ui-state";
import { useUiTranslation } from "../../../i18n";

import type { CanonicalCardProps } from "../props";

import { useShortcutHintsHidden } from "./shortcut-hints";
import { useToolPermissionDecision } from "./use-tool-permission-decision";

// formatToolSummary 给 header 当一行摘要:Bash 突出 command,Edit/Write 突出路径,
// 其它工具退回压缩 JSON(截断 100 字符),让卡片不展开也能看清意图。
function formatToolSummary(
  toolName: string,
  input: Record<string, unknown> | undefined,
): string {
  if (!input) return "";
  if (toolName === "Bash" && typeof input.command === "string") {
    return input.command;
  }
  if (
    (toolName === "Edit" || toolName === "Write") &&
    typeof input.file_path === "string"
  ) {
    return input.file_path;
  }
  try {
    const compact = JSON.stringify(input);
    return compact.length > 100 ? compact.slice(0, 97) + "..." : compact;
  } catch {
    return "";
  }
}

const DIFF_MAX_LINES = 8;

function clipLines(text: string): string[] {
  const lines = text.split("\n");
  return lines.length > DIFF_MAX_LINES
    ? [...lines.slice(0, DIFF_MAX_LINES), "…"]
    : lines;
}

// ToolInputBody 按工具给可读正文:Edit=路径+小 diff,Write=路径+行数,
// Bash=命令,其余(或入参形状不符)退回格式化 JSON。停靠形态也用它。
export function ToolInputBody({
  toolName,
  input,
}: {
  toolName: string;
  input: Record<string, unknown> | undefined;
}) {
  const { t } = useUiTranslation();
  if (!input) return null;
  const path = typeof input.file_path === "string" ? input.file_path : null;
  const pathLine = (
    <div className="break-all font-mono text-aux text-muted-foreground">
      {path}
    </div>
  );

  if (
    toolName === "Edit" &&
    path &&
    typeof input.old_string === "string" &&
    typeof input.new_string === "string"
  ) {
    return (
      <div className="flex flex-col gap-1.5">
        {pathLine}
        <div className="overflow-x-auto rounded-sm bg-muted px-2.5 py-2 font-mono text-aux">
          <div data-testid="tool-permission-diff-del">
            {clipLines(input.old_string).map((l, i) => (
              <div key={i} className="whitespace-pre text-destructive-text">
                {"- " + l}
              </div>
            ))}
          </div>
          <div data-testid="tool-permission-diff-add">
            {clipLines(input.new_string).map((l, i) => (
              <div key={i} className="whitespace-pre text-status-running-text">
                {"+ " + l}
              </div>
            ))}
          </div>
        </div>
      </div>
    );
  }
  if (toolName === "Write" && path && typeof input.content === "string") {
    return (
      <div className="flex flex-col gap-1.5">
        {pathLine}
        <div className="text-aux text-muted-foreground">
          {t("canonical.toolPermission.lines", {
            count: input.content.split("\n").length,
          })}
        </div>
      </div>
    );
  }
  if (toolName === "Bash" && typeof input.command === "string") {
    return (
      <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-sm bg-muted px-2.5 py-2 font-mono text-aux">
        {input.command}
      </pre>
    );
  }
  return (
    <CollapsibleCode
      value={JSON.stringify(input, null, 2)}
      surface="muted"
      bodyClassName="rounded-sm px-2.5 py-2"
    />
  );
}

// 焦点在可输入元素里(输入框/选择框/contenteditable)时不抢键盘。
function isTypingTarget(el: Element | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  return (
    el.tagName === "INPUT" ||
    el.tagName === "TEXTAREA" ||
    el.tagName === "SELECT" ||
    el.isContentEditable
  );
}

const KBD_CLASS =
  "ml-1 rounded-sm bg-button-fill px-1 font-mono text-2xs text-muted-foreground";

// ToolPermissionCard 渲染 canonical.tool.permission —— 通用工具审批(非
// ExitPlanMode,后者走 PlanApproveCard)。来源:claudecode can_use_tool 控制请求。
//
// 数据来源同 PlanApproveCard:
//   - liveBlocks(resolved=false → 渲染交互态 仅本次允许 / 本会话始终允许 / 拒绝)
//   - 历史持久化 ChatBlock(resolved=true → 渲染只读 banner,标明决策)
export const ToolPermissionCard: React.FC<CanonicalCardProps> = ({
  toolBlock,
  sessionId,
  messageId,
  uiStateKey,
}) => {
  const { t } = useUiTranslation();
  // submit 必须在任何 early-return 前取到,否则两次 render 之间 hook 数会变化。
  const { payload, isResolved, submitting, error, errorDetail, submit } =
    useToolPermissionDecision({ toolBlock, sessionId, messageId });
  const hideShortcutHints = useShortcutHintsHidden();

  const [collapsed, setCollapsed] = useTranscriptBooleanState(
    uiStateKey,
    isResolved,
  );
  const { mounted, onTransitionEnd } = useCollapsible(!collapsed);

  const allowRef = useRef<HTMLButtonElement>(null);

  // 卡片出现且用户不在打字(输入框/另一张审批卡已持有焦点)时,焦点落到「允许」。
  useEffect(() => {
    if (isResolved) return;
    const active = document.activeElement;
    if (isTypingTarget(active)) return;
    if (active?.closest("[data-approval-card]")) return;
    allowRef.current?.focus({ preventScroll: true });
  }, [isResolved]);

  if (!payload || !payload.requestId) return null;

  // Enter 允许 / Esc 拒绝只在焦点位于本卡内时响应(多张待批卡并存时只有持焦点的
  // 那张动作);输入法组词中、输入框内、以及其它按钮上的 Enter(原生即触发它自己)不接管。
  const onKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    if (isResolved || submitting || event.defaultPrevented) return;
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;
    const target = event.target as Element;
    if (isTypingTarget(target)) return;
    if (event.key === "Escape") {
      event.preventDefault();
      void submit(false, false);
    } else if (event.key === "Enter") {
      if (target.closest("button") && target !== allowRef.current) return;
      event.preventDefault();
      void submit(true, false);
    }
  };

  const summary = formatToolSummary(
    payload.toolName,
    payload.toolInput as Record<string, unknown> | undefined,
  );
  const hasInput = !!payload.toolInput;

  return (
    <TranscriptCard
      data-testid="tool-permission-card"
      data-selectable-text="true"
      data-approval-card={isResolved ? undefined : ""}
      onKeyDown={onKeyDown}
      className={cn(
        "text-card-foreground",
        isResolved && !payload.allowed
          ? "bg-destructive-soft"
          : "bg-status-waiting-bg",
      )}
    >
      <TranscriptCardHeader
        onClick={(event) => {
          if (shouldIgnoreClickForSelection(event)) return;
          setCollapsed((c) => !c);
        }}
        aria-expanded={!collapsed}
      >
        <ShieldAlert
          className={cn(
            "h-4 w-4 shrink-0",
            isResolved
              ? payload.allowed
                ? "text-status-running"
                : "text-destructive"
              : "text-status-waiting",
          )}
        />
        <span data-copyable-control-text="true" className="font-medium">
          {payload.toolName}
        </span>
        {summary && (
          <span
            data-copyable-control-text="true"
            className="truncate text-aux text-muted-foreground"
          >
            {summary}
          </span>
        )}
        <span className="ml-auto flex items-center gap-2 text-aux text-muted-foreground">
          {isResolved && (
            <>
              <StatusMark state={payload.allowed ? "granted" : "denied"} />
              {payload.allowed && payload.alwaysAllow && (
                <span data-copyable-control-text="true">
                  {t("statusScope.thisSession")}
                </span>
              )}
            </>
          )}
          <ChevronDown
            className={cn(
              "h-4 w-4 transition-transform",
              collapsed ? "" : "rotate-180",
            )}
          />
        </span>
      </TranscriptCardHeader>

      <div
        aria-hidden={collapsed}
        onTransitionEnd={onTransitionEnd}
        className="grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: collapsed ? "0fr" : "1fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          {mounted && hasInput ? (
            <TranscriptCardBody data-testid="tool-permission-body">
              <ToolInputBody
                toolName={payload.toolName}
                input={payload.toolInput as Record<string, unknown>}
              />
            </TranscriptCardBody>
          ) : null}
        </div>
      </div>

      {!isResolved && (
        <TranscriptCardBody className="flex flex-wrap items-center gap-2">
          <Button
            ref={allowRef}
            size="sm"
            disabled={submitting}
            onClick={() => submit(true, false)}
          >
            <Check className="mr-1 h-3.5 w-3.5" />
            {t("canonical.toolPermission.allowOnce")}
            {!hideShortcutHints && (
              <kbd
                aria-hidden="true"
                className={cn(
                  KBD_CLASS,
                  "text-primary-foreground/80 bg-primary-foreground/15",
                )}
              >
                ↵
              </kbd>
            )}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={submitting}
            onClick={() => submit(true, true)}
          >
            {t("canonical.toolPermission.allowSession")}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="text-destructive-text"
            disabled={submitting}
            onClick={() => submit(false, false)}
          >
            <X className="mr-1 h-3.5 w-3.5" />
            {t("common.reject")}
            {!hideShortcutHints && (
              <kbd aria-hidden="true" className={KBD_CLASS}>
                Esc
              </kbd>
            )}
          </Button>
          {error && (
            <span
              title={errorDetail}
              className="text-aux text-destructive-text"
            >
              {error}
            </span>
          )}
        </TranscriptCardBody>
      )}
    </TranscriptCard>
  );
};
