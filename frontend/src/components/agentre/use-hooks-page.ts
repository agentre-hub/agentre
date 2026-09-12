import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  CreateHook,
  DeleteHook,
  LoadHooks,
  ProbeInterpreters,
  RunHook,
  ToggleHook,
  UpdateHook,
} from "../../../wailsjs/go/app/App";
import { hook_svc } from "../../../wailsjs/go/models";

import {
  draftFromHook,
  emptyDraft,
  hookMatchesQuery,
  interpMeta,
  runOk,
  type Draft,
  type FlashState,
  type HookEventItem,
  type HookItem,
  type HookTab,
  type InterpreterOption,
  type RunHookResult,
} from "./hooks-page-model";

/**
 * HooksPage 的取数与写入：列表 / 选中 / 草稿 / 运行结果各自的状态，加上保存、
 * 启停、删除、试运行四个动作。页面本身只剩装配与呈现。
 */
export function useHooksPage() {
  const { t } = useTranslation();
  const [hooks, setHooks] = React.useState<HookItem[]>([]);
  const [events, setEvents] = React.useState<HookEventItem[]>([]);
  const [selectedId, setSelectedId] = React.useState<number | null>(null);
  const [draft, setDraft] = React.useState<Draft | null>(null);
  const [activeTab, setActiveTab] = React.useState<HookTab>("script");
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);
  const [running, setRunning] = React.useState(false);
  const [flash, setFlash] = React.useState<FlashState>(null);
  const [query, setQuery] = React.useState("");
  const [runResult, setRunResult] = React.useState<RunHookResult | null>(null);
  const [selectedEventId, setSelectedEventId] = React.useState<number | null>(
    null,
  );
  const [deleteTarget, setDeleteTarget] = React.useState<HookItem | null>(null);
  const [interpreters, setInterpreters] = React.useState<InterpreterOption[]>(
    [],
  );

  const flashOk = React.useCallback(
    (text: string) => setFlash({ kind: "ok", text }),
    [],
  );
  const flashErr = React.useCallback(
    (text: string) => setFlash({ kind: "err", text }),
    [],
  );

  const loadEvents = React.useCallback(async (hookId: number) => {
    try {
      const resp = await LoadHooks({ hookId, limit: 50 });
      setEvents(resp.events ?? []);
      setSelectedEventId(resp.events?.[0]?.id ?? null);
    } catch {
      setEvents([]);
    }
  }, []);

  const selectHook = React.useCallback(
    (hook: HookItem) => {
      setSelectedId(hook.id);
      setDraft(draftFromHook(hook));
      setRunResult(null);
      void loadEvents(hook.id);
    },
    [loadEvents],
  );

  const reload = React.useCallback(
    async (preferId?: number | null) => {
      const resp = await LoadHooks({
        hookId: 0,
        limit: 100,
      });
      const list = resp.hooks ?? [];
      setHooks(list);
      const target = list.find((h) => h.id === preferId) ?? list[0] ?? null;
      if (target) {
        setSelectedId(target.id);
        setDraft(draftFromHook(target));
        void loadEvents(target.id);
      } else {
        setSelectedId(null);
        setDraft(null);
      }
      return list;
    },
    [loadEvents],
  );

  React.useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        const resp = await LoadHooks({
          hookId: 0,
          limit: 100,
        });
        if (!alive) return;
        const list = resp.hooks ?? [];
        setHooks(list);
        if (list[0]) {
          setSelectedId(list[0].id);
          setDraft(draftFromHook(list[0]));
          void loadEvents(list[0].id);
        }
      } catch {
        if (alive) flashErr(t("hooks.flash.saveFailed", { error: "load" }));
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => {
      alive = false;
    };
  }, [loadEvents, flashErr, t]);

  React.useEffect(() => {
    ProbeInterpreters()
      .then(setInterpreters)
      .catch(() => setInterpreters([]));
  }, []);

  const startCreate = () => {
    setSelectedId(null);
    setDraft(emptyDraft(t));
    setRunResult(null);
    setActiveTab("script");
  };

  const save = async () => {
    if (!draft) return;
    const payload = {
      name: draft.name,
      interpreter: draft.interpreter,
      interpreterPath: draft.interpreterPath,
      command: draft.command,
      scheduleExpr: draft.scheduleExpr,
      timezone: draft.timezone,
      env: draft.env,
      enabled: draft.enabled,
    };
    setBusy(true);
    try {
      let saved: HookItem;
      if (draft.id == null) {
        saved = await CreateHook(
          hook_svc.CreateHookRequest.createFrom(payload),
        );
        flashOk(t("hooks.flash.created", { name: saved.name }));
      } else {
        saved = await UpdateHook(
          hook_svc.UpdateHookRequest.createFrom({
            ...payload,
            id: draft.id,
          }),
        );
        flashOk(t("hooks.flash.updated", { name: saved.name }));
      }
      await reload(saved.id);
    } catch (err) {
      flashErr(t("hooks.flash.saveFailed", { error: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const toggle = async (hook: HookItem) => {
    setBusy(true);
    try {
      await ToggleHook(hook.id, !hook.enabled);
      flashOk(
        hook.enabled ? t("hooks.flash.disabled") : t("hooks.flash.enabled"),
      );
      await reload(hook.id);
    } catch (err) {
      flashErr(t("hooks.flash.saveFailed", { error: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleteTarget(null);
    setBusy(true);
    try {
      await DeleteHook(target.id);
      flashOk(t("hooks.flash.deleted", { name: target.name }));
      await reload(null);
    } catch (err) {
      flashErr(t("hooks.flash.saveFailed", { error: String(err) }));
    } finally {
      setBusy(false);
    }
  };

  const run = async () => {
    if (selectedId == null) return;
    setRunning(true);
    try {
      const result = await RunHook({
        id: selectedId,
        dryRun: true,
      });
      setRunResult(result);
      if (!runOk(result))
        flashErr(
          t("hooks.flash.runFailed", {
            error:
              result.parseError || result.stderr || `exit ${result.exitCode}`,
          }),
        );
    } catch (err) {
      flashErr(t("hooks.flash.runFailed", { error: String(err) }));
    } finally {
      setRunning(false);
    }
  };

  const filtered = hooks.filter((h) => hookMatchesQuery(h, query));
  const selectedHook = hooks.find((h) => h.id === selectedId) ?? null;
  const headerMeta = draft ? interpMeta(draft.interpreter) : null;

  return {
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
  };
}
