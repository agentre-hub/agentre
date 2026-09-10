import * as React from "react";
import { useUiTranslation } from "../i18n";
import { Copy } from "lucide-react";

import { copyTextWithToast } from "../lib/clipboard-toast";
import { cn } from "../lib/utils";

// collapsible-code.tsx —— 对话流工具卡片共用的长内容滚动块。参数和结果始终
// 保留完整原文，内容超过固定高度后直接滚动，不再维护展开状态或渐变遮罩。

// stringifyToolValue 序列化单个工具入参值：字符串 / 数字 / 布尔原样，对象 / 数组
// 缩进 JSON（紧凑 JSON 压成一行既不可读也无法展开细看）。
export function stringifyToolValue(value: unknown): string {
  if (value == null) return "";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function toolInputEntries(
  input?: Record<string, unknown>,
): [string, string][] {
  if (!input) return [];
  return Object.entries(input)
    .filter(([, value]) => typeof value !== "undefined")
    .map(([key, value]) => [key, stringifyToolValue(value)]);
}

export type CollapsibleCodeSurface = "card" | "muted" | "destructive";

const SURFACE_BODY: Record<CollapsibleCodeSurface, string> = {
  card: "text-foreground",
  muted: "bg-muted/40 text-foreground",
  destructive: "bg-destructive-soft text-status-error",
};

export const CollapsibleCode: React.FC<{
  value: string;
  showCopy?: boolean;
  surface?: CollapsibleCodeSurface;
  bodyClassName?: string;
  className?: string;
  testId?: string;
}> = ({
  value,
  showCopy = true,
  surface = "card",
  bodyClassName,
  className,
  testId,
}) => {
  const { t } = useUiTranslation();

  async function handleCopy() {
    await copyTextWithToast(value, {
      errorTitle: t("common.copyFailed"),
      successTitle: t("common.copied"),
    });
  }

  return (
    <div className={cn("relative min-w-0", className)} data-testid={testId}>
      <div
        data-testid={testId ? `${testId}-body` : undefined}
        data-selectable-text="true"
        className={cn(
          "max-h-64 overflow-auto whitespace-pre-wrap break-words",
          SURFACE_BODY[surface],
          bodyClassName,
        )}
      >
        {value}
      </div>

      {showCopy && (
        <div className="absolute right-1 top-1 z-[2] flex items-center gap-1">
          <button
            type="button"
            data-testid={testId ? `${testId}-copy` : undefined}
            onClick={() => void handleCopy()}
            aria-label={t("canonical.code.copy")}
            title={t("canonical.code.copy")}
            className="inline-flex size-5 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            <Copy className="size-3" aria-hidden="true" />
          </button>
        </div>
      )}
    </div>
  );
};

// CollapsibleCodeParams 渲染工具入参的 key-value 表：每个值过 CollapsibleCode
// （长值独立折叠），空表返回 null（由调用方渲染空态占位）。
export const CollapsibleCodeParams: React.FC<{
  entries: [string, string][];
  testIdPrefix?: string;
}> = ({ entries, testIdPrefix }) => {
  if (entries.length === 0) return null;
  return (
    <dl className="grid grid-cols-[minmax(72px,auto)_1fr] gap-x-3 gap-y-1">
      {entries.map(([key, value]) => (
        <React.Fragment key={key}>
          <dt className="text-muted-foreground">{key}</dt>
          <dd className="min-w-0">
            <CollapsibleCode
              value={value}
              showCopy={false}
              testId={testIdPrefix ? `${testIdPrefix}:${key}` : undefined}
            />
          </dd>
        </React.Fragment>
      ))}
    </dl>
  );
};
