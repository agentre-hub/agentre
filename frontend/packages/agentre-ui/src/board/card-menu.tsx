import { MoreHorizontal } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
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
}

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
}: BoardCardMenuProps) {
  const { t } = useUiTranslation();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          data-testid={`board-card-menu-${cardId}`}
          aria-label={t("board.cardMenu")}
          className={cn(
            "inline-flex size-6 cursor-pointer items-center justify-center rounded-md text-muted-foreground opacity-0 transition-opacity",
            "hover:bg-secondary hover:text-foreground",
            "group-hover/card:opacity-100 group-focus-within/card:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100",
            "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
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
