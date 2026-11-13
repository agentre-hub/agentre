import * as React from "react";
import { MessageSquare, MoreHorizontal, Star, Text, User } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";

import { formatMemoryDate } from "./format";
import { MemoryEditForm } from "./memory-edit-form";
import type {
  MemoryEntryView,
  MemoryRowAction,
  MemoryRowProps,
  MemoryWriteResult,
} from "./types";

/** 「删除」点第一下变成「确认删除？」，这么久之内再点才执行，否则退回。 */
const DELETE_CONFIRM_MS = 2000;

/**
 * 记忆页的一行：摘要、创建者（会话可点回）、最近修改日期、有无详情；点行就地展开
 * 编辑；「更多」菜单里是置顶、归档 / 恢复、恢复上一版、移到其他层、删除。
 *
 * 写入一律经 `onSave` / `onAction` 交给宿主；失败就地写在行下面（design.md §9
 * 「失败报在发生的地方」）。删除是彻底删除，所以在菜单里就地二次确认，不弹对话框。
 */
export function MemoryRow({
  entry,
  editing,
  onEditingChange,
  onSave,
  onAction,
  moveTargets,
  dimmed = false,
}: MemoryRowProps) {
  const { t } = useUiTranslation();
  const [failure, setFailure] = React.useState<{ reason?: string } | null>(
    null,
  );

  const report = (result: MemoryWriteResult) => {
    setFailure(result.ok ? null : { reason: result.reason });
    return result;
  };

  const runAction = async (action: MemoryRowAction) => {
    report(await onAction(action));
  };

  const save = async (draft: { summary: string; detail: string }) => {
    const result = await onSave(draft);
    if (result.ok) {
      setFailure(null);
      onEditingChange(false);
    }
    return result;
  };

  return (
    <div
      data-testid={`memory-row-${entry.id}`}
      data-dimmed={dimmed ? "true" : undefined}
      className={cn(
        "group/memory-row flex items-start gap-2 rounded-lg px-2 py-1.5",
        !editing && "hover:bg-accent/60 hover:on-accent",
      )}
    >
      <span className="flex h-5 w-4 shrink-0 items-center justify-center">
        {entry.pinned ? (
          <Star
            className="size-3.5 fill-current text-primary-text"
            role="img"
            aria-label={t("memory.row.pinned")}
          />
        ) : null}
      </span>
      <div
        className={cn(
          "flex min-w-0 flex-1 flex-col gap-0.5",
          // 正在编辑的那一行不变淡：输入框要和别处一样看得清。
          dimmed && !editing && "opacity-60",
        )}
      >
        {editing ? (
          <MemoryEditForm
            summary={entry.summary}
            detail={entry.detail}
            meta={<MemoryRowMeta entry={entry} showDetailChip={false} />}
            onSave={save}
            onCancel={() => onEditingChange(false)}
          />
        ) : (
          <>
            <button
              type="button"
              aria-label={t("memory.row.edit", { summary: entry.summary })}
              className="w-full rounded-sm text-left text-sm text-foreground"
              onClick={() => onEditingChange(true)}
            >
              {entry.summary}
            </button>
            <MemoryRowMeta entry={entry} showDetailChip />
          </>
        )}
        {failure ? (
          <p role="alert" className="text-xs text-destructive-text">
            {failure.reason
              ? t("memory.row.actionFailedWithReason", {
                  reason: failure.reason,
                })
              : t("memory.row.actionFailed")}
          </p>
        ) : null}
      </div>
      {editing ? null : (
        <MemoryRowMenu
          entry={entry}
          moveTargets={moveTargets}
          onAction={(action) => void runAction(action)}
        />
      )}
    </div>
  );
}

function MemoryRowMeta({
  entry,
  showDetailChip,
}: {
  entry: MemoryEntryView;
  showDetailChip: boolean;
}) {
  const { t } = useUiTranslation();
  const date = formatMemoryDate(entry.updatedAt);
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-2xs text-muted-foreground">
      {entry.creator.kind === "user" ? (
        <MetaIcon icon={User} name={t("memory.row.writtenByYou")} />
      ) : (
        <span className="flex min-w-0 items-center gap-1">
          <MetaIcon icon={MessageSquare} name={t("memory.row.fromSession")} />
          {entry.creator.onOpen ? (
            <button
              type="button"
              className="truncate rounded-sm underline underline-offset-2 hover:text-foreground"
              onClick={entry.creator.onOpen}
            >
              {entry.creator.label}
            </button>
          ) : (
            <span className="truncate">{entry.creator.label}</span>
          )}
        </span>
      )}
      {date ? (
        <>
          <span aria-hidden="true" className="text-decorative-foreground">
            ·
          </span>
          <span className="font-mono">{date}</span>
        </>
      ) : null}
      {showDetailChip && entry.detail.trim() !== "" ? (
        <MetaIcon icon={Text} name={t("memory.row.hasDetail")} />
      ) : null}
    </div>
  );
}

/** 来源 / 详情这类身份标记：只画图标，原来的字成为可访问名与悬停提示。 */
function MetaIcon({
  icon: Icon,
  name,
}: {
  icon: React.ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
  name: string;
}) {
  return (
    <span
      role="img"
      aria-label={name}
      title={name}
      className="inline-flex size-3 shrink-0 items-center justify-center"
    >
      <Icon aria-hidden className="size-full" />
    </span>
  );
}

function MemoryRowMenu({
  entry,
  moveTargets,
  onAction,
}: {
  entry: MemoryEntryView;
  moveTargets: MemoryRowProps["moveTargets"];
  onAction: (action: MemoryRowAction) => void;
}) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const [confirmingDelete, setConfirmingDelete] = React.useState(false);

  React.useEffect(() => {
    if (!confirmingDelete) return;
    const timer = window.setTimeout(
      () => setConfirmingDelete(false),
      DELETE_CONFIRM_MS,
    );
    return () => window.clearTimeout(timer);
  }, [confirmingDelete]);

  return (
    <DropdownMenu
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setConfirmingDelete(false);
      }}
    >
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("memory.row.more")}
          className="shrink-0 text-muted-foreground opacity-0 group-hover/memory-row:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100"
        >
          <MoreHorizontal aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44">
        <DropdownMenuItem
          onSelect={() => onAction({ type: "pin", pinned: !entry.pinned })}
        >
          {entry.pinned ? t("memory.menu.unpin") : t("memory.menu.pin")}
        </DropdownMenuItem>
        <DropdownMenuItem
          onSelect={() =>
            onAction({ type: entry.archived ? "unarchive" : "archive" })
          }
        >
          {entry.archived
            ? t("memory.menu.unarchive")
            : t("memory.menu.archive")}
        </DropdownMenuItem>
        {entry.hasPrevious ? (
          <DropdownMenuItem
            onSelect={() => onAction({ type: "restorePrevious" })}
          >
            {t("memory.menu.restorePrevious")}
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            {t("memory.menu.moveTo")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="max-h-72 min-w-44 overflow-y-auto">
            {moveTargets.length === 0 ? (
              <DropdownMenuItem disabled>
                {t("memory.menu.noMoveTargets")}
              </DropdownMenuItem>
            ) : (
              moveTargets.map((target, index) => (
                <React.Fragment key={target.key}>
                  {target.group &&
                  target.group !== moveTargets[index - 1]?.group ? (
                    <DropdownMenuLabel className="text-2xs font-medium text-muted-foreground">
                      {target.group}
                    </DropdownMenuLabel>
                  ) : null}
                  <DropdownMenuItem
                    style={
                      target.depth
                        ? { paddingLeft: `${0.5 + target.depth * 0.75}rem` }
                        : undefined
                    }
                    onSelect={() =>
                      onAction({ type: "move", targetKey: target.key })
                    }
                  >
                    {target.label}
                  </DropdownMenuItem>
                </React.Fragment>
              ))
            )}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          // 确认态是实心的危险色（与结束进程那颗按钮同一种二次确认）。
          variant={confirmingDelete ? "default" : "destructive"}
          className={cn(
            confirmingDelete &&
              "bg-destructive font-medium text-destructive-foreground hover:bg-destructive/90 hover:text-destructive-foreground focus:bg-destructive/90 focus:text-destructive-foreground dark:bg-destructive/60",
          )}
          onSelect={(event) => {
            if (!confirmingDelete) {
              // 第一下只把这一项变成确认：菜单不关，人还在原处。
              event.preventDefault();
              setConfirmingDelete(true);
              return;
            }
            setConfirmingDelete(false);
            onAction({ type: "delete" });
          }}
        >
          {confirmingDelete
            ? t("memory.menu.confirmDelete")
            : t("memory.menu.delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
