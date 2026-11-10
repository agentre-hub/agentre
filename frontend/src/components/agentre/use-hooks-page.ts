import * as React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

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
import { classifyAppError, type AppError } from "@/lib/app-error";

import { FailureBody } from "./failure-body";
import {
  draftFromHook,
  draftsEqual,
  emptyDraft,
  hookMatchesQuery,
  interpMeta,
  runOk,
  type Draft,
  type HookEventItem,
  type HookItem,
  type HookTab,
  type InterpreterOption,
  type RunHookResult,
} from "./hooks-page-model";

/** 列表首次取数的三态：失败是独立一态，不落到「还没有 Hook」的空态。 */
type LoadState = "loading" | "failed" | "ready";

/** 有未保存修改时被拦下的那次离开：切到另一条，或新建。 */
type PendingLeave = { kind: "select"; hook: HookItem } | { kind: "create" };

/**
 * 失败 toast 的正文：后端带码的那句人话当正文；机器原话只进默认折叠的「详情」。
 * 这个文件是 .ts，所以用 createElement 而不是 JSX。
 */
function failureDescription(err: AppError): React.ReactNode {
  if (!err.message && !err.detail) return undefined;
  return React.createElement(FailureBody, {
    message: err.message,
    detail: err.detail,
  });
}

/**
 * HooksPage 的取数与写入：列表 / 选中 / 草稿 / 运行结果各自的状态，加上保存、
 * 启停、删除、试运行四个动作。页面本身只剩装配与呈现。
 *
 * 草稿与已保存内容不同即为「脏」：脏态下切换 / 新建先问，运行键先保存再运行，
 * 启停只改那一条的启用状态。
 */
export function useHooksPage() {
  const { t } = useTranslation();
  const [hooks, setHooks] = React.useState<HookItem[]>([]);
  const [events, setEvents] = React.useState<HookEventItem[]>([]);
  const [selectedId, setSelectedId] = React.useState<number | null>(null);
  const [draft, setDraft] = React.useState<Draft | null>(null);
  const [activeTab, setActiveTab] = React.useState<HookTab>("script");
  const [loadState, setLoadState] = React.useState<LoadState>("loading");
  const [loadError, setLoadError] = React.useState<AppError | null>(null);
  const [loadAttempt, setLoadAttempt] = React.useState(0);
  const [pendingLeave, setPendingLeave] = React.useState<PendingLeave | null>(
    null,
  );
  const [busy, setBusy] = React.useState(false);
  const [running, setRunning] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const [runResult, setRunResult] = React.useState<RunHookResult | null>(null);
  const [selectedEventId, setSelectedEventId] = React.useState<number | null>(
    null,
  );
  const [deleteTarget, setDeleteTarget] = React.useState<HookItem | null>(null);
  const [interpreters, setInterpreters] = React.useState<InterpreterOption[]>(
    [],
  );

  const notifyFailure = (title: string, err: unknown) =>
    toast.error(title, {
      description: failureDescription(classifyAppError(err)),
    });

  const loadEvents = React.useCallback(async (hookId: number) => {
    try {
      const resp = await LoadHooks({ hookId, limit: 50 });
      setEvents(resp.events ?? []);
      setSelectedEventId(resp.events?.[0]?.id ?? null);
    } catch {
      setEvents([]);
    }
  }, []);

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
        setLoadState("ready");
      } catch (err) {
        if (!alive) return;
        setLoadError(classifyAppError(err));
        setLoadState("failed");
      }
    })();
    return () => {
      alive = false;
    };
  }, [loadEvents, loadAttempt]);

  const retryLoad = () => {
    setLoadState("loading");
    setLoadError(null);
    setLoadAttempt((n) => n + 1);
  };

  React.useEffect(() => {
    ProbeInterpreters()
      .then(setInterpreters)
      .catch(() => setInterpreters([]));
  }, []);

  const selectedHook = hooks.find((h) => h.id === selectedId) ?? null;
  // 新建的草稿没有已保存的那一份，拿刚点「＋」时的初始值作基准。
  const dirty =
    draft != null &&
    !draftsEqual(
      draft,
      selectedHook ? draftFromHook(selectedHook) : emptyDraft(t),
    );

  const leave = (target: PendingLeave) => {
    setRunResult(null);
    if (target.kind === "select") {
      setSelectedId(target.hook.id);
      setDraft(draftFromHook(target.hook));
      void loadEvents(target.hook.id);
      return;
    }
    setSelectedId(null);
    setDraft(emptyDraft(t));
    setActiveTab("script");
  };

  /** 离开当前草稿：没改过直接走，改过先问。 */
  const requestLeave = (target: PendingLeave) => {
    if (dirty) setPendingLeave(target);
    else leave(target);
  };

  const selectHook = (hook: HookItem) => {
    // 点的就是正在编辑的这一条：不是离开，草稿原样留着。
    if (dirty && hook.id === selectedId) return;
    requestLeave({ kind: "select", hook });
  };

  const startCreate = () => requestLeave({ kind: "create" });

  const confirmLeave = () => {
    if (!pendingLeave) return;
    leave(pendingLeave);
    setPendingLeave(null);
  };

  const cancelLeave = () => setPendingLeave(null);

  /** 保存草稿；成功返回落库后的 id，失败（已报错）返回 null。 */
  const save = async (): Promise<number | null> => {
    if (!draft) return null;
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
        toast.success(t("hooks.flash.created", { name: saved.name }));
      } else {
        saved = await UpdateHook(
          hook_svc.UpdateHookRequest.createFrom({
            ...payload,
            id: draft.id,
          }),
        );
        toast.success(t("hooks.flash.updated", { name: saved.name }));
      }
      await reload(saved.id);
      return saved.id;
    } catch (err) {
      notifyFailure(t("hooks.flash.saveFailed"), err);
      return null;
    } finally {
      setBusy(false);
    }
  };

  const toggle = async (hook: HookItem) => {
    setBusy(true);
    try {
      const updated = await ToggleHook(hook.id, !hook.enabled);
      // 只换这一条的已保存状态；草稿里除了启用位，一个字都不动。
      setHooks((list) => list.map((h) => (h.id === updated.id ? updated : h)));
      setDraft((d) =>
        d && d.id === updated.id ? { ...d, enabled: updated.enabled } : d,
      );
      toast.success(
        updated.enabled ? t("hooks.flash.enabled") : t("hooks.flash.disabled"),
      );
    } catch (err) {
      notifyFailure(
        hook.enabled
          ? t("hooks.flash.disableFailed")
          : t("hooks.flash.enableFailed"),
        err,
      );
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
    } catch (err) {
      notifyFailure(t("hooks.flash.deleteFailed"), err);
      setBusy(false);
      return;
    }
    toast.success(t("hooks.flash.deleted", { name: target.name }));
    try {
      await reload(null);
    } catch {
      // 删除已经成功；列表没刷出来不是「删除失败」，本地先把这一条拿掉。
      setHooks((list) => list.filter((h) => h.id !== target.id));
      if (selectedId === target.id) {
        setSelectedId(null);
        setDraft(null);
      }
    } finally {
      setBusy(false);
    }
  };

  /** 试运行。有未保存修改时先保存，保存成功才运行 —— 跑的就是眼前这份。 */
  const run = async () => {
    setRunning(true);
    try {
      const id = dirty ? await save() : selectedId;
      if (id == null) return;
      let result: RunHookResult;
      try {
        result = await RunHook({ id, dryRun: true });
      } catch (err) {
        notifyFailure(t("hooks.flash.runFailed"), err);
        return;
      }
      setRunResult(result);
      if (!runOk(result))
        toast.error(t("hooks.flash.runFailed"), {
          description: failureDescription({
            kind: "unknown",
            message: "",
            detail:
              result.parseError || result.stderr || `exit ${result.exitCode}`,
          }),
        });
    } finally {
      setRunning(false);
    }
  };

  const filtered = hooks.filter((h) => hookMatchesQuery(h, query));
  const headerMeta = draft ? interpMeta(draft.interpreter) : null;

  return {
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
  };
}
