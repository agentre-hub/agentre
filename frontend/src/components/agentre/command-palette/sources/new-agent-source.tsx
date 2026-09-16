import type { TFunction } from "i18next";
import { Bot } from "lucide-react";

import i18n from "@/i18n";
import { requestNewAgentDialog } from "@/stores/new-agent-intent-store";

import { scoreSuggestion as scoreItem } from "@agentre-hub/agentre-ui";
import type { CommandSource, OnSelectCtx } from "../types";

type NewAgentItem = {
  key: string;
};

const ITEM: NewAgentItem = { key: "new-agent" };

function title(t: TFunction): string {
  return t("commandPalette.newAgent.title");
}

function useItems(): { items: NewAgentItem[]; loading: boolean } {
  return { items: [ITEM], loading: false };
}

function getScore(query: string, item: NewAgentItem): number {
  return scoreItem({
    query,
    title: i18n.t("commandPalette.newAgent.title"),
    subtitle: item.key,
  });
}

function renderItem(): React.ReactNode {
  const t = i18n.t.bind(i18n);
  return (
    <div className="flex min-w-0 items-center gap-3">
      <Bot className="size-4 shrink-0 text-primary-text" aria-hidden="true" />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-sm text-foreground">{title(t)}</span>
        <span className="truncate text-2xs text-muted-foreground">
          {t("commandPalette.newAgent.description")}
        </span>
      </div>
      <kbd
        className="rounded-sm border border-border bg-card px-1.5 py-0.5 font-mono text-2xs font-medium text-muted-foreground opacity-0 group-data-[selected=true]/cmditem:opacity-100"
        aria-hidden="true"
      >
        ↵
      </kbd>
    </div>
  );
}

function onSelect(_item: NewAgentItem, ctx: OnSelectCtx): void {
  ctx.close();
  requestNewAgentDialog();
  ctx.navigate("/org");
}

export const newAgentSource: CommandSource<NewAgentItem> = {
  id: "new-agent",
  heading: i18n.t("commandPalette.newAgent.heading"),
  modes: ["command"],
  useItems,
  getScore,
  renderItem,
  onSelect,
};
