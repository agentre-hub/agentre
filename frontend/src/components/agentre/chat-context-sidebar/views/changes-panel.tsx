import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

import type { ChangesScope } from "@/stores/chat-sidebar-store";

import type { ChangeRow } from "../derive";

import { GitView } from "./git-view";
import { SessionChangesView } from "./session-changes-view";
import type { GitChangesState } from "./use-git-changes";

type Props = {
  sessionId: number;
  /** 「本次会话」档的行，由消息派生（零后端调用）。 */
  rows: ChangeRow[];
  cwd: string;
  remote: boolean;
  /** 已生效的档位：非 git 仓库 / 无工作目录时调用方已把它钳到「本次会话」。 */
  scope: ChangesScope;
  onScopeChange: (scope: ChangesScope) => void;
  /** 「未提交」档是否有意义；为 false 时那一档整个不渲染（spec 决策 11）。 */
  uncommittedAvailable: boolean;
  git: GitChangesState;
  onRetry: () => void;
  onJumpToTurn: (turn: number) => void;
};

/**
 * ChangesPanel 是「变更」页的外壳：第二行是 `本次会话 / 未提交` 两档胶囊，下面
 * 是当前档的内容区（两档都是扁平列表，行的形态统一，spec 决策 12）。
 *
 * 两档口径不同是有意为之（决策 2）：「本次会话」回答工具改了什么、纯消息派生；
 * 「未提交」回答工作区还有什么没提交、走 git。非 git 仓库时「未提交」失去意义，
 * 该行只剩一档而不是留一个空页（决策 11）。
 */
export function ChangesPanel({
  sessionId,
  rows,
  cwd,
  remote,
  scope,
  onScopeChange,
  uncommittedAvailable,
  git,
  onRetry,
  onJumpToTurn,
}: Props) {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-1.5 border-b border-border px-2">
        <div
          role="tablist"
          aria-label={t("chatContext.changes.scope.label")}
          className="inline-flex shrink-0 overflow-hidden rounded-full border border-border"
        >
          <ScopeTab
            label={t("chatContext.changes.scope.session")}
            active={scope === "session"}
            onClick={() => onScopeChange("session")}
          />
          {uncommittedAvailable ? (
            <ScopeTab
              label={t("chatContext.changes.scope.uncommitted")}
              active={scope === "uncommitted"}
              onClick={() => onScopeChange("uncommitted")}
            />
          ) : null}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {scope === "session" ? (
          <SessionChangesView
            sessionId={sessionId}
            rows={rows}
            cwd={cwd}
            remote={remote}
            onJumpToTurn={onJumpToTurn}
          />
        ) : (
          <GitView
            sessionId={sessionId}
            cwd={cwd}
            remote={remote}
            state={git}
            onRetry={onRetry}
          />
        )}
      </div>
    </div>
  );
}

/**
 * 胶囊组与既有的两档胶囊同形（圆角胶囊、自然宽度，而不是等宽 flex-1），
 * whitespace-nowrap，侧栏被拖窄到 190px 时不换行。
 */
function ScopeTab({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        "px-2 py-0.5 text-3xs whitespace-nowrap transition-colors",
        "focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
        active
          ? "bg-accent font-semibold text-foreground"
          : "text-muted-foreground hover:bg-muted/50",
      )}
    >
      {label}
    </button>
  );
}
