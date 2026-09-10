// 模型工具条：没有选中项时是「搜索 + 手动添加 + 计数」，一旦选中就原地换成批量操作条。
// 两态互斥占同一行，所以放在同一个组件里。
import { Plus } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { SearchInput } from "../../ui/search-input";
import { Button } from "../../ui/button";

export function ModelsToolbar({
  search,
  onSearchChange,
  selectedCount,
  visibleCount,
  totalCount,
  onSelectAll,
  onClearSelection,
  onBatchEnable,
  onBatchDisable,
  onBatchDelete,
  onAddModel,
}: {
  search: string;
  onSearchChange: (value: string) => void;
  selectedCount: number;
  visibleCount: number;
  totalCount: number;
  onSelectAll: () => void;
  onClearSelection: () => void;
  onBatchEnable: () => void;
  onBatchDisable: () => void;
  onBatchDelete: () => void;
  onAddModel: () => void;
}) {
  const { t } = useTranslation();
  return (
    <>
      {selectedCount > 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2 sm:px-4">
          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
            {t("llmProviders.modelsTable.batch.selectedCount", {
              selected: selectedCount,
              total: visibleCount,
            })}
          </span>
          <div className="flex flex-wrap items-center gap-1.5">
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={onSelectAll}
            >
              {t("llmProviders.modelsTable.batch.selectAll")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={onClearSelection}
            >
              {t("llmProviders.modelsTable.batch.clearSelection")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={onBatchEnable}
            >
              {t("llmProviders.modelsTable.batch.enable")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={onBatchDisable}
            >
              {t("llmProviders.modelsTable.batch.disable")}
            </Button>
            <Button
              type="button"
              variant="destructive"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={onBatchDelete}
            >
              {t("llmProviders.modelsTable.batch.delete")}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2 sm:px-4">
          <SearchInput
            value={search}
            onChange={onSearchChange}
            placeholder={t("llmProviders.workspace.searchPlaceholder")}
            aria-label={t("llmProviders.workspace.searchAria")}
            className="min-w-0 flex-1"
          />
          {/* 手动添加常驻工具栏：有模型之后也必须够得着 */}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-8 shrink-0 gap-1.5 px-3 text-xs"
            onClick={onAddModel}
          >
            <Plus
              className="size-3.5"
              data-icon="inline-start"
              aria-hidden="true"
            />
            {t("llmProviders.workspace.addModelManual")}
          </Button>
          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
            {t("llmProviders.workspace.modelCount", {
              shown: visibleCount,
              total: totalCount,
            })}
          </span>
        </div>
      )}{" "}
    </>
  );
}
