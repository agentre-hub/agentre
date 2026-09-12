/**
 * 「导入本地会话…」这一条 —— 四条轴共用的**唯一**定义（规格 2026-08-26，决策 13）。
 *
 * 会话索引有四条轴（项目 / Agent / 时间 / 机器），各按自己那一维预填。项目组头本来
 * 就有一份 ⋮ 菜单，这一条插进那份全集里（见 `project/project-header-actions.tsx`）；
 * 其余组头此前没有 ⋮，由这里这一件独立地摆出来。两处的**文案与图标只定义一次**
 * —— 各摆一遍就是两处各写一句不同的话的机会。
 *
 * **能力开关**：没有 `onImport` 就整条不出现，连 ⋮ 都不摆（不置灰）。置灰在说
 * 「你以后可以」，而没有本地磁盘会话这回事的宿主永远不会有这个入口。
 */
import { HardDriveDownload, MoreVertical } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { groupActionRevealTouchClassName } from "../session-index/group-header";

/** 条目的稳定标识：两种容器里的 `data-testid` 都由它拼出来。 */
export const IMPORT_MENU_ITEM_ID = "import-local-session";

/** 条目那枚图标。两处共用一件，免得项目组头和机器组头各配一枚不同的图。 */
export function ImportLocalSessionIcon() {
  return <HardDriveDownload className="size-3.5" aria-hidden="true" />;
}

/** 条目文案。项目组头把它插进自己那份 `menuItems()`，所以单独出一份。 */
export function useImportLocalSessionLabel(): string {
  const { t } = useUiTranslation();
  return t("importSession.menuItem");
}

export interface ImportLocalSessionMenuProps {
  /**
   * 打开导入对话框（入口的预填由宿主在这里决定）。
   * **不给就整条不出现** —— 这是能力开关，不是可选回调。
   */
  onImport?: () => void;
  /** 这一组的名字，只进无障碍名（「Build box 的更多操作」）。 */
  label: string;
  testId?: string;
}

/**
 * 机器 / Agent / 随手对话三种组头上的那枚 ⋮。
 *
 * 它嵌在组头那颗收放按钮**之外**（`IndexGroupHeader` 的 `actions` 槽）：HTML 不允许
 * 按钮套按钮，而且点 ⋮ 不该顺手把这一组折起来。
 */
export function ImportLocalSessionMenu({
  onImport,
  label,
  testId = "import-menu",
}: ImportLocalSessionMenuProps) {
  const { t } = useUiTranslation();
  const itemLabel = useImportLocalSessionLabel();
  if (!onImport) return null;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <span
          data-testid={`${testId}-trigger`}
          role="button"
          tabIndex={0}
          aria-label={t("importSession.menuAria", { name: label })}
          className={cn(
            "shrink-0 cursor-pointer rounded-sm p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
            groupActionRevealTouchClassName,
          )}
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") e.stopPropagation();
          }}
        >
          <MoreVertical className="size-3.5" aria-hidden="true" />
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-[180px]">
        <DropdownMenuItem
          data-testid={`${testId}-item`}
          onSelect={() => onImport()}
        >
          <ImportLocalSessionIcon />
          <span>{itemLabel}</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
