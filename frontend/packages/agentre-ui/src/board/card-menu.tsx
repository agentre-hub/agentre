import * as React from "react";
import { Check, MoreHorizontal, Pencil, Trash2 } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import {
  DialogShell,
  DialogShellBody,
  DialogShellHeader,
} from "../ui/dialog-shell";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";

import { BOARD_STAGE_META } from "./stages";
import { BOARD_STAGES, type BoardPorts, type BoardStage } from "./types";

export interface BoardCardMenuProps {
  cardId: number;
  stage: BoardStage;
  ports: BoardPorts;
  className?: string;
  /**
   * 触屏档：⋯ 常显且点击区 ≥44px，菜单换成底部弹层 —— 「移动到」平铺四个阶段，
   * 编辑 / 删除另成一组。不传时与桌面一致。
   */
  touch?: boolean;
  /** 受控开合：触屏档长按卡片也要能打开同一块弹层。 */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

const sheetItem = cn(
  "flex min-h-12 w-full cursor-pointer items-center gap-3 px-4 text-left text-sm transition-colors",
  "hover:bg-field-fill-hover disabled:cursor-default disabled:hover:bg-transparent focus-visible:focus-outline",
);

/**
 * 卡片右上角那个 hover 才浮出的菜单。
 *
 * 触发器**不靠 `hidden` 藏**而是 opacity：隐藏的元素不在 Tab 序里，「hover 才
 * 出现」就变成键盘用户够不着的功能。所以卡片 focus-within 时它一并显形，
 * 触发器自己拿到焦点时也是。
 */
export function BoardCardMenu({
  cardId,
  stage,
  ports,
  className,
  touch = false,
  open,
  onOpenChange,
}: BoardCardMenuProps) {
  const { t } = useUiTranslation();
  const [innerOpen, setInnerOpen] = React.useState(false);
  const sheetOpen = open ?? innerOpen;
  const setSheetOpen = (next: boolean) => {
    setInnerOpen(next);
    onOpenChange?.(next);
  };

  if (touch) {
    return (
      <>
        <button
          type="button"
          data-testid={`board-card-menu-${cardId}`}
          aria-label={t("board.cardMenu")}
          aria-haspopup="dialog"
          aria-expanded={sheetOpen}
          onClick={() => setSheetOpen(true)}
          className={cn(
            "inline-flex size-11 cursor-pointer items-center justify-center rounded-md text-muted-foreground",
            "hover:bg-accent hover:text-foreground focus-visible:focus-outline",
            className,
          )}
        >
          <MoreHorizontal className="size-4" aria-hidden="true" />
        </button>
        {/* 底部弹层统一走 DialogShell：<640px 贴底，640–767px 居中浮卡。 */}
        <DialogShell open={sheetOpen} onOpenChange={setSheetOpen} size="sm">
          <div data-testid={`board-card-sheet-${cardId}`} className="contents">
            <DialogShellHeader
              title={t("board.moveCardTo", { id: cardId })}
              onClose={() => setSheetOpen(false)}
            />
            <DialogShellBody className="flex flex-col gap-3 px-4 pt-0 pb-[max(1rem,env(safe-area-inset-bottom))]">
              <div className="flex flex-col overflow-hidden rounded-xl bg-field-fill">
                {BOARD_STAGES.map((target) => (
                  <button
                    key={target}
                    type="button"
                    disabled={target === stage}
                    aria-current={target === stage ? "true" : undefined}
                    onClick={() => {
                      setSheetOpen(false);
                      ports.onMove(cardId, target);
                    }}
                    className={sheetItem}
                  >
                    <span className="flex-1">
                      {t(BOARD_STAGE_META[target].labelKey)}
                    </span>
                    {target === stage ? (
                      <Check
                        className="size-4 text-primary-text"
                        aria-hidden="true"
                      />
                    ) : null}
                  </button>
                ))}
              </div>
              <div className="flex flex-col overflow-hidden rounded-xl bg-field-fill">
                <button
                  type="button"
                  onClick={() => {
                    setSheetOpen(false);
                    ports.onEdit(cardId);
                  }}
                  className={sheetItem}
                >
                  <Pencil className="size-4" aria-hidden="true" />
                  {t("board.edit")}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setSheetOpen(false);
                    ports.onDelete(cardId);
                  }}
                  className={cn(sheetItem, "text-destructive")}
                >
                  <Trash2 className="size-4" aria-hidden="true" />
                  {t("board.delete")}
                </button>
              </div>
            </DialogShellBody>
          </div>
        </DialogShell>
      </>
    );
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-testid={`board-card-menu-${cardId}`}
          aria-label={t("board.cardMenu")}
          className={cn(
            "inline-flex size-6 cursor-pointer items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity",
            "hover:bg-accent hover:text-foreground",
            "group-hover/card:opacity-100 group-focus-within/card:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100",
            "focus-visible:focus-outline",
            className,
          )}
        >
          <MoreHorizontal className="size-3.5" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-[9rem]">
        <DropdownMenuItem onSelect={() => ports.onEdit(cardId)}>
          {t("board.edit")}
        </DropdownMenuItem>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>{t("board.moveTo")}</DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            {BOARD_STAGES.map((target) => (
              <DropdownMenuItem
                key={target}
                disabled={target === stage}
                onSelect={() => ports.onMove(cardId, target)}
              >
                {t(BOARD_STAGE_META[target].labelKey)}
              </DropdownMenuItem>
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        {/* 分隔线落在 --popover 上：--border 在暗色那里对比度只有 1.06，整条消失。 */}
        <DropdownMenuSeparator className="bg-border-strong" />
        <DropdownMenuItem
          variant="destructive"
          onSelect={() => ports.onDelete(cardId)}
        >
          {t("board.delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
