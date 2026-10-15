// 项目上下文选择器:一枚 chip + 它打开的弹层(搜索框 / 「无项目」/ 可滚动的项目树)。
//
// 树的收窄规则、命中高亮与项目字形和看板的「项目范围」选择器同源(包里的
// scope-tree + ProjectGlyph),同一个项目在两处是同一个样子。
//
// 弹层是自己的键盘作用域:portal 只挪 DOM,React 事件照旧沿组件树冒泡到 cmdk 根。
// 不在这里拦住的话,方向键移动的是背后的命令列表,回车执行的是背后高亮的那条命令。

import * as React from "react";
import { Check, ChevronDown, Search } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
  ProjectGlyph,
  buildScopeRows,
  cn,
  filterScopeRows,
  splitMatch,
  type ScopeRow,
} from "@agentre-hub/agentre-ui";
import { type ProjectFlat } from "@/hooks/use-project-list";
import { type ProjectContext } from "@/stores/new-chat-context-store";

export type ProjectChipPickerProps = {
  projectContext: ProjectContext | null;
  projects: ProjectFlat[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPick: (picked: ProjectFlat | null) => void;
  // 收起后焦点交还给谁(命令输入框)。不给就按 Radix 默认回到 chip。
  returnFocusRef?: React.RefObject<HTMLInputElement | null>;
};

export function ProjectChipPicker({
  projectContext,
  projects,
  open,
  onOpenChange,
  onPick,
  returnFocusRef,
}: ProjectChipPickerProps) {
  const { t } = useTranslation();
  const rows = React.useMemo(
    () =>
      buildScopeRows(
        projects.map((p) => ({
          id: p.id,
          name: p.name,
          depth: p.depth ?? 0,
          color: p.color,
          icon: p.icon,
        })),
      ),
    [projects],
  );
  const selected = projectContext
    ? rows.find((row) => row.node.id === projectContext.projectID)
    : undefined;
  // 列表里找得到就用列表里的名字(改名后是新的);找不到(列表还没到 / 项目已删)
  // 退回上下文里记着的那个。
  const name = projectContext
    ? (selected?.node.name ??
      (projectContext.projectName ||
        t("commandPalette.project.fallbackName", {
          id: projectContext.projectID,
        })))
    : t("commandPalette.project.none");
  const path = selected?.path ?? [];

  return (
    // modal:面板本身是个 modal Dialog,它的滚动锁只认自己那块 DOM,portal 出去的
    // 弹层里滚轮会被整个吃掉。modal 弹层自带一把锁并压在最上面,树才滚得动;
    // 顺带地,点弹层外面只是收起它,不会同时点中背后的命令。
    <Popover modal open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={t("commandPalette.project.switchAria")}
          title={t("commandPalette.project.switchTitle")}
          className={cn(
            "inline-flex h-[22px] min-w-0 max-w-[20rem] items-center gap-1.5 rounded-sm bg-field-fill pl-1 pr-1.5 text-xs font-medium text-foreground outline-none transition-colors hover:bg-field-fill-hover",
            "focus-visible:focus-outline",
            open && "bg-accent",
            !projectContext && "text-muted-foreground",
          )}
        >
          <ProjectGlyph
            project={
              projectContext
                ? {
                    name,
                    color: selected?.node.color,
                    icon: selected?.node.icon,
                  }
                : null
            }
            className="size-3.5 shrink-0 rounded-[4px]"
          />
          {path.length > 0 ? (
            // 放不下时先截父级、保住子项目名:两个父项目下的同名子项目从尾部截断
            // 会变成同一个样子。
            <>
              <span className="min-w-0 truncate font-normal text-muted-foreground">
                {path.join(" / ")}
              </span>
              <span
                className="shrink-0 font-normal text-muted-foreground"
                aria-hidden="true"
              >
                /
              </span>
            </>
          ) : null}
          <span className="max-w-[14rem] shrink-0 truncate">{name}</span>
          <ChevronDown
            className="size-[11px] shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        sideOffset={4}
        className="w-72 p-0"
        // 面板没接住的按键(焦点落在弹层留白上时)也到此为止,不漏给 cmdk 根。
        onKeyDown={(event) => event.stopPropagation()}
        onCloseAutoFocus={(event) => {
          if (!returnFocusRef?.current) return;
          event.preventDefault();
          returnFocusRef.current.focus();
        }}
      >
        <ProjectPickerPanel
          rows={rows}
          value={projectContext?.projectID ?? null}
          onPick={(id) => {
            onPick(
              id == null ? null : (projects.find((p) => p.id === id) ?? null),
            );
            onOpenChange(false);
          }}
          onClose={() => onOpenChange(false)}
        />
      </PopoverContent>
    </Popover>
  );
}

// 已选中项与键盘游标是两个视觉,不共用一块底色。
function rowClassName(selected: boolean, cursor: boolean, dimmed: boolean) {
  return cn(
    "flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left text-xs transition-colors",
    !cursor && !selected && "hover:bg-accent/60 hover:on-accent",
    cursor && "bg-accent",
    selected && "bg-primary-soft text-primary-text",
    selected && cursor && "bg-primary/15",
    dimmed && "opacity-60",
  );
}

type ProjectPickerPanelProps = {
  rows: ScopeRow[];
  value: number | null;
  onPick: (projectID: number | null) => void;
  onClose: () => void;
};

// 弹层里的那一块。只在弹层打开期间挂载,所以搜索词与游标每次打开都重新起步。
function ProjectPickerPanel({
  rows: allRows,
  value,
  onPick,
  onClose,
}: ProjectPickerPanelProps) {
  const { t } = useTranslation();
  const [needle, setNeedle] = React.useState("");
  const rows = React.useMemo(
    () => filterScopeRows(allRows, needle),
    [allRows, needle],
  );
  // 「无项目」是游标的第 0 行;搜索时它让位。一个项目都没有、也没选着什么的时候
  // 它选了也没区别,同样不摆。
  const showNone =
    needle.trim() === "" && (allRows.length > 0 || value != null);
  const offset = showNone ? 1 : 0;
  const count = rows.length + offset;
  // 从已选中项起步:找项目多半是找它附近的,从头数是白按。
  const [cursor, setCursor] = React.useState(() => {
    const index = allRows.findIndex((row) => row.node.id === value);
    return index === -1 ? 0 : index + 1;
  });
  const at = Math.min(cursor, count - 1);

  const treeRef = React.useRef<HTMLDivElement | null>(null);
  React.useEffect(() => {
    treeRef.current
      ?.querySelector('[data-cursor="true"]')
      ?.scrollIntoView?.({ block: "nearest" });
  }, [at]);

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (count === 0) return;
      const step = event.key === "ArrowDown" ? 1 : -1;
      setCursor((at + step + count) % count);
      return;
    }
    if (event.key === "Enter") {
      // 输入法上屏的那一下回车不是「选定」。
      if (event.nativeEvent.isComposing || event.keyCode === 229) return;
      event.preventDefault();
      if (count === 0) return;
      onPick(showNone && at === 0 ? null : rows[at - offset].node.id);
      return;
    }
    if (event.key === "Tab") {
      // Tab 打开它,再按一次收起;焦点不在弹层里的几行之间游走。
      event.preventDefault();
      onClose();
    }
  };

  return (
    <div
      className="flex max-h-[min(22rem,var(--radix-popover-content-available-height))] min-h-0 flex-col"
      onKeyDown={onKeyDown}
    >
      <div className="flex shrink-0 items-center gap-1.5 px-2.5 py-1.5">
        <Search
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <input
          // 打开就落在搜索框上:上下键要有落点。
          autoFocus
          value={needle}
          onChange={(event) => {
            const next = event.target.value;
            setNeedle(next);
            // 落在第一条「自己命中」的行上,而不是被留下来撑路径的祖先。
            const own = filterScopeRows(allRows, next).findIndex(
              (row) => !row.ancestorOnly,
            );
            setCursor(next.trim() === "" ? 0 : Math.max(own, 0));
          }}
          placeholder={t("commandPalette.project.searchPlaceholder")}
          aria-label={t("commandPalette.project.searchAria")}
          className="h-6 w-full min-w-0 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
        />
      </div>
      {showNone ? (
        <div className="shrink-0 p-1">
          <button
            type="button"
            data-selected={value == null ? "true" : undefined}
            data-cursor={at === 0 ? "true" : undefined}
            onClick={() => onPick(null)}
            className={rowClassName(value == null, at === 0, false)}
          >
            <ProjectGlyph
              project={null}
              className="size-3.5 shrink-0 rounded-[4px]"
            />
            <span className="truncate">{t("commandPalette.project.none")}</span>
            {value == null ? (
              <Check className="ml-auto size-3.5 shrink-0" aria-hidden="true" />
            ) : null}
          </button>
        </div>
      ) : null}
      {allRows.length === 0 ? (
        <p className="px-2 py-5 text-center text-2xs text-muted-foreground">
          {t("commandPalette.project.empty")}
        </p>
      ) : (
        <div
          ref={treeRef}
          className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto p-1"
        >
          {rows.length === 0 ? (
            <p className="px-2 py-3 text-center text-2xs text-muted-foreground">
              {t("commandPalette.project.noMatch")}
            </p>
          ) : null}
          {rows.map((row, index) => {
            const selected = row.node.id === value;
            const isCursor = index + offset === at;
            return (
              <button
                key={row.node.id}
                type="button"
                data-selected={selected ? "true" : undefined}
                data-cursor={isCursor ? "true" : undefined}
                onClick={() => onPick(row.node.id)}
                className={rowClassName(selected, isCursor, row.ancestorOnly)}
              >
                {Array.from({ length: row.node.depth }).map((_, level) => (
                  <span
                    key={level}
                    aria-hidden="true"
                    className="ml-1 h-4 w-px shrink-0 self-stretch bg-border-strong"
                  />
                ))}
                <ProjectGlyph
                  project={{
                    name: row.node.name,
                    color: row.node.color,
                    icon: row.node.icon,
                  }}
                  className="size-3.5 shrink-0 rounded-[4px]"
                />
                <span className="truncate">
                  {splitMatch(row.node.name, needle).map((segment, i) =>
                    segment.match ? (
                      <mark
                        key={i}
                        className="bg-transparent font-semibold text-primary-text"
                      >
                        {segment.text}
                      </mark>
                    ) : (
                      <React.Fragment key={i}>{segment.text}</React.Fragment>
                    ),
                  )}
                </span>
                {selected ? (
                  <Check
                    className="ml-auto size-3.5 shrink-0"
                    aria-hidden="true"
                  />
                ) : null}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
