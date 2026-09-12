// composer 底栏右侧那颗「思考力度」控件（spec 2026-09-01
// 「会话级思考力度的选择与生效」）。桌面端与 agentre-server 各渲染一次，所以它在
// 包里，且只吃平坦入参：会话行上的值、后端配置的值、一个回调。写库、IPC、失败回滚
// 都是宿主的事。
//
// 形态取自右侧既有的读数（决策 9）：与两个计量器同款的无填充、无描边、hover 才显
// 边框，只以一枚常驻 chevron 与手形指针把「可点」与那两个只读读数区分开——不是
// ProviderPill 那身有填充有描边的 pill 外壳，一颗描边填充的控件插进底栏右侧那片
// 安静区会把它打断。
import { Brain, ChevronDown } from "lucide-react";
import * as React from "react";

import { useUiTranslation } from "../i18n";
// 档位枚举只有一份：后端编辑器那张表与这颗控件说的是同一个 reasoning_effort。
// 纯类型引用，运行期不把引擎设置那棵模块树拖进 composer。
import type { ReasoningEffortValue } from "../engine/agent-backends-shared";
import { cn } from "../lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";

/**
 * 五个档位，从弱到强。序数是自明的，所以每行只有档位名，不配说明副行（决策 12）；
 * 强度由 heat-0…heat-4 色阶的一枚 8px 圆点承载（决策 10）。
 *
 * 四个支持该能力的后端呈现同一张表，不按后端裁剪。
 */
const EFFORT_LEVELS = ["low", "medium", "high", "xhigh", "max"] as const;

type ReasoningEffortLevel = (typeof EFFORT_LEVELS)[number];

// heat 色阶按档位序号取，写成静态表而不是拼类名：Tailwind 扫的是源码里的字面量，
// `bg-heat-${i}` 拼出来的类不会被生成。
const LEVEL_DOT_TONE: Record<ReasoningEffortLevel, string> = {
  low: "bg-heat-0",
  medium: "bg-heat-1",
  high: "bg-heat-2",
  xhigh: "bg-heat-3",
  max: "bg-heat-4",
};

export type ReasoningEffortPickerProps = {
  /**
   * 会话行上的值。空串 = 跟随后端配置。
   *
   * 它同时是 **no-op 判据**：选中的就是这个值时不回调。注意判据不是有效档位——
   * 会话行为空而后端配的是 high 时，用户显式选 high 是一次真实写入（把「跟随后端」
   * 钉成「就是 high」）。
   */
  value: ReasoningEffortValue;
  /** 后端配置的档位，会话行为空时由它兜底。空串 = 后端也没配。 */
  backendValue: ReasoningEffortValue;
  /** 只在值真的变了时触发；空串表示改回「默认（跟随后端配置）」。 */
  onChange: (value: ReasoningEffortValue) => void;
  /** 宿主写库失败时的原因，弹层底部说明之下追加一条错误行。 */
  errorText?: string;
  /** 宿主的测试抓手。包不预设任何一端的测试约定，要用就自己传。 */
  dataTestId?: string;
};

/**
 * 脸上写的是**有效档位**：会话行上的值非空时是它，为空时是后端配置的档位，
 * 两者都空时是「默认」。
 */
export function ReasoningEffortPicker({
  value,
  backendValue,
  onChange,
  errorText,
  dataTestId,
}: ReasoningEffortPickerProps) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const effective: ReasoningEffortValue = value || backendValue;
  const effectiveLabel = effective || t("reasoningEffortPicker.defaultLabel");

  const select = (next: ReasoningEffortValue) => {
    setOpen(false);
    // 选中的就是会话行上已有的那个值 → 不回调：否则每点一次已选中项就往转录里
    // 塞一条「已切换到 high」。
    if (next === value) return;
    onChange(next);
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          data-testid={dataTestId}
          aria-haspopup="listbox"
          aria-expanded={open}
          // 念出的是当前有效档位，不是一串枚举值。
          aria-label={t("reasoningEffortPicker.aria", {
            level: effectiveLabel,
          })}
          className={cn(
            // 与 ContextMeter 同一身读数外壳，只把 cursor-default 换成手形：
            // 那两个是只读读数，这颗可点。
            // 取的是**观感**不是收缩语义：底栏的溢出优先级里两个计量器是唯一带
            // min-w-0 的让位者（chat-composer.tsx 的两条硬约束），控件与提交键
            // 一律 shrink-0——所以这里不抄 ContextMeter 的 min-w-0/overflow-hidden。
            "flex shrink-0 cursor-pointer items-center gap-1.5 rounded-sm border border-transparent px-1 py-0.5 whitespace-nowrap",
            "font-mono text-meta transition-colors motion-reduce:transition-none",
            "hover:border-border hover:bg-accent",
            "focus-visible:border-border focus-visible:bg-accent focus-visible:outline-none",
          )}
        >
          <Brain
            className="size-3.5 shrink-0 text-primary-text"
            aria-hidden="true"
          />
          {/* 窄档退成纯图标（与 PermissionModePill 同一档）：控件自身不收缩，
              让位靠整条标签隐藏而不是把字截半——档位由图标、aria-label 与弹层承载。 */}
          <span className="font-medium text-primary-text @max-[620px]/composer:hidden">
            {effectiveLabel}
          </span>
          <ChevronDown
            data-testid="effort-chevron"
            className="size-3 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" side="top" className="w-80 p-0">
        <div className="flex items-center gap-1.5 border-b border-border px-3.5 py-2.5">
          <Brain
            className="size-3.5 shrink-0 text-foreground"
            aria-hidden="true"
          />
          <span className="text-xs font-semibold text-foreground">
            {t("reasoningEffortPicker.title")}
          </span>
        </div>
        <div role="listbox" aria-label={t("reasoningEffortPicker.title")}>
          {/* 「默认」是独立顶区而不是第六档（决策 11）：它表达的是「不设定」，
              与五个档位不同类。 */}
          <div className="border-b border-border p-1.5">
            <EffortRow
              effort=""
              current={effective === ""}
              onSelect={select}
              label={t("reasoningEffortPicker.defaultLabel")}
              sub={
                <span className="flex min-w-0 items-center gap-1">
                  <span aria-hidden="true">→</span>
                  {backendValue ? (
                    <span className="min-w-0 truncate">
                      {t("reasoningEffortPicker.followBackend")}
                      {" · "}
                      <span className="font-mono">{backendValue}</span>
                    </span>
                  ) : (
                    <span className="min-w-0 truncate">
                      {t("reasoningEffortPicker.followBackendUnset")}
                    </span>
                  )}
                </span>
              }
            />
          </div>
          <div className="flex flex-col gap-0.5 p-1.5">
            {EFFORT_LEVELS.map((level) => (
              <EffortRow
                key={level}
                effort={level}
                current={effective === level}
                onSelect={select}
                label={level}
                mono
              />
            ))}
          </div>
        </div>
        <div className="border-t border-border px-3.5 py-2 text-2xs text-muted-foreground">
          {t("reasoningEffortPicker.note")}
        </div>
        {errorText ? (
          <div
            role="alert"
            className="border-t border-border bg-destructive-soft px-3.5 py-2 text-2xs text-destructive-text"
          >
            {errorText}
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

/**
 * 一行档位。`aria-selected` 跟着**有效**档位走，与底色和「当前」徽章同源——
 * 屏幕阅读器念到的和看得见的是同一件事。会话值与有效值的分歧只影响 no-op 判据
 * （在 `select` 里），不在这里另画一套视觉。
 */
function EffortRow({
  effort,
  current,
  label,
  mono,
  sub,
  onSelect,
}: {
  effort: ReasoningEffortValue;
  current: boolean;
  label: string;
  mono?: boolean;
  sub?: React.ReactNode;
  onSelect: (value: ReasoningEffortValue) => void;
}) {
  const { t } = useUiTranslation();
  return (
    <button
      type="button"
      role="option"
      aria-selected={current}
      data-effort={effort}
      data-current={current ? "true" : "false"}
      onClick={() => onSelect(effort)}
      className={cn(
        "flex w-full cursor-pointer items-start gap-2.5 rounded-md px-2.5 py-2 text-left transition-colors",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
        current ? "bg-accent" : "hover:bg-accent",
      )}
    >
      {effort ? (
        <span
          data-testid="effort-dot"
          className={cn(
            // 暗色下 heat-0 与弹层底几乎没有对比，靠这圈 border-strong 描边让最低档
            // 读成一个空环而不是消失；色阶本身的值一个字节都不能改（它同时供
            // agentre-server 的活跃热力图使用）。浅色不需要，但描边照留（透明）
            // 免得两个主题下圆点大小差 2px。
            "mt-1 size-2 shrink-0 rounded-full border border-transparent dark:border-border-strong",
            LEVEL_DOT_TONE[effort],
          )}
          aria-hidden="true"
        />
      ) : (
        <span
          data-testid="effort-dot-empty"
          className="mt-1 size-2 shrink-0 rounded-full border border-border-strong"
          aria-hidden="true"
        />
      )}
      <span className="flex min-w-0 flex-1 flex-col gap-1">
        <span className="flex items-center gap-2">
          <span
            className={cn(
              "truncate text-xs font-medium text-foreground",
              mono && "font-mono",
            )}
          >
            {label}
          </span>
          {current ? (
            <span className="flex shrink-0 items-center gap-1 rounded-sm bg-primary-soft px-1.5 py-px text-2xs font-medium text-primary-text">
              <span
                className="size-1 shrink-0 rounded-full bg-primary"
                aria-hidden="true"
              />
              {t("reasoningEffortPicker.current")}
            </span>
          ) : null}
        </span>
        {sub ? (
          <span className="text-2xs text-muted-foreground">{sub}</span>
        ) : null}
      </span>
    </button>
  );
}
