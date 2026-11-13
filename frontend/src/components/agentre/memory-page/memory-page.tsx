import * as React from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate } from "react-router-dom";

import { Button } from "@agentre-hub/agentre-ui";

import {
  memoryOwnerPath,
  memoryRecentPath,
  parseMemorySelection,
} from "@/lib/app-routing";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";

import { useRefreshSignal } from "../use-refresh-signal";

import { ownerKey, type MemoryDirectoryEntry } from "./directory";
import { MemoryOwnerPanel } from "./memory-owner-panel";
import { MemoryRecentPanel } from "./memory-recent-panel";
import { MemorySearchPanel } from "./memory-search-panel";
import { MemorySidebar } from "./memory-sidebar";
import type { CreatorContext } from "./to-entry-view";
import { useMemoryDirectory } from "./use-memory-directory";

/**
 * 记忆页（spec 2026-11-11「记忆页」）：左列归属目录，右侧是选中的归属、最近变更或
 * 搜索结果。选中项只活在路由的 query 里（`lib/app-routing.ts` 的
 * `memoryOwnerPath` / `parseMemorySelection`），所以别处的深链与左列点击是同一条路。
 *
 * 其他设备同步下来的变更（`sync:applied`）与本机其他入口的写入（`config:changed`）
 * 到达时，左列与右侧就地重拉；正在编辑的行由右侧面板持有编辑态、表单持有草稿，
 * 重拉不会把它覆盖。
 */
export function MemoryPage() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const selection = parseMemorySelection(location.search);
  const { state, reload, retry } = useMemoryDirectory();
  const [query, setQuery] = React.useState("");
  const refreshSignal = useRefreshSignal();
  const sessionMetas = useSessionMetaStore((s) => s.metas);

  const firstSignal = React.useRef(refreshSignal);
  React.useEffect(() => {
    if (refreshSignal === firstSignal.current) return;
    void reload();
  }, [refreshSignal, reload]);

  const directory = state.status === "ready" ? state.directory : null;

  const creator = React.useMemo<CreatorContext>(
    () => ({
      sessionTitle: (sessionId) =>
        sessionMetas.get(sessionId)?.title || undefined,
      fallbackLabel: (agentId) => {
        const agent = directory?.byKey.get(
          ownerKey({ kind: "agent", id: agentId }),
        );
        return agent
          ? t("memoryPage.creator.agentSession", { agent: agent.label })
          : t("memoryPage.recent.unknownAgent");
      },
      openSession: (sessionId) => {
        useChatTabsStore.getState().openSession(sessionId);
        navigate("/chat");
      },
    }),
    [directory, navigate, sessionMetas, t],
  );

  const openOwner = (entry: MemoryDirectoryEntry) => {
    setQuery("");
    navigate(memoryOwnerPath(entry.owner));
  };

  if (state.status === "loading") {
    return (
      <div
        role="status"
        aria-label={t("memoryPage.loading")}
        className="flex min-h-0 min-w-0 flex-1 items-center justify-center text-muted-foreground"
      >
        <Loader2 className="size-4 animate-spin motion-reduce:animate-none" />
      </div>
    );
  }

  if (state.status === "error") {
    return (
      <div className="flex min-h-0 min-w-0 flex-1 flex-col items-center justify-center gap-3">
        <span className="text-sm text-destructive-text">
          {t("memoryPage.pageLoadFailed")}
        </span>
        <Button variant="outline" size="sm" onClick={retry}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  const dir = state.directory;
  // 深链指向一个已不存在的归属（被删的 agent 等）时落到全局。
  const selectedEntry =
    selection.view === "owner"
      ? (dir.byKey.get(ownerKey(selection.owner)) ?? dir.global)
      : null;
  const selectedKey = selectedEntry ? selectedEntry.key : "recent";
  const searching = query.trim() !== "";

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-1">
      <MemorySidebar
        directory={dir}
        counts={state.counts}
        recentCount={state.recent.length}
        selectedKey={selectedKey}
        query={query}
        onQueryChange={setQuery}
        onSelectOwner={openOwner}
        onSelectRecent={() => {
          setQuery("");
          navigate(memoryRecentPath());
        }}
      />
      <main className="content-sheet flex min-h-0 min-w-0 flex-1 flex-col text-foreground">
        {searching ? (
          <MemorySearchPanel
            query={query}
            directory={dir}
            refreshSignal={refreshSignal}
            onOpenOwner={openOwner}
          />
        ) : selectedEntry ? (
          <MemoryOwnerPanel
            key={selectedEntry.key}
            entry={selectedEntry}
            directory={dir}
            creator={creator}
            refreshSignal={refreshSignal}
            onChanged={() => void reload()}
            onOpenOwner={openOwner}
          />
        ) : (
          <MemoryRecentPanel
            items={state.recent}
            directory={dir}
            onChanged={reload}
          />
        )}
      </main>
    </div>
  );
}
