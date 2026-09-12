import { useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { copyTextToClipboard } from "../lib/clipboard-toast";
import { Button } from "../ui/button";

export type CommandCardProps = {
  /** 这条命令要在哪里执行，例如「远端终端」。同时是这张卡的无障碍名称。 */
  label: string;
  command: string;
  /** 宿主用例按 testid 抓命令与复制按钮时给。 */
  testId?: string;
  copyTestId?: string;
};

/**
 * 一条可复制的命令。
 *
 * 反馈走**按钮内联**的「已复制」而不是 toast：引导常被部署在
 * `http://<局域网 IP>:port` 上，那里 `navigator.clipboard` 整个对象都不存在
 * （规范标了 [SecureContext]），`copyTextToClipboard` 会退回 `execCommand`；
 * 连兜底也失败时按钮必须保持原样，让人知道要手抄。toast 说的是「动作发生了」，
 * 表达不了这个区别，还要求宿主装着同一个 toast 实例。
 */
export function CommandCard({
  label,
  command,
  testId,
  copyTestId,
}: CommandCardProps) {
  const { t } = useUiTranslation();
  // 记的是「复制走的是哪一条命令」而不是「复制过没有」：切换系统会把同一张卡换成
  // 另一条命令，只记布尔值的话，按钮就会对着一条从没进过剪贴板的命令说「已复制」。
  const [copiedCommand, setCopiedCommand] = useState<string | null>(null);
  const copied = copiedCommand === command;

  // 计时从点击那一刻起算，与卡片此刻显示哪条命令无关。
  useEffect(() => {
    if (copiedCommand === null) return;
    const timer = window.setTimeout(() => setCopiedCommand(null), 2000);
    return () => window.clearTimeout(timer);
  }, [copiedCommand]);

  return (
    <div
      role="group"
      aria-label={label}
      className="overflow-hidden rounded-lg border border-border"
    >
      <div className="flex min-h-8 items-center gap-2 border-b border-border bg-muted px-3 py-1">
        <span className="min-w-0 flex-1 truncate text-2xs text-muted-foreground">
          {label}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          aria-label={t("onboarding.copyLabel", { label })}
          data-testid={copyTestId}
          onClick={() => {
            copyTextToClipboard(command)
              .then((ok) => {
                // 没复制成就保持原样：命令仍可选中手抄，不谎报「已复制」。
                if (ok) setCopiedCommand(command);
              })
              .catch(() => {
                // 同上——Clipboard API 存在却拒绝（文档失焦、权限被拒）时也一样。
              });
          }}
        >
          {copied ? (
            <Check data-icon="inline-start" aria-hidden="true" />
          ) : (
            <Copy data-icon="inline-start" aria-hidden="true" />
          )}
          {copied ? t("onboarding.copied") : t("onboarding.copy")}
        </Button>
      </div>
      <code
        data-selectable-text="true"
        data-testid={testId}
        className="block select-text overflow-x-auto whitespace-pre-wrap break-words bg-code-surface px-3 py-2.5 font-mono text-xs leading-relaxed text-code-foreground"
      >
        {command}
      </code>
    </div>
  );
}
