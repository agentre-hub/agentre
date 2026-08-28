import { Monitor, Moon, Sun } from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";

import type { AppThemePreference } from "./theme-context";
import { nextThemePreference, useTheme } from "./theme-context";

/**
 * 三态主题切换按钮：点一下按 `system → light → dark` 走一格。
 *
 * 两个宿主原本各画一份，图标与顺序逐字相同，差的只有外壳类名（桌面端挂在左侧
 * 导航栏、要带 `wails-no-drag`；浏览器宿主挂在顶栏、尺寸另有规矩）。外壳类名是
 * 宿主的事，经 `className` 传进来；按钮说什么、点一下变成什么在这里。
 *
 * 无障碍文案取桌面端那份（当前档 + 下一档都念出来）：浏览器那份只念当前档，
 * 用键盘的人因此不知道按下去会变成什么。
 */

const THEME_PREFERENCE_META: Record<
  AppThemePreference,
  { icon: LucideIcon; labelKey: string }
> = {
  system: { icon: Monitor, labelKey: "theme.system" },
  light: { icon: Sun, labelKey: "theme.lightMode" },
  dark: { icon: Moon, labelKey: "theme.darkMode" },
};

export type ThemeToggleProps = {
  className?: string;
};

export function ThemeToggle({ className }: ThemeToggleProps) {
  const { t } = useUiTranslation();
  const { effectiveTheme, setThemePreference, themePreference } = useTheme();

  const meta = THEME_PREFERENCE_META[themePreference];
  const Icon = meta.icon;
  const next = nextThemePreference(themePreference);
  const nextMeta = THEME_PREFERENCE_META[next];
  const currentDescription =
    themePreference === "system"
      ? t("theme.systemWithResolved", {
          resolved:
            effectiveTheme === "dark" ? t("theme.dark") : t("theme.light"),
        })
      : t(meta.labelKey);
  const nextDescription = t(nextMeta.labelKey);

  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className={cn("group relative overflow-visible", className)}
      aria-label={t("theme.toggle", {
        current: currentDescription,
        next: nextDescription,
      })}
      title={t("theme.toggleTitle", {
        current: currentDescription,
        next: nextDescription,
      })}
      onClick={() => setThemePreference(next)}
    >
      <Icon data-icon="only" aria-hidden="true" />
    </Button>
  );
}
