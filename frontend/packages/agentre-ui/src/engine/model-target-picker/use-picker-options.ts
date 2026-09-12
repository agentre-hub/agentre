/**
 * ModelTargetPicker 的派生与交互：从目录到「此刻这个弹层该列哪几行」。
 *
 * 全部 memo 化不是为了省 CPU 而是为了**身份稳定**：`flatOptions` 参与
 * `handleOpenChange` / `handleKeyDown` 的依赖，每次 render 换一个新数组会让打开
 * 弹层时的「定位到当前选中项」时机随渲染次数漂移。
 *
 * `t` 由调用方传进来：它自己也是 hook 的产物，在这里再取一次会改变宿主组件的
 * hook 数量与顺序。
 */
import * as React from "react";
import { GitBranch, Lock, UserRound, type LucideIcon } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";

import { removeRecentTarget } from "./recents";
import {
  buildCatalogOptions,
  buildInvalidOption,
  buildRecentChips,
  buildSyncAnchors,
  filterFlatOptions,
  resolveTargetLabel,
} from "./options";
import {
  isNativeTarget,
  providerCompatibleForBackend,
  sameTarget,
  type ModelTarget,
  type PickerProvider,
  type PickerScenario,
} from "./types";

type TranslateFn = ReturnType<typeof useTranslation>["t"];

export interface UsePickerOptionsArgs {
  scenario: PickerScenario;
  backendType: string;
  executionLocation: string;
  selected: ModelTarget | null;
  onChange: (target: ModelTarget) => void;
  catalog: PickerProvider[];
  invalid: boolean;
  supportsFixedModel: boolean;
  remoteCatalog: PickerProvider[] | undefined;
  specialSublabel: React.ReactNode;
  onSyncProvider: ((provider: PickerProvider) => void) | undefined;
  openOnMount: boolean;
  t: TranslateFn;
}

export function usePickerOptions({
  scenario,
  backendType,
  executionLocation,
  selected,
  onChange,
  catalog,
  invalid,
  supportsFixedModel,
  remoteCatalog,
  specialSublabel,
  onSyncProvider,
  openOnMount,
  t,
}: UsePickerOptionsArgs) {
  const [open, setOpen] = React.useState(openOnMount);
  const [search, setSearch] = React.useState("");
  const [activeIndex, setActiveIndex] = React.useState(0);
  const [recentTick, setRecentTick] = React.useState(0);
  const searchRef = React.useRef<HTMLInputElement>(null);
  const listRef = React.useRef<HTMLUListElement>(null);

  const specialLabel = t(`modelTargetPicker.special.${scenario}`);
  // 特殊项图标分场景（mockup）：chat = 跟随 Agent（与 composer pill 同一枚人像），
  // backend = CLI 自身登录态（锁），route = 继承主绑定（分支）。
  const SpecialIcon: LucideIcon =
    scenario === "chat" ? UserRound : scenario === "backend" ? Lock : GitBranch;
  const specialResolution =
    specialSublabel ??
    (scenario === "backend"
      ? t("modelTargetPicker.special.backendSublabel")
      : undefined);

  const selectedLabel = React.useMemo(() => {
    if (!selected || isNativeTarget(selected)) return specialLabel;
    return resolveTargetLabel(selected, catalog);
  }, [catalog, selected, specialLabel]);

  // 兼容目录：按 effective backend type 过滤。
  const compatible = React.useMemo(
    () =>
      catalog.filter((p) => providerCompatibleForBackend(backendType, p.type)),
    [catalog, backendType],
  );

  // 行内禁用原因文案（t 派生）。
  const remoteSyncHint = t("modelTargetPicker.remoteSyncNeeded");
  const remoteFixedHint = t("modelTargetPicker.fixedModelUnsupported");
  const disabledModelHint = t("modelTargetPicker.disabledModel");
  const disabledProviderHint = t("modelTargetPicker.disabledProvider");
  const followDefaultLabel = t("modelTargetPicker.followDefault");
  const noDefaultModelLabel = t("modelTargetPicker.noDefaultModel");

  // remoteByKey：daemon 目录的 Provider/Model 存在性索引（task 6 决策 12）。
  // providerKey → provider（含其 models 的 modelKey 集合）。
  const remoteByKey = React.useMemo(() => {
    if (!remoteCatalog) return null;
    const m = new Map<string, PickerProvider>();
    for (const p of remoteCatalog) m.set(p.providerKey, p);
    return m;
  }, [remoteCatalog]);

  // remoteGated：远端执行 + 已知 daemon 目录 → 组头标注「设备上已有」、行内同步入口生效。
  const remoteGated = remoteByKey != null && executionLocation !== "";

  const recents = React.useMemo(() => {
    // recentTick 仅在移除 chip 后递增，强制重新读取 localStorage；读取仍走 localStorage
    // 而不是内存态，保证多实例/刷新后一致。
    void recentTick;
    return buildRecentChips({
      scenario,
      executionLocation,
      compatible,
      remoteByKey,
      supportsFixedModel,
      disabledProviderHint,
      disabledModelHint,
      remoteFixedHint,
      remoteSyncHint,
      defaultLabel: t("modelTargetPicker.defaultLabel"),
    });
  }, [
    compatible,
    executionLocation,
    scenario,
    recentTick,
    disabledProviderHint,
    disabledModelHint,
    remoteByKey,
    supportsFixedModel,
    remoteFixedHint,
    remoteSyncHint,
    t,
  ]);

  const catalogOptions = React.useMemo(
    () =>
      buildCatalogOptions({
        compatible,
        executionLocation,
        remoteByKey,
        supportsFixedModel,
        followDefaultLabel,
        noDefaultModelLabel,
        remoteSyncHint,
        remoteFixedHint,
        disabledProviderHint,
        disabledModelHint,
        defaultCurrentPrefix: t("modelTargetPicker.defaultCurrentPrefix"),
      }),
    [
      compatible,
      executionLocation,
      remoteByKey,
      supportsFixedModel,
      followDefaultLabel,
      noDefaultModelLabel,
      remoteSyncHint,
      remoteFixedHint,
      disabledProviderHint,
      disabledModelHint,
      t,
    ],
  );

  const invalidOption = React.useMemo(
    () => buildInvalidOption({ invalid, selected, catalog }),
    [invalid, selected, catalog],
  );

  const flatOptions = React.useMemo(
    () =>
      filterFlatOptions({
        search,
        specialLabel,
        specialResolution,
        invalidOption,
        catalogOptions,
      }),
    [search, specialLabel, specialResolution, invalidOption, catalogOptions],
  );

  const syncAnchorByProvider = React.useMemo(
    () =>
      buildSyncAnchors({
        flatOptions,
        hasSyncRoute: Boolean(onSyncProvider),
        remoteSyncHint,
      }),
    [flatOptions, onSyncProvider, remoteSyncHint],
  );

  const showChips = search.trim() === "" && recents.length > 0;

  const handleRemoveRecent = React.useCallback(
    (target: ModelTarget) => {
      removeRecentTarget(scenario, executionLocation, target);
      setRecentTick((x) => x + 1);
    },
    [scenario, executionLocation],
  );

  // 选中任意一项（列表行或最近使用 chip）都是「发射 target 并收起弹层」。
  const handlePick = React.useCallback(
    (target: ModelTarget) => {
      onChange(target);
      setOpen(false);
    },
    [onChange],
  );

  // 打开时重置搜索与活动索引（优先定位当前选中项）。
  const handleOpenChange = React.useCallback(
    (next: boolean) => {
      setOpen(next);
      if (next) {
        setSearch("");
        const idx = flatOptions.findIndex(
          (o) => !o.disabled && sameTarget(o.target, selected),
        );
        setActiveIndex(idx >= 0 ? idx : 0);
        setTimeout(() => searchRef.current?.focus(), 0);
      }
    },
    [flatOptions, selected],
  );

  // 键盘导航：↑↓ 移动、Enter 选中、Esc 清搜索/关闭。
  const handleKeyDown = React.useCallback(
    (e: React.KeyboardEvent) => {
      if (flatOptions.length === 0) return;
      if (e.key === "ArrowDown") {
        e.preventDefault();
        setActiveIndex((i) => Math.min(i + 1, flatOptions.length - 1));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setActiveIndex((i) => Math.max(i - 1, 0));
      } else if (e.key === "Enter") {
        e.preventDefault();
        const opt = flatOptions[activeIndex];
        if (opt && !opt.disabled) {
          onChange(opt.target);
          setOpen(false);
        }
      } else if (e.key === "Escape") {
        if (search) {
          e.preventDefault();
          setSearch("");
        } else {
          setOpen(false);
        }
      }
    },
    [flatOptions, activeIndex, onChange, search],
  );

  // 活动项滚入可视区。
  React.useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(
      `[data-option-index="${activeIndex}"]`,
    );
    el?.scrollIntoView({ block: "nearest" });
  }, [activeIndex]);

  return {
    open,
    search,
    setSearch,
    activeIndex,
    setActiveIndex,
    searchRef,
    listRef,
    SpecialIcon,
    selectedLabel,
    compatible,
    remoteByKey,
    remoteGated,
    recents,
    catalogOptions,
    flatOptions,
    syncAnchorByProvider,
    showChips,
    handleRemoveRecent,
    handlePick,
    handleOpenChange,
    handleKeyDown,
  };
}
