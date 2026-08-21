import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

import type { ChatFilesMode } from "@/stores/chat-sidebar-store";

type Props = {
  active: ChatFilesMode;
  onChange: (mode: ChatFilesMode) => void;
  /** 「变动」段的角标恒显示 deriveFiles() 的文件数（零成本）。 */
  changesCount: number;
};

/**
 * FilesModeSwitcher 是「文件」页内的两档胶囊（变动 / 目录）。Git 已提升为顶层
 * tab（设计决策 1），这里只剩同一批文件的两种视角。
 *
 * 胶囊组与 Git 页上下文条的「未提交 / 本分支」两档同形（圆角胶囊、自然宽度，
 * 而不是等宽 flex-1），whitespace-nowrap，侧栏被拖窄到 190px 时不换行。
 */
export function FilesModeSwitcher({ active, onChange, changesCount }: Props) {
  const { t } = useTranslation();
  return (
    <div
      role="tablist"
      aria-label={t("chatContext.files.modes.label")}
      className="inline-flex shrink-0 overflow-hidden rounded-full border border-border"
    >
      <ModeTab
        label={t("chatContext.files.modes.changes")}
        count={changesCount}
        active={active === "changes"}
        onClick={() => onChange("changes")}
      />
      <ModeTab
        label={t("chatContext.files.modes.directory")}
        count={null}
        active={active === "directory"}
        onClick={() => onChange("directory")}
      />
    </div>
  );
}

function ModeTab({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count: number | null;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1 px-2 py-0.5 text-3xs whitespace-nowrap transition-colors",
        "focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
        active
          ? "bg-accent font-semibold text-foreground"
          : "text-muted-foreground hover:bg-muted/50",
      )}
    >
      <span>{label}</span>
      {count != null ? (
        <span className="font-mono text-3xs opacity-75">{count}</span>
      ) : null}
    </button>
  );
}
