// 「外观」分区:主题偏好(跟随系统 / 亮 / 暗)与它的下拉。

import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  Badge,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
} from "@agentre-hub/agentre-ui";
import { LANGUAGE_STORAGE_KEY, type SupportedLanguage } from "@/i18n";
import { useTabSettingsStore } from "@/stores/tab-settings-store";

import type { AppTheme, AppThemePreference } from "../chrome";
import { toastFailure } from "../failure-toast";

import { SettingsPageHeader } from "./header";

export type AppearanceSettingsProps = {
  effectiveTheme: AppTheme;
  onThemePreferenceChange: (themePreference: AppThemePreference) => void;
  themePreference: AppThemePreference;
};

export const themePreferenceOptions = [
  {
    labelKey: "theme.system",
    value: "system",
  },
  {
    labelKey: "theme.light",
    value: "light",
  },
  {
    labelKey: "theme.dark",
    value: "dark",
  },
] satisfies {
  labelKey: string;
  value: AppThemePreference;
}[];

export type ThemePreferenceSelectProps = Omit<
  AppearanceSettingsProps,
  "effectiveTheme"
>;

export function ThemePreferenceSelect({
  onThemePreferenceChange,
  themePreference,
}: ThemePreferenceSelectProps) {
  const { t } = useTranslation();
  const labelId = React.useId();

  return (
    <div className="flex flex-col gap-2 py-3">
      <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-col">
          <span id={labelId} className="text-sm font-medium">
            {t("settings.appearance.themeMode.label")}
          </span>
        </div>
        <div className="w-full sm:w-[220px]">
          <Select
            value={themePreference}
            onValueChange={(value) =>
              onThemePreferenceChange(value as AppThemePreference)
            }
          >
            <SelectTrigger
              aria-label={t("settings.appearance.themeMode.label")}
              aria-labelledby={labelId}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {themePreferenceOptions.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {t(option.labelKey)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  );
}

export function AppearanceSettings({
  effectiveTheme,
  onThemePreferenceChange,
  themePreference,
}: AppearanceSettingsProps) {
  const { i18n, t } = useTranslation();
  const followsSystem = themePreference === "system";
  const language =
    i18n.resolvedLanguage === "zh-CN" || i18n.resolvedLanguage === "en"
      ? i18n.resolvedLanguage
      : "en";

  function handleLanguageChange(next: string) {
    const supportedLanguage = next as SupportedLanguage;
    void i18n.changeLanguage(supportedLanguage);

    try {
      localStorage.setItem(LANGUAGE_STORAGE_KEY, supportedLanguage);
    } catch {
      // Embedded previews may block localStorage.
    }
  }

  return (
    <>
      <SettingsPageHeader
        title={t("settings.appearance.title")}
        description={t("settings.appearance.description")}
      />
      {/* 无边框：分组不再是带框的卡片，标题与各行直接落在内容页上、靠留白分开；
          下拉框靠自己的字段填充立起来。 */}
      <section data-slot="appearance-group" className="flex flex-col">
        <div className="flex flex-wrap items-center gap-3 pb-1">
          <div className="flex min-w-0 flex-1 flex-col">
            <h2 className="text-sm font-semibold">
              {t("settings.appearance.colorMode.title")}
            </h2>
          </div>
          <Badge
            variant="secondary"
            className="rounded-sm px-1.5 py-0 font-mono text-2xs font-medium"
          >
            {followsSystem
              ? t("theme.system")
              : effectiveTheme === "dark"
                ? t("theme.dark")
                : t("theme.light")}
          </Badge>
        </div>
        <ThemePreferenceSelect
          onThemePreferenceChange={onThemePreferenceChange}
          themePreference={themePreference}
        />
        <div className="flex flex-col gap-2 py-3">
          <div className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex min-w-0 flex-col">
              <span className="text-sm font-medium">{t("language.label")}</span>
            </div>
            <div className="w-full sm:w-[220px]">
              <Select value={language} onValueChange={handleLanguageChange}>
                <SelectTrigger aria-label={t("language.label")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="zh-CN">{t("language.zh-CN")}</SelectItem>
                  <SelectItem value="en">{t("language.en")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        </div>
      </section>
      <TabsGroup />
    </>
  );
}

// 「标签页」一组：新建对话是否靠拢同项目（spec 2026-11-17-group-new-tabs-by-project）。
// 写库成功才翻开关；没存上就留在原值并说清楚（与记忆设置页同一做法）。
function TabsGroup() {
  const { t } = useTranslation();
  const groupNewByProject = useTabSettingsStore(
    (s) => s.settings.groupNewByProject,
  );
  const load = useTabSettingsStore((s) => s.load);
  const save = useTabSettingsStore((s) => s.save);
  const labelId = React.useId();
  const descId = React.useId();

  React.useEffect(() => {
    void load();
  }, [load]);

  const handleChange = async (next: boolean) => {
    try {
      await save({ groupNewByProject: next });
    } catch (err) {
      toastFailure(t("common.saveFailed"), err);
    }
  };

  return (
    <section data-slot="appearance-group" className="flex flex-col pt-3">
      <h2 className="pb-1 text-sm font-semibold">
        {t("settings.appearance.tabs.title")}
      </h2>
      <div className="flex items-center gap-4 py-3">
        <div className="flex min-w-0 flex-1 flex-col">
          <span id={labelId} className="text-sm font-medium">
            {t("settings.appearance.tabs.groupNewByProject.label")}
          </span>
          <span
            id={descId}
            className="text-2xs leading-relaxed text-muted-foreground"
          >
            {t("settings.appearance.tabs.groupNewByProject.desc")}
          </span>
        </div>
        <Switch
          aria-labelledby={labelId}
          aria-describedby={descId}
          checked={groupNewByProject}
          onCheckedChange={(v) => void handleChange(v)}
        />
      </div>
    </section>
  );
}
