import { AlertCircle } from "lucide-react";

import { useUiTranslation } from "../../i18n";
import { cn } from "../../lib/utils";
import { Button } from "../../ui/button";
import { useTouch } from "../../ui/touch";

/**
 * 清单读失败的原位说明 + 重试。读失败时清单是空的但**不是没有**：画「还没有配置任何…」
 * 空态会诱导用户重复新建，所以失败走这一块而不是空态。触屏档重试键 44px。
 */
export function LoadFailureNotice({
  testId,
  title,
  description,
  onRetry,
}: {
  testId: string;
  title: string;
  description?: string;
  onRetry: () => void;
}) {
  const { t } = useUiTranslation();
  const touch = useTouch();
  return (
    <div
      data-testid={testId}
      className="flex flex-col items-center justify-center gap-3 px-6 py-10 text-center"
    >
      <AlertCircle
        className="size-5 text-muted-foreground"
        aria-hidden="true"
      />
      <div className="flex max-w-md flex-col gap-1">
        <p className="text-sm font-semibold">{title}</p>
        {description ? (
          <p className="text-2xs leading-relaxed text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className={cn("px-3 text-xs", touch && "min-h-11 px-4 text-sm")}
        onClick={onRetry}
      >
        {t("common.retry")}
      </Button>
    </div>
  );
}
