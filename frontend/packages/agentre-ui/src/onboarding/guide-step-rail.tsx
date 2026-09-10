import { Check, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";

export type GuideStep = {
  key: string;
  title: string;
  hint: string;
  /** 省略即这一步没有「已完成」的说法——终点步骤就是这种。 */
  doneLabel?: string;
};

export type GuideStepRailProps = {
  steps: readonly GuideStep[];
  /** 1 起算。 */
  current: number;
  done: readonly number[];
  onSelect: (step: number) => void;
  /** 有在途操作时锁住整条，免得正文被提前卸载吞掉失败原因。 */
  disabled?: boolean;
  /** 省略即不可收起——宿主没有可回退的地方（列表为空）时就是这种。 */
  onDismiss?: () => void;
};

/**
 * 引导顶上那条步骤条。标题与副标题由宿主给：桌面端走「安装 / 常驻 / 配对」，
 * 控制台走「安装并登录 / 输码 / 常驻」，两端的顺序各自被 daemon 的落盘时序钉死，
 * 不是这里能统一的东西。这里统一的是**语义**：三格可点、当前步骤可识别、
 * 完成态换副标题。
 */
export function GuideStepRail({
  steps,
  current,
  done,
  onSelect,
  disabled = false,
  onDismiss,
}: GuideStepRailProps) {
  const { t } = useUiTranslation();

  return (
    <div className="flex items-stretch border-b border-border bg-muted/50">
      <ol className="grid min-w-0 flex-1 grid-cols-1 sm:grid-cols-3">
        {steps.map((step, index) => {
          const number = index + 1;
          const finished = done.includes(number);
          const active = current === number;

          return (
            <li
              key={step.key}
              className="flex min-w-0 border-b border-border last:border-b-0 sm:border-b-0 sm:border-r sm:last:border-r-0"
            >
              <Button
                type="button"
                variant="ghost"
                aria-current={active ? "step" : undefined}
                // 序号只画在 aria-hidden 的徽标里,读屏拿不到;不自带序号的话,这一格
                // 听起来就是一个带副标题尾巴的普通按钮。
                aria-label={t("onboarding.stepNav", {
                  number,
                  title: step.title,
                })}
                className={cn(
                  "h-auto min-w-0 flex-1 items-start justify-start gap-3 rounded-none p-4 text-left font-normal",
                  active && "bg-primary-soft",
                )}
                disabled={disabled}
                onClick={() => onSelect(number)}
              >
                <span
                  aria-hidden="true"
                  className={cn(
                    "flex size-6 shrink-0 items-center justify-center rounded-full bg-muted font-mono text-2xs font-semibold text-muted-foreground",
                    finished && "bg-status-running-bg text-status-running",
                    active && "bg-primary text-primary-foreground",
                  )}
                >
                  {finished ? (
                    <Check className="size-3" />
                  ) : (
                    String(number).padStart(2, "0")
                  )}
                </span>
                <span className="min-w-0">
                  <span className="block truncate text-xs font-semibold">
                    {step.title}
                  </span>
                  <span className="mt-0.5 block truncate text-2xs text-muted-foreground">
                    {finished && step.doneLabel ? step.doneLabel : step.hint}
                  </span>
                </span>
              </Button>
            </li>
          );
        })}
      </ol>
      {onDismiss ? (
        <div className="flex shrink-0 items-center border-l border-border px-2">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={t("onboarding.dismiss")}
            disabled={disabled}
            onClick={onDismiss}
          >
            <X aria-hidden="true" />
          </Button>
        </div>
      ) : null}
    </div>
  );
}
