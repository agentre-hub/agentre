// 一个部门都没有时的空态。

import { FolderPlus, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";

export function EmptyDepartments({
  onCreateDepartment,
  onCreateAgent,
}: {
  onCreateDepartment: () => void;
  onCreateAgent?: (departmentId: number) => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      className="m-5 flex flex-col items-center gap-2 rounded-lg border border-dashed p-6 text-center"
      data-slot="org-index-empty-departments"
    >
      <span className="text-sm font-semibold">
        {t("org.index.emptyDepartments.title")}
      </span>
      <span className="text-2xs text-muted-foreground">
        {t("org.index.emptyDepartments.description")}
      </span>
      <div className="mt-1 flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 px-2.5 text-2xs"
          onClick={onCreateDepartment}
        >
          <FolderPlus className="size-3" aria-hidden="true" />
          {t("org.index.emptyDepartments.newDepartment")}
        </Button>
        {onCreateAgent ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 px-2.5 text-2xs"
            onClick={() => onCreateAgent(0)}
          >
            <Plus className="size-3" aria-hidden="true" />
            {t("org.index.emptyDepartments.addAgent")}
          </Button>
        ) : null}
      </div>
    </div>
  );
}
