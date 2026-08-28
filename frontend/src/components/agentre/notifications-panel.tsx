import * as React from "react";
import { useTranslation } from "react-i18next";

import { Switch } from "@agentre-hub/agentre-ui";

import {
  useNotificationSettingsStore,
  type NotificationSettings,
} from "../../stores/notification-settings-store";

export function NotificationsPanel() {
  const { t } = useTranslation();
  const settings = useNotificationSettingsStore((s) => s.settings);
  const save = useNotificationSettingsStore((s) => s.save);
  const load = useNotificationSettingsStore((s) => s.load);

  React.useEffect(() => {
    void load();
  }, [load]);

  const set = (patch: Partial<NotificationSettings>) => void save(patch);

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <section className="overflow-hidden rounded-lg border border-border bg-card">
        <header className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold">
            {t("settings.notifications.masterTitle")}
          </h2>
          <p className="text-2xs leading-relaxed text-muted-foreground">
            {t("settings.notifications.masterDesc")}
          </p>
        </header>
        <Row
          label={t("settings.notifications.enableLabel")}
          desc={t("settings.notifications.enableDesc")}
        >
          <Switch
            aria-label={t("settings.notifications.enableLabel")}
            checked={settings.enabled}
            onCheckedChange={(v) => set({ enabled: v })}
          />
        </Row>
        <Row
          label={t("settings.notifications.onlyWhenUnfocusedLabel")}
          desc={t("settings.notifications.onlyWhenUnfocusedDesc")}
        >
          <Switch
            aria-label={t("settings.notifications.onlyWhenUnfocusedLabel")}
            checked={settings.onlyWhenUnfocused}
            onCheckedChange={(v) => set({ onlyWhenUnfocused: v })}
          />
        </Row>
      </section>

      <section className="overflow-hidden rounded-lg border border-border bg-card">
        <header className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold">
            {t("settings.notifications.channelsTitle")}
          </h2>
          <p className="text-2xs leading-relaxed text-muted-foreground">
            {t("settings.notifications.channelsDesc")}
          </p>
        </header>

        <Row
          label={t("settings.notifications.systemLabel")}
          desc={t("settings.notifications.systemDesc")}
        >
          <Switch
            aria-label={t("settings.notifications.systemLabel")}
            checked={settings.system}
            onCheckedChange={(v) => set({ system: v })}
          />
        </Row>

        <Row
          label={t("settings.notifications.toastLabel")}
          desc={t("settings.notifications.toastDesc")}
        >
          <Switch
            aria-label={t("settings.notifications.toastLabel")}
            checked={settings.toast}
            onCheckedChange={(v) => set({ toast: v })}
          />
        </Row>
      </section>

      <div className="rounded-lg border border-primary-text/30 bg-primary-soft px-4 py-3 text-primary-text">
        <p className="text-xs font-semibold">
          {t("settings.notifications.ruleTitle")}
        </p>
        <p className="text-2xs leading-relaxed">
          {t("settings.notifications.ruleDesc")}
        </p>
      </div>
    </div>
  );
}

function Row({
  label,
  desc,
  children,
}: {
  label: string;
  desc: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-4 border-t border-border px-4 py-3 first:border-t-0">
      <div className="min-w-0 flex-1">
        <div className="text-xs font-medium">{label}</div>
        <div className="text-2xs leading-relaxed text-muted-foreground">
          {desc}
        </div>
      </div>
      <div className="flex items-center gap-2">{children}</div>
    </div>
  );
}
