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
  SegmentedControl,
  SegmentedControlItem,
} from "@agentre-hub/agentre-ui";

import { FailureBody } from "./failure-body";
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
    loadState,
    loadError,
    retryLoad,
    dirty,
    pendingLeave,
    confirmLeave,
    cancelLeave,
    busy,
    running,
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

  if (loadState === "loading") {
    return (
      <div className="flex h-full min-w-0 flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
        {t("hooks.loading")}
      </div>
    );
  }

  if (loadState === "failed") {
    return (
      <div className="flex h-full min-w-0 flex-1 flex-col items-center justify-center gap-2 text-sm text-muted-foreground">
        <p>{t("hooks.loadFailed")}</p>
        {loadError ? (
          <div className="max-w-md text-xs">
            <FailureBody
              message={loadError.message}
              detail={loadError.detail}
            />
          </div>
        ) : null}
        <Button type="button" variant="outline" size="sm" onClick={retryLoad}>
          {t("common.retry")}
        </Button>
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
        dirty={dirty}
        onQueryChange={setQuery}
        onSelect={selectHook}
        onCreate={startCreate}
        t={t}
      />

      {/* Main */}
      <main className="content-sheet flex min-w-0 flex-1 flex-col">
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
              dirty={dirty}
              running={running}
              busy={busy}
              onRun={run}
              onToggle={toggle}
              onDelete={setDeleteTarget}
              t={t}
            />

            {/* Tabs */}
            <div className="px-7 pt-3">
              <SegmentedControl
                value={activeTab}
                onValueChange={(tab) => setActiveTab(tab as typeof activeTab)}
                aria-label={t("hooks.title")}
              >
                {(["script", "runLog"] as const).map((tab) => (
                  <SegmentedControlItem key={tab} value={tab}>
                    {t(`hooks.tabs.${tab}`)}
                    {tab === "runLog" && events.length > 0 ? (
                      <Badge variant="secondary" className="font-mono text-3xs">
                        {events.length}
                      </Badge>
                    ) : null}
                  </SegmentedControlItem>
                ))}
              </SegmentedControl>
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

      {/* Discard confirm */}
      <Dialog
        open={pendingLeave != null}
        onOpenChange={(open) => !open && cancelLeave()}
      >
        <DialogContent showCloseButton={false} aria-describedby={undefined}>
          <DialogHeader>
            <DialogTitle>
              {t("hooks.discard.title", { name: draft?.name ?? "" })}
            </DialogTitle>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={cancelLeave}>
              {t("hooks.discard.keep")}
            </Button>
            <Button type="button" variant="destructive" onClick={confirmLeave}>
              {t("hooks.discard.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
