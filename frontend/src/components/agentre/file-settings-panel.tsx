import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@agentre-hub/agentre-ui";

import {
  useFileSettingsStore,
  type FileOpenAction,
} from "../../stores/file-settings-store";

const openActionOptions: { value: FileOpenAction; labelKey: string }[] = [
  { value: "preview", labelKey: "settings.files.openAction.preview" },
  { value: "external", labelKey: "settings.files.openAction.external" },
];

/**
 * FileSettingsPanel 是「常规 → 文件」这一页。目前只有一个设置：单击一个文件时
 * 是开内置预览还是交给外部应用。分区卡头部写的是这个设置**管不到**的两种情形
 * （预览不了的文件、远端会话），它们不是可选项而是适用边界（spec 决策 9）。
 */
export function FileSettingsPanel() {
  const { t } = useTranslation();
  const labelId = React.useId();
  const openAction = useFileSettingsStore((s) => s.settings.openAction);
  const save = useFileSettingsStore((s) => s.save);
  const load = useFileSettingsStore((s) => s.load);

  React.useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <section className="overflow-hidden rounded-lg border border-border bg-card">
        <header className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold">
            {t("settings.files.openTitle")}
          </h2>
          <p className="text-2xs leading-relaxed text-muted-foreground">
            {t("settings.files.openDesc")}
          </p>
        </header>
        <Row
          labelId={labelId}
          label={t("settings.files.openAction.label")}
          desc={t("settings.files.openAction.desc")}
        >
          <Select
            value={openAction}
            onValueChange={(value) =>
              void save({ openAction: value as FileOpenAction })
            }
          >
            <SelectTrigger
              aria-label={t("settings.files.openAction.label")}
              aria-labelledby={labelId}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {openActionOptions.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {t(option.labelKey)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Row>
      </section>
    </div>
  );
}

function Row({
  labelId,
  label,
  desc,
  children,
}: {
  labelId: string;
  label: string;
  desc: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-4 border-t border-border px-4 py-3 first:border-t-0">
      <div className="min-w-0 flex-1">
        <div id={labelId} className="text-xs font-medium">
          {label}
        </div>
        <div className="text-2xs leading-relaxed text-muted-foreground">
          {desc}
        </div>
      </div>
      <div className="w-[220px] shrink-0">{children}</div>
    </div>
  );
}
