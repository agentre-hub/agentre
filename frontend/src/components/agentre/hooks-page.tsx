// Hooks 页:脚本编辑与运行记录两个页签。
//
// 两个页签本体与它们用的卡片在 hooks-page/ 下。

import { useTranslation } from "react-i18next";
import { Loader2, Plus, Save } from "lucide-react";
import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agentre-hub/agentre-ui";

import { cn } from "@/lib/utils";

import { HookDetailHeader } from "./hooks-page-header";
import { HooksSidebar } from "./hooks-page-sidebar";
import { useHooksPage } from "./use-hooks-page";

import { RunLogTab } from "./hooks-page/run-log-tab";
import { RunResultCard } from "./hooks-page/run-result-card";
import { ScriptTab } from "./hooks-page/script-tab";

export function HooksPage() {
  const { t } = useTranslation();
  const {
    hooks,
    events,
    selectedId,
    draft,
    setDraft,
    activeTab,
    setActiveTab,
    loading,
    busy,
    running,
    flash,
    query,
    setQuery,
    runResult,
    selectedEventId,
    setSelectedEventId,
    deleteTarget,
    setDeleteTarget,
    interpreters,
    filtered,
    selectedHook,
    headerMeta,
    selectHook,
    startCreate,
    save,
    toggle,
    confirmDelete,
    run,
  } = useHooksPage();

  if (loading) {
    return (
      <div className="flex h-full min-w-0 flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        {t("hooks.loading")}
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 min-w-0 flex-1">
      <HooksSidebar
        hooks={hooks}
        filtered={filtered}
        query={query}
        selectedId={selectedId}
        onQueryChange={setQuery}
        onSelect={selectHook}
        onCreate={startCreate}
        t={t}
      />

      {/* Main */}
      <main className="flex min-w-0 flex-1 flex-col">
        {flash ? (
          <div
            className={cn(
              "flex items-center gap-2 border-b px-7 py-2 text-xs",
              flash.kind === "ok"
                ? "border-status-running/30 bg-status-running-bg text-status-running"
                : "border-status-error/30 text-status-error",
            )}
            role="status"
          >
            {flash.text}
          </div>
        ) : null}

        {!draft ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
            <p>{t("hooks.list.empty")}</p>
            <Button type="button" size="sm" onClick={startCreate}>
              <Plus className="mr-1 h-3.5 w-3.5" />
              {t("hooks.list.addAria")}
            </Button>
          </div>
        ) : (
          <>
            <HookDetailHeader
              draft={draft}
              headerMeta={headerMeta}
              selectedHook={selectedHook}
              selectedId={selectedId}
              running={running}
              busy={busy}
              onRun={run}
              onToggle={toggle}
              onDelete={setDeleteTarget}
              t={t}
            />

            {/* Tabs */}
            <div className="flex gap-1 border-b border-border px-7">
              {(["script", "runLog"] as const).map((tab) => (
                <button
                  key={tab}
                  type="button"
                  role="tab"
                  aria-selected={activeTab === tab}
                  onClick={() => setActiveTab(tab)}
                  className={cn(
                    "flex items-center gap-1.5 border-b-2 px-1.5 pb-2.5 pt-3 text-aux font-medium transition-colors",
                    activeTab === tab
                      ? "border-primary text-foreground"
                      : "border-transparent text-muted-foreground",
                  )}
                >
                  {t(`hooks.tabs.${tab}`)}
                  {tab === "runLog" && events.length > 0 ? (
                    <Badge variant="secondary" className="font-mono text-3xs">
                      {events.length}
                    </Badge>
                  ) : null}
                </button>
              ))}
            </div>

            {/* Body */}
            <div className="min-h-0 flex-1 overflow-y-auto px-7 py-5">
              {activeTab === "script" ? (
                <div className="flex flex-col gap-4">
                  <ScriptTab
                    draft={draft}
                    onChange={setDraft}
                    interpreters={interpreters}
                    t={t}
                  />
                  {runResult ? (
                    <RunResultCard result={runResult} t={t} />
                  ) : null}
                  <div className="flex justify-end">
                    <Button
                      type="button"
                      data-testid="hook-save"
                      onClick={save}
                      disabled={busy || !draft.name.trim()}
                    >
                      {busy ? (
                        <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                      ) : (
                        <Save className="mr-1.5 h-3.5 w-3.5" />
                      )}
                      {draft.id == null
                        ? t("hooks.script.create")
                        : t("hooks.script.save")}
                    </Button>
                  </div>
                </div>
              ) : (
                <RunLogTab
                  events={events}
                  selectedEventId={selectedEventId}
                  onSelectEvent={setSelectedEventId}
                  t={t}
                />
              )}
            </div>
          </>
        )}
      </main>

      {/* Delete confirm */}
      <Dialog
        open={deleteTarget != null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("hooks.del.title")}</DialogTitle>
            <DialogDescription>
              {t("hooks.del.description", { name: deleteTarget?.name ?? "" })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setDeleteTarget(null)}
            >
              {t("hooks.del.cancel")}
            </Button>
            <Button type="button" variant="destructive" onClick={confirmDelete}>
              {t("hooks.del.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
