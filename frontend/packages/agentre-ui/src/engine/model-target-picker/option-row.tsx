// 弹层列表的一行：sticky 供应商组头、fixed-model 小节头、选项按钮本体，
// 以及挂在该供应商第一条待修复行上的同步入口。
//
// 全部视觉分歧（动态跟随 vs 固定模型、选中 vs hover vs 键盘活动、停用行的
// 「名字 / 原因」两行）都在这里定案；调用方只把已算好的布尔与文案传进来。
import {
  AlertTriangle,
  Check,
  RefreshCw,
  Upload,
  type LucideIcon,
} from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { cn } from "../../lib/utils";

import { LlmProviderLogo } from "../ai-brand-logo";
import { formatTokens } from "../llm-provider-models";
import type { Option } from "./options";
import type { ModelTarget, PickerProvider, PickerScenario } from "./types";

export function PickerOptionRow({
  opt,
  index,
  active,
  selectedNow,
  showGroup,
  showFixedSection,
  groupOnDevice,
  inlineHint,
  scenario,
  SpecialIcon,
  syncProvider,
  onSyncProvider,
  onActivate,
  onSelect,
}: {
  opt: Option;
  index: number;
  active: boolean;
  selectedNow: boolean;
  showGroup: boolean;
  showFixedSection: boolean;
  groupOnDevice: boolean;
  // inlineHint 行内原因，有值时取代副行。
  inlineHint?: string;
  scenario: PickerScenario;
  SpecialIcon: LucideIcon;
  // syncProvider 仅在本行是该供应商的同步落点时有值。
  syncProvider?: PickerProvider;
  onSyncProvider?: (provider: PickerProvider) => void;
  onActivate: (index: number) => void;
  onSelect: (target: ModelTarget) => void;
}) {
  const { t } = useTranslation();
  return (
    <li role="none">
      {showGroup ? (
        <div
          data-testid="picker-group"
          data-provider-key={opt.target.providerKey}
          // 底色必须是 popover：bg-background 在暗色下比弹层更黑，
          // 会在列表里拉出一条黑带（mockup .pgrp { background: var(--popover) }）。
          className="sticky top-0 z-10 flex items-center gap-1.5 bg-popover px-2 pb-1 pt-[9px] text-3xs font-semibold uppercase tracking-[0.06em] text-muted-foreground"
        >
          {opt.groupType ? (
            <LlmProviderLogo
              providerType={opt.groupType}
              providerName={opt.group}
              className="size-3.5"
            />
          ) : null}
          <span>{opt.group}</span>
          {groupOnDevice ? (
            <span className="ml-auto rounded-full bg-status-running-bg px-1.5 py-0.5 text-2xs font-medium normal-case tracking-normal text-status-running">
              {t("modelTargetPicker.providerOnDevice")}
            </span>
          ) : null}
        </div>
      ) : null}
      {/* mockup .fixhdr：6px 8px 2px / 10px / 不加粗。 */}
      {showFixedSection ? (
        <div className="px-2 pb-0.5 pt-1.5 text-3xs text-muted-foreground">
          {t("modelTargetPicker.fixedSection")}
        </div>
      ) : null}
      <div className="flex items-center gap-1">
        <button
          type="button"
          role="option"
          aria-selected={selectedNow}
          aria-disabled={opt.disabled}
          data-option-index={index}
          data-kind={opt.kind}
          disabled={opt.disabled}
          title={
            opt.kind === "invalid"
              ? t("modelTargetPicker.invalidHint")
              : opt.disabledHint
          }
          onMouseEnter={() => onActivate(index)}
          onClick={() => {
            if (opt.disabled) return;
            onSelect(opt.target);
          }}
          className={cn(
            // mockup .opt：7px 圆角 / 6px 8px padding / 手形光标。
            "flex min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-[7px] px-2 py-1.5 text-left text-xs transition-[background-color,filter]",
            // 焦点环只表达「键盘焦点在这」，不兼任常态 hover。
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
            // 核心语义分歧：跟随默认（动态）用 primary-soft 强调底，
            // 固定到具体模型保持中性底，两态一眼可分。
            opt.kind === "provider-default"
              ? cn(
                  // mockup .opt.dyn:hover 只改 filter：底色不换、不画边，
                  // 靠提亮/压暗表达 hover 与键盘活动态。
                  "mb-[3px] bg-primary-soft",
                  !opt.disabled &&
                    "hover:brightness-95 dark:hover:brightness-125",
                  !opt.disabled &&
                    active &&
                    "brightness-95 dark:brightness-125",
                )
              : opt.kind === "invalid"
                ? "border border-dashed border-status-waiting"
                : // mockup .opt:hover 是满色 accent；.opt.dis:hover 保持透明；
                  // .opt.sel 排在 .opt:hover 之后 → 选中项 hover 不翻 accent。
                  !opt.disabled &&
                  !selectedNow &&
                  (active ? "bg-accent" : "hover:bg-accent"),
            // 顶部特殊项是独立描边卡片（mockup .opt.special）：比普通行
            // 重一档，选中时描边转实线 primary。
            opt.kind === "special" &&
              cn(
                "mb-1 border border-dashed p-2",
                selectedNow
                  ? "border-solid border-primary"
                  : "border-border-strong",
              ),
            // 选中项带底色且盖过 hover（mockup .opt.sel 排在 .opt:hover 之后）。
            //
            // ⚠️ 与 mockup 字面的有意偏离：mockup 给 .opt.sel 用的是
            // primary-soft，但这里改成中性的 accent。primary-soft 在这颗
            // 弹层里是「动态跟随供应商默认」的语义编码（.opt.dyn 常态就是
            // 蓝底），选中态不能把这层含义借走 —— 否则「选中的固定模型」和
            // 「跟随默认」长成一样，只剩 ✓ 能区分，正好抹掉整套设计要表达的
            // 核心分歧。跟随默认行因此**不**套 accent：它选中与否都是蓝底，
            // 由 ✓ 表示选中。别为了「对齐 mockup」把这里改回 primary-soft。
            selectedNow &&
              (opt.kind === "provider-default"
                ? "bg-primary-soft"
                : "bg-accent"),
            "disabled:cursor-not-allowed disabled:opacity-50",
          )}
        >
          <span className="flex min-w-0 flex-1 items-center gap-2">
            {opt.kind === "special" ? (
              <SpecialIcon
                data-testid="special-icon"
                data-scenario={scenario}
                className="size-3.5 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
            ) : opt.kind === "invalid" ? (
              <AlertTriangle
                className="size-3.5 shrink-0 text-status-waiting"
                aria-hidden="true"
              />
            ) : opt.kind === "provider-default" ? (
              <RefreshCw
                data-testid="provider-default-icon"
                className="size-3.5 shrink-0 text-primary-text"
                aria-hidden="true"
              />
            ) : null}
            <span className="flex min-w-0 flex-1 flex-col">
              <span
                className={cn(
                  // mockup .opt .o1 一律 500 字重。
                  "truncate font-medium",
                  opt.kind === "provider-default"
                    ? "text-primary-text"
                    : "text-foreground",
                )}
              >
                {opt.label}
              </span>
              {/* 停用行是「名字 / 原因」两行（mockup .opt.dis）：原因
                  取代副行，不在模型标识之下再叠第三行。 */}
              {inlineHint ? (
                <span className="truncate text-2xs text-status-waiting">
                  {inlineHint}
                </span>
              ) : opt.sublabel ? (
                <span
                  className={cn(
                    "text-2xs text-muted-foreground",
                    // 等宽只给「副行整条就是一个标识符」的 fixed 行；
                    // 跟随默认 / 特殊项的副行是人读文案，标识符由副行
                    // 自己包 mono。跟随默认行宁可折行也不许截断。
                    opt.kind === "provider-default"
                      ? "break-all"
                      : opt.kind === "fixed"
                        ? "truncate font-mono"
                        : "truncate",
                  )}
                >
                  {opt.sublabel}
                </span>
              ) : null}
            </span>
            {opt.kind === "invalid" ? (
              <span className="shrink-0 rounded-full bg-status-waiting-bg px-1.5 py-0.5 text-2xs font-medium text-status-waiting">
                {t("modelTargetPicker.invalidChip")}
              </span>
            ) : null}
            {opt.kind === "fixed" && (opt.contextWindow || opt.maxOutput) ? (
              <span className="shrink-0 font-mono text-3xs text-muted-foreground">
                {t("modelTargetPicker.contextOutput", {
                  ctx: formatTokens(opt.contextWindow ?? 0),
                  out: formatTokens(opt.maxOutput ?? 0),
                })}
              </span>
            ) : null}
          </span>
          {selectedNow ? (
            <Check
              className="size-3.5 shrink-0 text-primary-text"
              aria-hidden="true"
            />
          ) : null}
        </button>
        {/* 同步入口就放在它会修复的那一行内（消费方负责确认与凭证复制）。 */}
        {syncProvider && onSyncProvider ? (
          <button
            type="button"
            className="inline-flex shrink-0 cursor-pointer items-center gap-1 rounded-md border border-border px-1.5 py-1 text-2xs text-primary-text transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            aria-label={t("modelTargetPicker.syncProviderNamed", {
              provider: syncProvider.name,
            })}
            onClick={() => onSyncProvider(syncProvider)}
          >
            <Upload className="size-3" aria-hidden="true" />
            {t("modelTargetPicker.syncInline")}
          </button>
        ) : null}
      </div>
    </li>
  );
}
