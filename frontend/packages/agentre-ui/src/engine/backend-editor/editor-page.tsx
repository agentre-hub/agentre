// 编辑器整页档（手机）：同一份字段换一副外壳——「✕ · 标题 · 保存」的页头、按
// 基本 / 运行方式 / 高级 分组的正文、钉在底部安全区之上的测试连接与结果条。
// 字段、校验与保存端口全在 BackendEditor 里，这里只吃已经装配好的节点。
import * as React from "react";
import { X } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Button } from "../../ui/button";

export type BackendEditorGroup = {
  id: "basics" | "runtime" | "advanced";
  // 组内字段按装配顺序排；node 为 null 的不算数，整组都为 null 时这一组不画。
  // key 是字段自己的名字：类型切换让某一格变成 null 时，其余各格不会因为位置变了而重挂。
  fields: { key: string; node: React.ReactNode }[];
};

export function BackendEditorPage({
  title,
  onClose,
  onSubmit,
  busy,
  saveDisabled,
  saving,
  groups,
  summary,
  footer,
}: {
  title: string;
  onClose: () => void;
  onSubmit: React.FormEventHandler<HTMLFormElement>;
  // 写请求在飞时不许离开：与弹窗档脚部「取消」的禁用口径一致。
  busy: boolean;
  saveDisabled: boolean;
  saving: boolean;
  groups: BackendEditorGroup[];
  summary: React.ReactNode;
  footer: React.ReactNode;
}) {
  const { t } = useTranslation();
  const titleId = React.useId();
  return (
    <form
      aria-labelledby={titleId}
      onSubmit={onSubmit}
      className="flex min-h-0 flex-1 flex-col bg-background"
    >
      <header className="flex shrink-0 items-center gap-1 bg-sheet px-1 py-1">
        <button
          type="button"
          aria-label={t("common.close")}
          disabled={busy}
          onClick={onClose}
          className="inline-flex size-11 shrink-0 items-center justify-center rounded-md text-foreground transition-colors hover:bg-accent focus-visible:focus-outline disabled:pointer-events-none disabled:opacity-40"
        >
          <X className="size-5" aria-hidden="true" />
        </button>
        <h1
          id={titleId}
          className="min-w-0 flex-1 truncate text-center text-base font-semibold tracking-tight text-foreground"
        >
          {title}
        </h1>
        <Button
          type="submit"
          variant="ghost"
          disabled={saveDisabled}
          className="h-11 shrink-0 px-3 text-base font-semibold text-primary-text"
        >
          {saving ? t("common.saving") : t("common.save")}
        </Button>
      </header>

      <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-y-auto px-4 py-4 [&_[data-slot=input]]:h-11! [&_[data-slot=select-trigger]]:h-11!">
        {groups.map((group) => (
          <EditorGroup key={group.id} group={group} />
        ))}
        {summary}
      </div>

      <div className="flex shrink-0 flex-col gap-2 bg-sheet px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))]">
        {footer}
      </div>
    </form>
  );
}

function EditorGroup({ group }: { group: BackendEditorGroup }) {
  const { t } = useTranslation();
  const headingId = React.useId();
  const fields = group.fields.filter((field) => field.node != null);
  if (fields.length === 0) return null;
  const heading =
    group.id === "basics"
      ? t("agentBackends.editor.groups.basics")
      : group.id === "runtime"
        ? t("agentBackends.editor.groups.runtime")
        : t("agentBackends.editor.groups.advanced");
  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-2">
      <h2
        id={headingId}
        className="px-1 text-xs font-medium text-muted-foreground"
      >
        {heading}
      </h2>
      <div className="flex flex-col gap-4 rounded-xl bg-card p-4">
        {fields.map((field) => (
          <React.Fragment key={field.key}>{field.node}</React.Fragment>
        ))}
      </div>
    </section>
  );
}
