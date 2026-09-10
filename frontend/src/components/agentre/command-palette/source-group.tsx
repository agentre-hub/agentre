// 一组搜索结果:分组标题 + 组内若干 SearchRow。

import * as React from "react";
import { Command as CommandPrimitive } from "cmdk";
import { useTranslation } from "react-i18next";

import { cn } from "@/lib/utils";

import type { CommandItemBase, CommandSource, OnSelectCtx } from "./types";

export type SourceGroupProps<T extends CommandItemBase> = {
  source: CommandSource<T>;
  sourcePriority: number;
  query: string;
  ctx: OnSelectCtx;
  onFirstSelectableChange: (sourcePriority: number, itemKeys: string[]) => void;
};

export function SourceGroup<T extends CommandItemBase>({
  source,
  sourcePriority,
  query,
  ctx,
  onFirstSelectableChange,
}: SourceGroupProps<T>) {
  const { t } = useTranslation();
  const { items, loading } = source.useItems();
  const q = query.trim();
  const ranked = React.useMemo(() => {
    const withScore = items
      .map((item) => ({ item, score: source.getScore(q, item) }))
      .filter((r) => r.score > 0);
    withScore.sort((a, b) => b.score - a.score);
    // 二次稳定排序: 把同 subHeading 的 item 聚到一起 (先出现的 subHeading 排前面);
    // 没有 subHeading 的当作 "" 单独成组并排到末尾。query 模式下 score 已是主序,
    // 这步只是把"散落到不同分组"的 item 重新汇拢,避免分组分隔条来回插入。
    const order = new Map<string, number>();
    let next = 0;
    for (const r of withScore) {
      const key = r.item.subHeading ?? "";
      if (!order.has(key)) order.set(key, next++);
    }
    withScore.sort((a, b) => {
      const ka = a.item.subHeading ?? "";
      const kb = b.item.subHeading ?? "";
      const oa = order.get(ka) ?? next;
      const ob = order.get(kb) ?? next;
      if (oa !== ob) return oa - ob;
      return b.score - a.score;
    });
    return withScore.slice(0, 50);
  }, [items, q, source]);

  const selectableKeys = React.useMemo(
    () =>
      ranked
        .filter(({ item }) => !(source.isDisabled?.(item) ?? false))
        .map(({ item }) => item.key),
    [ranked, source],
  );
  const selectableKeySignature = selectableKeys.join("\u0000");

  React.useLayoutEffect(() => {
    onFirstSelectableChange(sourcePriority, selectableKeys);
    // selectableKeySignature is the stable semantic dependency; selectableKeys
    // is intentionally omitted because its array identity changes every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onFirstSelectableChange, selectableKeySignature, sourcePriority]);

  const heading = q
    ? t("commandPalette.group.matches", { count: ranked.length })
    : t("commandPalette.group.activeFirst", { count: ranked.length });

  if (loading && ranked.length === 0 && !q) {
    return (
      <div className="px-5 py-8 text-center text-xs text-muted-foreground">
        {t("common.loading")}
      </div>
    );
  }
  if (ranked.length === 0) return null;

  return (
    <>
      <div className="flex items-center justify-between px-4 pb-1 pt-3">
        <span className="text-2xs font-semibold tracking-wide text-muted-foreground">
          {source.heading}
        </span>
        <span className="text-2xs text-muted-foreground/70">{heading}</span>
      </div>
      <CommandPrimitive.Group className="px-1 pb-1">
        {ranked.map(({ item }, idx) => {
          const prev = idx > 0 ? ranked[idx - 1].item.subHeading : undefined;
          const showSub = !!item.subHeading && item.subHeading !== prev;
          const disabled = source.isDisabled?.(item) ?? false;
          return (
            <React.Fragment key={item.key}>
              {showSub ? (
                <div
                  className="px-3 pb-1 pt-2 text-2xs font-semibold uppercase tracking-wider text-muted-foreground"
                  aria-hidden="true"
                >
                  {item.subHeading}
                </div>
              ) : null}
              <CommandPrimitive.Item
                value={item.key}
                disabled={disabled}
                onSelect={() => source.onSelect(item, ctx)}
                className={cn(
                  "group/cmditem flex items-center gap-3 rounded-md px-3 py-2 outline-none transition-colors",
                  disabled ? "cursor-not-allowed" : "cursor-pointer",
                  "data-[selected=true]:bg-accent data-[selected=true]:text-accent-foreground",
                )}
              >
                {source.renderItem(item, { active: false })}
              </CommandPrimitive.Item>
            </React.Fragment>
          );
        })}
      </CommandPrimitive.Group>
    </>
  );
}

// Tab / Shift+Tab 直接操作 new-chat-context-store（焦点不动）。
// 复用 ContextBar 的写库 / localStorage 写入逻辑，保证两套入口（点击 vs 键盘）
// 状态变化一致。
