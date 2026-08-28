/**
 * 项目的字形选择器：图标与颜色在**同一个**弹层里挑。
 *
 * 它们合起来才是侧栏里那一枚 `ProjectGlyph`——挑的时候摆在两格里，读者就得在脑子里
 * 把它们合成一遍。合之前更糟：桌面端「项目设置」的图标是一个要人手打 key 的输入框，
 * placeholder 写着「folder / briefcase / 自定义 emoji」——而那三种值**一个都画不出来**：
 * 侧栏的字形走 `hasIcon(key)` 判定，词表里既没有 `folder` 也没有 `briefcase`，emoji
 * 更不在其中，于是照着 placeholder 填完，字形照旧退回项目名首字。那一格从头到尾在
 * 说一件做不到的事。
 *
 * **图标网格归包**，与 Agent 头像那一侧不同：头像还有「上传图片」这一档（浏览器宿主
 * 的读写端点不带头像正文，画一个上传按钮就是伪造后端），所以那边的选择器留在宿主；
 * 项目字形没有这一档，存的就是词表里那三十个 key，两端同一张表（`org/icon-registry`
 * 已经把它收成一份）。既然如此就没有理由让两个宿主各画一次网格。
 */
import * as React from "react";
import { Check, Pencil, Search } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { agentColorOrder, tokenToCssColor } from "../lib/agent-color";
import { cn } from "../lib/utils";
import {
  hasIcon,
  iconForKey,
  iconsByCategory,
  searchIcons,
} from "../org/icon-registry";
import { ProjectGlyph } from "../session-index/project-glyph";
import { Input } from "../ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";

export interface ProjectGlyphPickerProps {
  /** 字形上退回首字时用它，也进弹层触发器的 aria-label。 */
  name: string;
  /** 当前 icon key（词表里的那三十个之一）。 */
  icon?: string;
  /** 颜色 token，如 "agent-3"。 */
  color?: string;
  onPickIcon(iconKey: string): void;
  onPickColor(color: string): void;
  className?: string;
}

export function ProjectGlyphPicker({
  name,
  icon,
  color,
  onPickIcon,
  onPickColor,
  className,
}: ProjectGlyphPickerProps) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          data-testid="project-glyph-trigger"
          aria-label={t("projectSettings.identity.glyphAria", { name })}
          className={cn(
            "group/glyph relative shrink-0 rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
            className,
          )}
        >
          <ProjectGlyph
            testId="project-glyph-preview"
            project={{ name, color }}
            glyph={glyphNode(icon)}
            className="size-13 rounded-xl text-base"
          />
          {/* 一枚记号说明它点得动：字形本身长得像展示件，不像控件。 */}
          <span
            aria-hidden="true"
            className="absolute -bottom-1 -right-1 inline-flex size-5 items-center justify-center rounded-full border border-border bg-card text-muted-foreground shadow-sm transition-colors group-hover/glyph:text-foreground"
          >
            <Pencil className="size-2.5" />
          </span>
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[320px] p-0">
        <div className="p-3">
          <p className="text-2xs font-semibold tracking-wide text-muted-foreground">
            {t("projectSettings.field.color")}
          </p>
          <div
            data-testid="project-glyph-colors"
            className="mt-2 grid grid-cols-8 gap-1.5"
          >
            {agentColorOrder.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={c}
                // 选中态挂在 aria-pressed 上而不是只靠描边：十六颗一样的圆点里，
                // 读屏用户与自动化都得说得出选的是哪一颗。
                aria-pressed={color === c}
                data-testid={`project-glyph-color-${c}`}
                onClick={() => onPickColor(c)}
                style={{ backgroundColor: tokenToCssColor(c) ?? undefined }}
                className={cn(
                  "inline-flex size-6 items-center justify-center rounded-full text-agent-foreground",
                  color === c &&
                    "outline outline-2 outline-offset-2 outline-foreground",
                )}
              >
                {color === c ? (
                  <Check className="size-3.5" aria-hidden="true" />
                ) : null}
              </button>
            ))}
          </div>
        </div>
        <IconGrid
          value={icon ?? ""}
          onPick={(key) => {
            onPickIcon(key);
            // 挑完就关：这个弹层只有一件事要做，留着它等于让人再点一次外面。
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}

/**
 * 词表认不得的 key 交 `undefined`，让字形去画项目名首字。
 *
 * 必须是 `undefined` 而不是一个渲染成 null 的元素：`AgentAvatar` 走的是
 * `icon ?? initials`，一个空元素照样是真值，于是首字被顶掉、那一格什么都不画。
 * 这也正是侧栏 `agentIconNode` 的判据，两处同一条规则。
 */
function glyphNode(iconKey?: string) {
  if (!hasIcon(iconKey)) return undefined;
  const Icon = iconForKey(iconKey);
  return <Icon className="size-[55%]" aria-hidden="true" />;
}

/**
 * 图标网格：搜索 + 按分类分组，与 Agent 头像那一侧同一张词表、同一套文案。
 *
 * 搜不到时说的是「没有匹配」而不是留白 —— 空网格会被读成「这个词表是空的」。
 */
function IconGrid({
  value,
  onPick,
}: {
  value: string;
  onPick: (iconKey: string) => void;
}) {
  const { t } = useUiTranslation();
  const [query, setQuery] = React.useState("");
  const trimmed = query.trim();
  const groups = React.useMemo(
    () =>
      trimmed
        ? [
            {
              category: "search",
              label: t("projectSettings.identity.iconResults"),
              items: searchIcons(trimmed, t),
            },
          ]
        : iconsByCategory(t),
    [t, trimmed],
  );

  return (
    <div data-testid="project-glyph-icons" className="border-t border-border">
      <div className="relative px-3 py-2">
        <Search
          className="pointer-events-none absolute left-5 top-1/2 size-3 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          data-testid="project-glyph-icon-search"
          aria-label={t("projectSettings.identity.iconSearch")}
          placeholder={t("projectSettings.identity.iconSearch")}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="h-8 pl-7 text-xs"
        />
      </div>
      <div className="max-h-[220px] overflow-y-auto px-3 pb-3">
        {groups.map((g, i) => (
          <div key={g.category} className={cn(i > 0 && "mt-3")}>
            <p className="text-2xs text-muted-foreground">{g.label}</p>
            <div className="mt-1.5 grid grid-cols-8 gap-1">
              {g.items.map((meta) => {
                const Icon = iconForKey(meta.key);
                const active = meta.key === value;
                return (
                  <button
                    key={meta.key}
                    type="button"
                    title={meta.label}
                    aria-label={meta.label}
                    aria-pressed={active}
                    data-testid={`project-glyph-icon-${meta.key}`}
                    onClick={() => onPick(meta.key)}
                    className={cn(
                      "inline-flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground",
                      active && "bg-primary-soft text-primary-text",
                    )}
                  >
                    <Icon className="size-4" aria-hidden="true" />
                  </button>
                );
              })}
            </div>
          </div>
        ))}
        {groups.every((g) => g.items.length === 0) ? (
          <p
            data-testid="project-glyph-icon-none"
            className="py-2 text-2xs text-muted-foreground"
          >
            {t("projectSettings.identity.iconNoMatch")}
          </p>
        ) : null}
      </div>
    </div>
  );
}
