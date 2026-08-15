import { Copy } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import { copyTextWithToast } from "@agentre-ai/agentre-ui";

type CommandCardProps = {
  command: string;
  label: string;
};

export function CommandCard({ command, label }: CommandCardProps) {
  const { t } = useTranslation();

  return (
    <div
      role="group"
      aria-label={label}
      className="overflow-hidden rounded-lg border border-border bg-code-surface"
    >
      <div className="flex min-h-8 items-center justify-between gap-3 border-b border-border px-3 py-1 text-2xs text-code-muted-foreground">
        <span>{label}</span>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          aria-label={t("remoteDevices.onboarding.copyLabel", { label })}
          onClick={() => {
            void copyTextWithToast(command, {
              successTitle: t("remoteDevices.onboarding.copySuccess"),
            });
          }}
        >
          <Copy data-icon="inline-start" aria-hidden="true" />
          {t("common.copy")}
        </Button>
      </div>
      <code
        data-selectable-text="true"
        className="block select-text whitespace-pre-wrap break-words px-3 py-3 font-mono text-xs leading-relaxed text-code-foreground"
      >
        {command}
      </code>
    </div>
  );
}
