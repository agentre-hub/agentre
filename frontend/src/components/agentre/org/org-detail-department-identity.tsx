import { useTranslation } from "react-i18next";

import { Input } from "@agentre-hub/agentre-ui";

import { IconPicker } from "../icon-picker";
import type { AgentColor } from "../types";

import { OrgColorSwatches } from "./org-color-swatches";

type IdentityPatch = {
  name?: string;
  description?: string;
  icon?: string;
  accentColor?: AgentColor;
};

/** 三栏里的「身份」栏：名字 / 描述 / 图标 / 主题色。 */
export function OrgDetailDepartmentIdentity({
  name,
  description,
  icon,
  accentColor,
  patch,
  flush,
}: {
  name: string;
  description: string;
  icon: string;
  accentColor: AgentColor;
  patch: (partial: IdentityPatch, opts?: { immediate?: boolean }) => void;
  flush: () => void;
}) {
  const { t } = useTranslation();
  return (
    <section
      aria-label={t("org.detail.columns.identity")}
      data-slot="org-detail-col-identity"
      className="min-w-0 space-y-4"
    >
      <section className="space-y-4" data-slot="dept-section-basic">
        <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
          {t("org.department.basicInfo")}
        </h3>
        <div className="space-y-1.5">
          <label className="block text-2xs text-muted-foreground">
            {t("org.department.name")}
          </label>
          <Input
            value={name}
            onChange={(e) => patch({ name: e.target.value })}
            onBlur={flush}
            aria-label={t("org.department.name")}
          />
        </div>
        <div className="space-y-1.5">
          <div className="flex items-center justify-between gap-2">
            <label className="text-2xs text-muted-foreground">
              {t("org.department.description")}
            </label>
            <span className="font-mono text-2xs text-muted-foreground">
              {t("common.optional")}
            </span>
          </div>
          <Input
            value={description}
            onChange={(e) => patch({ description: e.target.value })}
            onBlur={flush}
            aria-label={t("org.department.description")}
          />
        </div>
        {/* 图标与主题色不再并排：三栏时栏宽实测 276px，150px 的图标块 + 5 列
            色板要 152px，并排放不下（色板被顶出栏宽，外层滚动容器把它藏起来
            而不报错）。竖着摞，两块各自吃满栏宽。 */}
        <div
          className="flex min-w-0 flex-col gap-2.5"
          data-slot="dept-icon-theme-row"
        >
          <div className="flex min-w-0 flex-col gap-1.5">
            <label className="block text-2xs text-muted-foreground">
              {t("org.department.icon")}
            </label>
            <IconPicker
              value={icon}
              onChange={(v) => patch({ icon: v }, { immediate: true })}
              accentColor={accentColor}
              ariaLabel={t("org.department.icon")}
              className="h-[38px] px-2.5 py-1.5"
            />
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <label className="block text-2xs text-muted-foreground">
              {t("org.department.themeColor")}
            </label>
            <OrgColorSwatches
              value={accentColor}
              groupLabel={t("org.department.themeColor")}
              optionLabel={(c) =>
                t("org.department.themeColorNamed", { color: c })
              }
              onPick={(c) => patch({ accentColor: c }, { immediate: true })}
              swatchClassName="size-6"
              selectedClassName="size-7 ring-2 ring-primary"
            />
          </div>
        </div>
      </section>
    </section>
  );
}
