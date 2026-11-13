import * as React from "react";
import { Loader2 } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Textarea } from "../ui/textarea";

import type { MemoryDraft, MemoryWriteResult } from "./types";

export type MemoryEditFormProps = {
  /** 打开表单时的摘要。只在挂载时读一次：之后到达的新值不覆盖正在输入的内容。 */
  summary: string;
  /** 打开表单时的详情，同上只读一次。 */
  detail: string;
  /** 表单左下角的一行说明（谁写的、何时改过），由调用方给。 */
  meta?: React.ReactNode;
  onSave: (draft: MemoryDraft) => Promise<MemoryWriteResult>;
  onCancel: () => void;
  autoFocus?: boolean;
};

/**
 * 记忆的就地编辑：一行摘要 + 多行详情。
 *
 * 不认识宿主：保存走 `onSave`，结果按 `MemoryWriteResult` 回来。失败时两个字段原样
 * 留着并就地说明（spec「保存失败时保留输入内容并就地说明」）；记忆页的行与对话里的
 * 记忆卡片共用这一份。
 *
 * 初值只在挂载时取：其他设备同步下来的变更会让父组件带着新的 `summary` / `detail`
 * 重渲，正在编辑的人不该看到自己打的字被换掉——保存时以后写的为准。
 */
export function MemoryEditForm({
  summary: initialSummary,
  detail: initialDetail,
  meta,
  onSave,
  onCancel,
  autoFocus = true,
}: MemoryEditFormProps) {
  const { t } = useUiTranslation();
  const [summary, setSummary] = React.useState(initialSummary);
  const [detail, setDetail] = React.useState(initialDetail);
  const [saving, setSaving] = React.useState(false);
  const [failure, setFailure] = React.useState<{ reason?: string } | null>(
    null,
  );
  const summaryId = React.useId();
  const detailId = React.useId();

  const canSave = summary.trim() !== "" && !saving;

  const save = async () => {
    if (!canSave) return;
    setSaving(true);
    setFailure(null);
    const result = await onSave({ summary, detail });
    setSaving(false);
    if (!result.ok) setFailure({ reason: result.reason });
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onCancel();
    }
  };

  return (
    <div
      className="flex flex-col gap-2 rounded-lg bg-field-fill p-3"
      data-slot="memory-edit-form"
      onKeyDown={onKeyDown}
    >
      <label htmlFor={summaryId} className="sr-only">
        {t("memory.edit.summaryLabel")}
      </label>
      <Input
        id={summaryId}
        value={summary}
        autoFocus={autoFocus}
        className="bg-sheet hover:bg-sheet"
        onChange={(e) => setSummary(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.nativeEvent.isComposing) {
            e.preventDefault();
            void save();
          }
        }}
      />
      <label htmlFor={detailId} className="sr-only">
        {t("memory.edit.detailLabel")}
      </label>
      <Textarea
        id={detailId}
        value={detail}
        rows={3}
        placeholder={t("memory.edit.detailPlaceholder")}
        className="resize-y bg-sheet hover:bg-sheet"
        onChange={(e) => setDetail(e.target.value)}
      />
      {failure ? (
        <p role="alert" className="text-xs text-destructive-text">
          {failure.reason
            ? t("memory.edit.saveFailedWithReason", { reason: failure.reason })
            : t("memory.edit.saveFailed")}
        </p>
      ) : null}
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1 truncate text-2xs text-muted-foreground">
          {meta}
        </div>
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          {t("memory.edit.cancel")}
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={!canSave}
          onClick={() => void save()}
        >
          {saving ? (
            <>
              <Loader2
                className="size-3.5 animate-spin motion-reduce:animate-none"
                aria-hidden="true"
              />
              {t("memory.edit.saving")}
            </>
          ) : (
            t("memory.edit.save")
          )}
        </Button>
      </div>
    </div>
  );
}
