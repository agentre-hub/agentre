// 共享 ModelTargetPicker（spec「UI, accessibility and recent targets」）。
//
// 三处复用同一主体，只替换顶部特殊项（scenario）：
//   - backend：顶部特殊项 = CLI 自身登录态（native）；
//   - chat：顶部特殊项 = 跟随 Agent 绑定（inherit-agent）；
//   - route：顶部特殊项 = 继承主绑定（inherit-main）。
//
// 主体交互：搜索、最近使用（localStorage，按执行位置指纹隔离，单行横向可移除 chip）、
// Provider 分组（sticky 组头承载品牌标识 + 供应商名）、provider-default 首项（强调底色 +
// 动态图标 + 当前解析模型）、fixed-model 列表（display name + 上下文/最大输出）、
// 兼容性过滤（effective backend type）、loading/empty/error/invalid/remote 状态、
// 键盘导航（方向键 / Enter / Esc / focus ring）。
// 只通过 onChange 发射 providerKey/modelKey，绝不发射名称 / ModelID / 凭据。
import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  AlertTriangle,
  Check,
  ChevronDown,
  GitBranch,
  Loader2,
  Lock,
  Monitor,
  RefreshCw,
  Search,
  Upload,
  UserRound,
  X,
} from "lucide-react";

import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

import { LlmModelLogo, LlmProviderLogo } from "../ai-brand-logo";
import { formatTokens } from "../llm-provider-models";
import { readRecentTargets, removeRecentTarget } from "./recents";
import {
  isNativeTarget,
  providerCompatibleForBackend,
  sameTarget,
  type ModelTarget,
  type PickerProvider,
  type PickerScenario,
} from "./types";

// specialItemKey 顶部特殊项（native / inherit）在选项列表里的唯一 key。
const SPECIAL_ITEM_KEY = "__special__";

export type ModelTargetPickerProps = {
  scenario: PickerScenario;
  // backendType 是 effective backend 类型，用于 provider.type 兼容性过滤。
  backendType: string;
  // executionLocation 空串 = 本机；非空 = 远端设备 id（最近使用指纹隔离）。
  executionLocation?: string;
  selected: ModelTarget | null;
  onChange: (target: ModelTarget) => void;
  catalog: PickerProvider[];
  loading?: boolean;
  // error：模型目录拉取失败 → 弹层错误行。errorText 覆盖默认错误文案（chat 场景把
  // 持久化切换失败的真实信息透出来）。
  error?: boolean;
  errorText?: string;
  disabled?: boolean;
  // openOnMount：直达更换绑定等入口打开消费方时，同时展开选择器。
  openOnMount?: boolean;
  // invalid：当前选中的 target 在目录里解析不出来（Provider/Model 缺失/停用/被删）。
  invalid?: boolean;
  // remoteMissing：目标执行设备上缺少所选 Provider（远端场景提示，task 6 深化）。
  remoteMissing?: boolean;
  // supportsFixedModel：目标执行设备是否公布 llm-model-target-v1 能力位（task 6 决策 11）。
  // 远端且为 false 时禁用所有 fixed-model 选项，避免旧 daemon 静默降级为默认模型；
  // 本机 / 未传 → 默认 true（不限制）。
  supportsFixedModel?: boolean;
  // remoteCatalog：目标执行设备的 daemon 目录（task 6 决策 12，远端可运行事实源）。
  // 未传 = 本机（不启用远端门控）。传了以后，desktop 目录里在 daemon 上不存在的
  // Provider/Model 被禁用并标注「需同步」，绝不保存一个未经验证的远端目标。
  remoteCatalog?: PickerProvider[];
  // deviceLabel：目标执行设备的人读名字。传了以后弹层顶部说明「在该设备上执行、以该设备
  // 的配置为准」，并给设备上已有的供应商组打标记；未传（本机 / 消费方还没解析出名字）
  // 时整块不渲染。
  deviceLabel?: string;
  // specialSublabel：顶部特殊项的解析结果副行（chat/route 由消费方解析后传入，
  // 写出「供应商 · 模型」或「跟随该 Agent 绑定的供应商」）。backend 场景未传时
  // 回落「由 CLI 自身的登录账号决定」。可以是节点（带品牌标识的「→ [logo] 供应商 ·
  // 模型」）；节点只参与渲染，特殊项的搜索始终只按 specialLabel 匹配（见 Option.searchText）。
  specialSublabel?: React.ReactNode;
  // 远端目录缺少本机 Provider 时的显式同步入口；消费方负责确认与执行凭证复制。
  onSyncProvider?: (provider: PickerProvider) => void;
  // footer：弹层底部常显说明（chat 场景的「自下一轮生效」等），随弹层一起出现。
  footer?: React.ReactNode;
  // compact：表单内嵌（claude tier 路由）用小号触发按钮。
  compact?: boolean;
  align?: "start" | "end";
  className?: string;
  title?: string;
  // triggerLabel：覆盖触发按钮主行。chat 场景「未选但 agent 已绑 provider」时显示
  // 绑定供应商名，而不是顶部特殊项「跟随 Agent 绑定」；undefined 时按目录解析。
  // 可以是节点（backend 编辑器的主行 = 品牌标识 + 供应商名 + 跟随/固定徽标）；此时
  // 按钮的无障碍名改由 aria-label 决定，节点内容不参与名字计算（见 triggerAriaLabel）。
  triggerLabel?: React.ReactNode;
  // triggerSub：可选触发按钮副行。后端编辑器用它展示当前解析出的生效模型与模式后果；
  // 未传时保持 model-pill / chat 等既有消费方的单行形态。
  triggerSub?: React.ReactNode;
  "data-testid"?: string;
  "aria-label"?: string;
};

type Option = {
  key: string;
  kind: "special" | "invalid" | "provider-default" | "fixed";
  label: string;
  // sublabel 只用于渲染，可以是节点（特殊项的解析结果带品牌标识）；搜索一律走
  // searchText，否则节点会被拼成 "[object Object]" 混进匹配。
  sublabel?: React.ReactNode;
  // searchText 该项参与搜索匹配的纯文本（渲染与匹配彻底分开）。
  searchText: string;
  target: ModelTarget;
  disabled: boolean;
  group?: string;
  groupType?: string;
  // disabledHint 不可选项的行内原因：模型停用 / 供应商停用 / 远端需同步 / 不支持固定模型。
  // 有原因时它取代副行（mockup 的停用行是「模型名 / 原因」两行，不叠第三行）。
  disabledHint?: string;
  // fixed 行专属：contextWindow/maxOutput 供右侧展示（品牌标识由 sticky 组头承载，
  // 行内不再重复）。
  contextWindow?: number;
  maxOutput?: number;
};

type RecentChip = {
  key: string;
  // kind 决定 chip 上的品牌标识取法：fixed 按 modelId 判定，provider-default 按供应商。
  kind: "fixed" | "provider-default";
  label: string;
  providerType: string;
  providerName: string;
  target: ModelTarget;
  disabled: boolean;
  title?: string;
};

// resolveTargetLabel 把 target 解析成人读摘要（供应商 · 模型）；目录里解析不出来时
// 回落原始 providerKey/modelKey。供触发按钮、失效警示与失效保留项共用。
function resolveTargetLabel(
  target: ModelTarget,
  catalog: PickerProvider[],
): string {
  const p = catalog.find((x) => x.providerKey === target.providerKey);
  const providerLabel = p?.name ?? target.providerKey;
  if (!target.modelKey) {
    return p?.defaultModel
      ? `${providerLabel} · ${p.defaultModel.modelId}`
      : providerLabel;
  }
  const m = p?.models.find((x) => x.modelKey === target.modelKey);
  const modelLabel = m ? m.name || m.modelId : target.modelKey;
  return `${providerLabel} · ${modelLabel}`;
}

export function ModelTargetPicker({
  scenario,
  backendType,
  executionLocation = "",
  selected,
  onChange,
  catalog,
  loading = false,
  error = false,
  errorText,
  disabled = false,
  openOnMount = false,
  invalid = false,
  remoteMissing = false,
  supportsFixedModel = true,
  remoteCatalog,
  deviceLabel,
  specialSublabel,
  onSyncProvider,
  footer,
  compact = false,
  align = "start",
  className,
  title,
  triggerLabel,
  triggerSub,
  "data-testid": testId,
  "aria-label": ariaLabel,
}: ModelTargetPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(openOnMount);
  const [search, setSearch] = React.useState("");
  const [activeIndex, setActiveIndex] = React.useState(0);
  const [recentTick, setRecentTick] = React.useState(0);
  const searchRef = React.useRef<HTMLInputElement>(null);
  const listRef = React.useRef<HTMLUListElement>(null);

  const specialLabel = t(`modelTargetPicker.special.${scenario}`);
  // 特殊项图标分场景（mockup）：chat = 跟随 Agent（与 composer pill 同一枚人像），
  // backend = CLI 自身登录态（锁），route = 继承主绑定（分支）。
  const SpecialIcon =
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

  // 最近使用（按执行位置指纹隔离）。只展示当前 backend 兼容的项；失效项禁用并给原因。
  const recents = React.useMemo(() => {
    // recentTick 仅在移除 chip 后递增，强制重新读取 localStorage；读取仍走 localStorage
    // 而不是内存态，保证多实例/刷新后一致。
    void recentTick;
    const all = readRecentTargets(scenario, executionLocation);
    const seen = new Set<string>();
    const out: RecentChip[] = [];
    for (const r of all) {
      const p = compatible.find((x) => x.providerKey === r.providerKey);
      if (!p) continue; // 当前 backend 不兼容 → 隐藏
      // 失效判定：仅当 recent 的固定模型已不存在/停用，或 provider 停用时才禁用。
      // 固定模型是不是 Provider 当前默认模型不影响其可选择性 —— 非默认的 fixed-model
      // recent 必须仍可一键重选（recent 存在的意义），不能因为不是默认就被禁用。
      const modelOk =
        r.modelKey === "" ||
        p.models.some((m) => m.modelKey === r.modelKey && m.enabled);
      const target: ModelTarget = {
        providerKey: r.providerKey,
        modelKey: r.modelKey,
      };
      const remoteProvider =
        executionLocation !== "" ? remoteByKey?.get(p.providerKey) : undefined;
      const remoteTargetOk =
        remoteByKey == null || executionLocation === ""
          ? true
          : target.modelKey === ""
            ? remoteProvider?.defaultModel?.modelKey ===
              p.defaultModel?.modelKey
            : supportsFixedModel &&
              remoteProvider?.models.some(
                (model) => model.modelKey === target.modelKey && model.enabled,
              ) === true;
      const dedupeKey = `${target.providerKey}\u0000${target.modelKey}`;
      if (seen.has(dedupeKey)) continue;
      seen.add(dedupeKey);
      const label = target.modelKey
        ? (p.models.find((m) => m.modelKey === target.modelKey)?.modelId ??
          target.modelKey)
        : p.name;
      out.push({
        key: `recent-${dedupeKey}`,
        kind: target.modelKey ? "fixed" : "provider-default",
        label,
        providerType: p.type,
        providerName: p.name,
        target,
        disabled: !p.enabled || !modelOk || !remoteTargetOk,
        title: !p.enabled
          ? disabledProviderHint
          : !modelOk
            ? disabledModelHint
            : !remoteTargetOk
              ? !supportsFixedModel && target.modelKey
                ? remoteFixedHint
                : remoteSyncHint
              : target.modelKey
                ? undefined
                : t("modelTargetPicker.defaultLabel"),
      });
    }
    return out.slice(0, 5);
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

  // 目录选项（provider-default 首项，再 fixed-model 列表）。
  const catalogOptions = React.useMemo(() => {
    const out: Option[] = [];
    for (const p of compatible) {
      const groupLabel = p.name;
      // 远端门控：daemon 上没有该 Provider → 本机独有，需同步后才能选。
      const remoteProvider =
        remoteByKey && executionLocation
          ? remoteByKey.get(p.providerKey)
          : undefined;
      const providerSyncNeeded =
        remoteByKey != null && executionLocation !== "" && !remoteProvider;
      // provider-default 的运行语义由 Provider 当前 defaultModelKey 决定；远端目录里的
      // 默认 key 不一致时，即使同名 Provider 已存在也必须重新同步后才能保存。
      const defaultModel = p.defaultModel;
      const defaultSyncNeeded =
        remoteByKey != null &&
        executionLocation !== "" &&
        remoteProvider != null &&
        remoteProvider.defaultModel?.modelKey !== defaultModel?.modelKey;
      out.push({
        key: `default-${p.providerKey}`,
        kind: "provider-default",
        group: groupLabel,
        groupType: p.type,
        label: followDefaultLabel,
        // 副行 = mockup .opt.dyn 的「当前 <mono>模型标识</mono>」：只回答「现在跑哪个
        // 模型」，不带后果从句；等宽只包标识符，人读前缀不跟着走等宽。
        sublabel: defaultModel ? (
          <>
            {t("modelTargetPicker.defaultCurrentPrefix")}{" "}
            <span className="break-all font-mono">{defaultModel.modelId}</span>
          </>
        ) : (
          noDefaultModelLabel
        ),
        searchText: `${followDefaultLabel} ${defaultModel?.modelId ?? ""}`,
        target: { providerKey: p.providerKey, modelKey: "" },
        disabled: !p.enabled || providerSyncNeeded || defaultSyncNeeded,
        disabledHint:
          providerSyncNeeded || defaultSyncNeeded
            ? remoteSyncHint
            : !p.enabled
              ? disabledProviderHint
              : undefined,
      });
      // fixed-model 列表。
      for (const m of p.models) {
        // 当前默认模型仍需作为 fixed-model 候选保留：provider-default 表达动态跟随，
        // 而选择这里的同一 ModelKey 表达锁定当前模型、以后不随 Provider 默认变化。
        // 远端门控：模型在 daemon 上不存在 / 停用 → 需同步；daemon 不支持
        // fixed-model（旧协议）→ 一律禁用，绝不静默降级。
        const remoteModelOk =
          !remoteProvider ||
          remoteProvider.models.some(
            (rm) => rm.modelKey === m.modelKey && rm.enabled,
          );
        const fixedUnsupported =
          remoteByKey != null &&
          executionLocation !== "" &&
          !supportsFixedModel;
        const fixedSyncNeeded =
          remoteByKey != null &&
          executionLocation !== "" &&
          remoteProvider != null &&
          !remoteModelOk;
        out.push({
          key: `fixed-${p.providerKey}-${m.modelKey}`,
          kind: "fixed",
          group: groupLabel,
          groupType: p.type,
          label: m.name || m.modelId,
          sublabel: m.modelId,
          searchText: `${m.name || m.modelId} ${m.modelId}`,
          contextWindow: m.contextWindow,
          maxOutput: m.maxOutput,
          target: { providerKey: p.providerKey, modelKey: m.modelKey },
          disabled:
            !p.enabled ||
            !m.enabled ||
            providerSyncNeeded ||
            fixedUnsupported ||
            fixedSyncNeeded,
          disabledHint: fixedUnsupported
            ? remoteFixedHint
            : providerSyncNeeded || fixedSyncNeeded
              ? remoteSyncHint
              : !p.enabled
                ? disabledProviderHint
                : !m.enabled
                  ? disabledModelHint
                  : undefined,
        });
      }
    }
    return out;
  }, [
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
  ]);

  // 失效目标：以禁用项保留在列表顶部（不被清除），目标本身即当前 selected。
  const invalidOption = React.useMemo((): Option | null => {
    if (!invalid || !selected || isNativeTarget(selected)) return null;
    const label = resolveTargetLabel(selected, catalog);
    return {
      key: "invalid-selected",
      kind: "invalid",
      label,
      searchText: label,
      target: selected,
      disabled: true,
    };
  }, [invalid, selected, catalog]);

  // 搜索过滤：匹配 provider 名 / model id / 特殊项。chips（最近使用）只在未搜索时显示。
  const flatOptions = React.useMemo(() => {
    // 特殊项（native / inherit）内联到 memo 内，避免每渲染新对象身份成为依赖。
    const specialOption: Option = {
      key: SPECIAL_ITEM_KEY,
      kind: "special",
      label: specialLabel,
      // 副行可能是节点（带品牌标识的解析结果）；搜索只按标题匹配。
      sublabel: specialResolution,
      searchText: specialLabel,
      target: { providerKey: "", modelKey: "" },
      disabled: false,
    };
    const q = search.trim().toLowerCase();
    const base: Option[] = [];
    if (invalidOption) base.push(invalidOption);
    if (q === "") {
      base.push(specialOption);
    } else if (specialLabel.toLowerCase().includes(q)) {
      base.push(specialOption);
    }
    base.push(...catalogOptions);
    if (q === "") return base;
    const match = (o: Option) => o.searchText.toLowerCase().includes(q);
    return base.filter(match);
  }, [search, specialLabel, specialResolution, invalidOption, catalogOptions]);

  // 同步入口落点：每个「本机独有 / 目录过期」的供应商，只在它第一条待修复的行内挂一个
  // 「同步过去」，而不是在列表底部聚合。没有真实同步路由（未传 onSyncProvider）时一个都不挂。
  const syncAnchorByProvider = React.useMemo(() => {
    const anchors = new Map<string, string>();
    if (!onSyncProvider) return anchors;
    for (const o of flatOptions) {
      if (o.disabledHint !== remoteSyncHint) continue;
      if (anchors.has(o.target.providerKey)) continue;
      anchors.set(o.target.providerKey, o.key);
    }
    return anchors;
  }, [flatOptions, onSyncProvider, remoteSyncHint]);

  const showChips = search.trim() === "" && recents.length > 0;

  const handleRemoveRecent = React.useCallback(
    (target: ModelTarget) => {
      removeRecentTarget(scenario, executionLocation, target);
      setRecentTick((x) => x + 1);
    },
    [scenario, executionLocation],
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

  const triggerText = triggerLabel ?? selectedLabel;
  // 主行是纯文本时按钮可以继续靠内容拿无障碍名（既有消费方形态不变）；主行是节点时
  // 内容里有品牌标识（role=img，自带品牌名）和模式徽标，让它们参与名字计算会念出
  // 重复且不稳定的名字 —— 所以名字只认显式字符串：aria-label 优先，其次目录解析结果。
  const isTextTriggerLabel =
    typeof triggerText === "string" || typeof triggerText === "number";
  const triggerAriaLabel =
    ariaLabel ?? (isTextTriggerLabel ? undefined : selectedLabel);

  // 最近使用横条：紧跟在顶部特殊项之后、供应商分组之前（mockup 的列表次序），
  // 自带「最近使用」标签，单行横向可移除 chip，不占竖列表位。
  const recentChipsRow = showChips ? (
    <li role="none">
      {/* mockup .recents：7px 8px 5px / gap 5px / 单行横滑且不显示滚动条。 */}
      <div
        data-testid="recent-chips"
        className="flex flex-nowrap items-center gap-[5px] overflow-x-auto px-2 pb-[5px] pt-[7px] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        <span className="shrink-0 pr-px text-[10px] uppercase tracking-[0.06em] text-muted-foreground">
          {t("modelTargetPicker.recentLabel")}
        </span>
        {recents.map((r) => (
          <span
            key={r.key}
            className={cn(
              "flex h-[22px] shrink-0 items-center gap-1 rounded-md border border-border bg-card pl-[5px] pr-1 text-2xs transition-colors",
              // mockup .rchip：整颗 chip 是手形 + hover 只加深描边；停用的不给可点暗示。
              r.disabled
                ? "cursor-not-allowed"
                : "cursor-pointer hover:border-border-strong",
            )}
          >
            <button
              type="button"
              disabled={r.disabled}
              title={r.title}
              className="flex max-w-[10rem] cursor-pointer items-center gap-1 font-mono text-foreground disabled:cursor-not-allowed disabled:opacity-50"
              onClick={() => {
                if (r.disabled) return;
                onChange(r.target);
                setOpen(false);
              }}
            >
              {/* 品牌标识只做视觉，绝不能进按钮的无障碍名（logo 自带 role=img + 品牌名）。 */}
              <span aria-hidden="true" className="flex shrink-0">
                {r.kind === "fixed" ? (
                  <LlmModelLogo
                    model={r.label}
                    providerType={r.providerType}
                    providerName={r.providerName}
                    className="size-3.5"
                  />
                ) : (
                  <LlmProviderLogo
                    providerType={r.providerType}
                    providerName={r.providerName}
                    className="size-3.5"
                  />
                )}
              </span>
              <span className="truncate">{r.label}</span>
            </button>
            <button
              type="button"
              aria-label={t("modelTargetPicker.removeRecent", {
                label: r.label,
              })}
              className="shrink-0 cursor-pointer rounded p-0.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
              onClick={() => handleRemoveRecent(r.target)}
            >
              <X className="size-3" aria-hidden="true" />
            </button>
          </span>
        ))}
      </div>
    </li>
  ) : null;

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <button
          type="button"
          disabled={disabled}
          data-testid={testId}
          title={title}
          aria-label={triggerAriaLabel}
          aria-expanded={open}
          aria-haspopup="listbox"
          className={cn(
            // mockup .trigger：input 描边 + input-bg 底 + 8px 圆角 + 手形光标。
            "inline-flex w-full cursor-pointer items-center justify-between gap-2 rounded-lg border border-input bg-input-bg text-xs transition-colors",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
            "disabled:cursor-not-allowed disabled:opacity-60",
            triggerSub
              ? compact
                ? "min-h-9 px-2 py-1"
                : "min-h-11 px-2.5 py-1.5"
              : compact
                ? "h-7 px-2"
                : "h-9 px-2.5",
            invalid
              ? "border-status-waiting bg-status-waiting-bg"
              : "hover:bg-secondary/60",
            className,
          )}
        >
          <span className="flex min-w-0 items-center gap-1.5">
            {invalid ? (
              <AlertTriangle
                className={cn("size-3.5 shrink-0 text-status-waiting")}
                aria-hidden="true"
              />
            ) : null}
            <span className="flex min-w-0 flex-col items-start gap-0.5">
              <span
                className={cn(
                  "max-w-full",
                  // 节点主行自己是一排图标 + 文字 + 徽标，得撑成 flex 行；纯文本主行
                  // 保持既有的单行截断。
                  isTextTriggerLabel
                    ? "truncate"
                    : "flex min-w-0 items-center gap-1.5",
                  invalid ? "text-status-waiting" : "text-foreground",
                )}
              >
                {triggerText}
              </span>
              {triggerSub ? (
                <span
                  data-testid="model-target-trigger-sub"
                  className="max-w-full truncate text-2xs text-muted-foreground"
                >
                  {triggerSub}
                </span>
              ) : null}
            </span>
          </span>
          <ChevronDown
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align={align}
        side="bottom"
        sideOffset={6}
        data-testid="model-target-popover"
        // 348px / 10px 圆角 = mockup .pop（index.html data-view="picker"）；overflow-hidden
        // 让搜索区与 foot 的分隔线贴着圆角收口。min(...) 在极窄视口兜底收缩，
        // 不会把弹层推出屏幕（860×640 最小窗口下恒等于 348px）。
        className="w-[min(348px,calc(100vw-2rem))] overflow-hidden rounded-[10px] p-0"
        onKeyDown={handleKeyDown}
      >
        {/* 失效目标顶部警示（弹层内上方，不是底部 footer）。 */}
        {invalid ? (
          // mockup 把警示做成 .psearch 里的一张圆角 .banner.warn，不是通栏横条。
          <div className="border-b border-border p-2">
            <div
              data-testid="invalid-banner"
              className="flex items-start gap-2 rounded-lg bg-status-waiting-bg px-2.5 py-2 text-2xs text-status-waiting"
            >
              <AlertTriangle
                className="mt-px size-3.5 shrink-0"
                aria-hidden="true"
              />
              <span>
                <strong className="font-semibold">
                  {t("modelTargetPicker.invalidTitle")}
                </strong>
                <br />
                {t("modelTargetPicker.invalidTarget", {
                  target: selected ? resolveTargetLabel(selected, catalog) : "",
                })}
              </span>
            </div>
          </div>
        ) : null}

        {/* 远端场景：说明以目标设备的配置为准（未传设备名 = 本机，不渲染）。 */}
        {deviceLabel ? (
          <div
            data-testid="remote-device-header"
            className="flex items-center gap-1.5 border-b border-border px-3 py-2 text-2xs text-muted-foreground"
          >
            <Monitor className="size-3.5 shrink-0" aria-hidden="true" />
            <span className="truncate">
              {t("modelTargetPicker.remoteDeviceHeader", {
                device: deviceLabel,
              })}
            </span>
          </div>
        ) : null}

        {/* 搜索框（mockup .psearch > .search）：有边框的输入框，放大镜绝对定位在框内左侧。 */}
        <div className="border-b border-border p-2">
          <div className="relative">
            <Search
              className="pointer-events-none absolute left-[9px] top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              ref={searchRef}
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                setActiveIndex(0);
              }}
              placeholder={t("modelTargetPicker.searchPlaceholder")}
              className="h-8 rounded-[7px] bg-input-bg pl-7 pr-7 text-xs dark:bg-input-bg"
            />
            {search ? (
              <button
                type="button"
                aria-label={t("modelTargetPicker.clearSearch")}
                className="absolute right-1.5 top-1/2 shrink-0 -translate-y-1/2 cursor-pointer rounded p-0.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                onClick={() => {
                  setSearch("");
                  searchRef.current?.focus();
                }}
              >
                <X className="size-3.5" aria-hidden="true" />
              </button>
            ) : null}
          </div>
        </div>

        {/* 330px / 5px = mockup .pop .plistw。 */}
        <div className="max-h-[330px] overflow-y-auto p-[5px]">
          {loading ? (
            <div className="flex items-center gap-2 px-2.5 py-3 text-xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              {t("modelTargetPicker.loading")}
            </div>
          ) : error ? (
            <div className="px-2.5 py-3 text-xs text-status-waiting">
              {errorText ?? t("modelTargetPicker.error")}
            </div>
          ) : catalog.length === 0 && isNativeTarget(selected) ? (
            <div className="px-2.5 py-3 text-xs text-muted-foreground">
              {t("modelTargetPicker.empty")}
            </div>
          ) : catalog.length > 0 && compatible.length === 0 ? (
            <div className="px-2.5 py-3 text-xs text-muted-foreground">
              {t("modelTargetPicker.noCompatibleProviders")}
            </div>
          ) : null}

          {!loading && !error && flatOptions.length > 0 ? (
            <ul
              ref={listRef}
              role="listbox"
              aria-label={ariaLabel}
              className="flex flex-col gap-0.5"
            >
              {flatOptions.map((opt, i) => {
                const active = i === activeIndex;
                const selectedNow = sameTarget(opt.target, selected);
                const showGroup =
                  opt.group &&
                  (i === 0 || flatOptions[i - 1].group !== opt.group);
                const showFixedSection =
                  opt.kind === "fixed" &&
                  (i === 0 ||
                    flatOptions[i - 1].kind !== "fixed" ||
                    flatOptions[i - 1].group !== opt.group);
                // 远端：组头标注该供应商在目标设备上已存在（不代表模型齐全，行内另有原因）。
                const groupOnDevice =
                  remoteGated && remoteByKey.has(opt.target.providerKey);
                // 行内原因取代副行，让不可选行收敛成「名字 / 原因」两行。
                const inlineHint =
                  opt.kind === "invalid"
                    ? t("modelTargetPicker.invalidCurrent")
                    : opt.disabled
                      ? opt.disabledHint
                      : undefined;
                // 行内同步入口：只挂在该供应商第一条待同步的行上。
                const syncProvider =
                  syncAnchorByProvider.get(opt.target.providerKey) === opt.key
                    ? compatible.find(
                        (p) => p.providerKey === opt.target.providerKey,
                      )
                    : undefined;
                return (
                  <React.Fragment key={opt.key}>
                    <li role="none">
                      {showGroup ? (
                        <div
                          data-testid="picker-group"
                          data-provider-key={opt.target.providerKey}
                          // 底色必须是 popover：bg-background 在暗色下比弹层更黑，
                          // 会在列表里拉出一条黑带（mockup .pgrp { background: var(--popover) }）。
                          className="sticky top-0 z-10 flex items-center gap-1.5 bg-popover px-2 pb-1 pt-[9px] text-[10px] font-semibold uppercase tracking-[0.06em] text-muted-foreground"
                        >
                          {opt.groupType ? (
                            <LlmProviderLogo
                              providerType={opt.groupType}
                              providerName={opt.group}
                              className="size-3.5"
                            />
                          ) : null}
                          <span>{opt.group}</span>
                          {groupOnDevice ? (
                            <span className="ml-auto rounded-full bg-status-running-bg px-1.5 py-0.5 text-2xs font-medium normal-case tracking-normal text-status-running">
                              {t("modelTargetPicker.providerOnDevice")}
                            </span>
                          ) : null}
                        </div>
                      ) : null}
                      {/* mockup .fixhdr：6px 8px 2px / 10px / 不加粗。 */}
                      {showFixedSection ? (
                        <div className="px-2 pb-0.5 pt-1.5 text-[10px] text-muted-foreground">
                          {t("modelTargetPicker.fixedSection")}
                        </div>
                      ) : null}
                      <div className="flex items-center gap-1">
                        <button
                          type="button"
                          role="option"
                          aria-selected={selectedNow}
                          aria-disabled={opt.disabled}
                          data-option-index={i}
                          data-kind={opt.kind}
                          disabled={opt.disabled}
                          title={
                            opt.kind === "invalid"
                              ? t("modelTargetPicker.invalidHint")
                              : opt.disabledHint
                          }
                          onMouseEnter={() => setActiveIndex(i)}
                          onClick={() => {
                            if (opt.disabled) return;
                            onChange(opt.target);
                            setOpen(false);
                          }}
                          className={cn(
                            // mockup .opt：7px 圆角 / 6px 8px padding / 手形光标。
                            "flex min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-[7px] px-2 py-1.5 text-left text-xs transition-[background-color,filter]",
                            // 焦点环只表达「键盘焦点在这」，不兼任常态 hover。
                            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
                            // 核心语义分歧：跟随默认（动态）用 primary-soft 强调底，
                            // 固定到具体模型保持中性底，两态一眼可分。
                            opt.kind === "provider-default"
                              ? cn(
                                  // mockup .opt.dyn:hover 只改 filter：底色不换、不画边，
                                  // 靠提亮/压暗表达 hover 与键盘活动态。
                                  "mb-[3px] bg-primary-soft",
                                  !opt.disabled &&
                                    "hover:brightness-95 dark:hover:brightness-125",
                                  !opt.disabled &&
                                    active &&
                                    "brightness-95 dark:brightness-125",
                                )
                              : opt.kind === "invalid"
                                ? "border border-dashed border-status-waiting"
                                : // mockup .opt:hover 是满色 accent；.opt.dis:hover 保持透明；
                                  // .opt.sel 排在 .opt:hover 之后 → 选中项 hover 不翻 accent。
                                  !opt.disabled &&
                                  !selectedNow &&
                                  (active ? "bg-accent" : "hover:bg-accent"),
                            // 顶部特殊项是独立描边卡片（mockup .opt.special）：比普通行
                            // 重一档，选中时描边转实线 primary。
                            opt.kind === "special" &&
                              cn(
                                "mb-1 border border-dashed p-2",
                                selectedNow
                                  ? "border-solid border-primary"
                                  : "border-border-strong",
                              ),
                            // 选中项带底色且盖过 hover（mockup .opt.sel 排在 .opt:hover 之后）。
                            //
                            // ⚠️ 与 mockup 字面的有意偏离：mockup 给 .opt.sel 用的是
                            // primary-soft，但这里改成中性的 accent。primary-soft 在这颗
                            // 弹层里是「动态跟随供应商默认」的语义编码（.opt.dyn 常态就是
                            // 蓝底），选中态不能把这层含义借走 —— 否则「选中的固定模型」和
                            // 「跟随默认」长成一样，只剩 ✓ 能区分，正好抹掉整套设计要表达的
                            // 核心分歧。跟随默认行因此**不**套 accent：它选中与否都是蓝底，
                            // 由 ✓ 表示选中。别为了「对齐 mockup」把这里改回 primary-soft。
                            selectedNow &&
                              (opt.kind === "provider-default"
                                ? "bg-primary-soft"
                                : "bg-accent"),
                            "disabled:cursor-not-allowed disabled:opacity-50",
                          )}
                        >
                          <span className="flex min-w-0 flex-1 items-center gap-2">
                            {opt.kind === "special" ? (
                              <SpecialIcon
                                data-testid="special-icon"
                                data-scenario={scenario}
                                className="size-3.5 shrink-0 text-muted-foreground"
                                aria-hidden="true"
                              />
                            ) : opt.kind === "invalid" ? (
                              <AlertTriangle
                                className="size-3.5 shrink-0 text-status-waiting"
                                aria-hidden="true"
                              />
                            ) : opt.kind === "provider-default" ? (
                              <RefreshCw
                                data-testid="provider-default-icon"
                                className="size-3.5 shrink-0 text-primary-text"
                                aria-hidden="true"
                              />
                            ) : null}
                            <span className="flex min-w-0 flex-1 flex-col">
                              <span
                                className={cn(
                                  // mockup .opt .o1 一律 500 字重。
                                  "truncate font-medium",
                                  opt.kind === "provider-default"
                                    ? "text-primary-text"
                                    : "text-foreground",
                                )}
                              >
                                {opt.label}
                              </span>
                              {/* 停用行是「名字 / 原因」两行（mockup .opt.dis）：原因
                                  取代副行，不在模型标识之下再叠第三行。 */}
                              {inlineHint ? (
                                <span className="truncate text-2xs text-status-waiting">
                                  {inlineHint}
                                </span>
                              ) : opt.sublabel ? (
                                <span
                                  className={cn(
                                    "text-2xs text-muted-foreground",
                                    // 等宽只给「副行整条就是一个标识符」的 fixed 行；
                                    // 跟随默认 / 特殊项的副行是人读文案，标识符由副行
                                    // 自己包 mono。跟随默认行宁可折行也不许截断。
                                    opt.kind === "provider-default"
                                      ? "break-all"
                                      : opt.kind === "fixed"
                                        ? "truncate font-mono"
                                        : "truncate",
                                  )}
                                >
                                  {opt.sublabel}
                                </span>
                              ) : null}
                            </span>
                            {opt.kind === "invalid" ? (
                              <span className="shrink-0 rounded-full bg-status-waiting-bg px-1.5 py-0.5 text-2xs font-medium text-status-waiting">
                                {t("modelTargetPicker.invalidChip")}
                              </span>
                            ) : null}
                            {opt.kind === "fixed" &&
                            (opt.contextWindow || opt.maxOutput) ? (
                              <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                                {t("modelTargetPicker.contextOutput", {
                                  ctx: formatTokens(opt.contextWindow ?? 0),
                                  out: formatTokens(opt.maxOutput ?? 0),
                                })}
                              </span>
                            ) : null}
                          </span>
                          {selectedNow ? (
                            <Check
                              className="size-3.5 shrink-0 text-primary-text"
                              aria-hidden="true"
                            />
                          ) : null}
                        </button>
                        {/* 同步入口就放在它会修复的那一行内（消费方负责确认与凭证复制）。 */}
                        {syncProvider && onSyncProvider ? (
                          <button
                            type="button"
                            className="inline-flex shrink-0 cursor-pointer items-center gap-1 rounded-md border border-border px-1.5 py-1 text-2xs text-primary-text transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                            aria-label={t(
                              "modelTargetPicker.syncProviderNamed",
                              {
                                provider: syncProvider.name,
                              },
                            )}
                            onClick={() => onSyncProvider(syncProvider)}
                          >
                            <Upload className="size-3" aria-hidden="true" />
                            {t("modelTargetPicker.syncInline")}
                          </button>
                        ) : null}
                      </div>
                    </li>
                    {opt.kind === "special" ? recentChipsRow : null}
                  </React.Fragment>
                );
              })}
            </ul>
          ) : null}
        </div>

        {/* 灰色项的原因每一行自己已经写了，底部不再总述一遍；这里只承诺同步的后果
            （mockup 远端弹层的 foot 也只有这一句）。 */}
        {remoteGated &&
        onSyncProvider &&
        catalogOptions.some((o) => o.disabledHint) ? (
          <div className="border-t border-border bg-card px-2.5 py-[7px] text-2xs text-muted-foreground">
            {t("modelTargetPicker.syncFootnote")}
          </div>
        ) : null}
        {remoteMissing ? (
          <div className="border-t border-border bg-card px-2.5 py-[7px] text-2xs text-muted-foreground">
            {t("modelTargetPicker.remoteMissing")}
          </div>
        ) : null}
        {footer ? (
          <div className="border-t border-border bg-card px-2.5 py-[7px] text-2xs text-muted-foreground">
            {footer}
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}

export {
  readRecentTargets,
  recordRecentTarget,
  removeRecentTarget,
} from "./recents";
export type {
  ModelTarget,
  PickerModel,
  PickerProvider,
  PickerScenario,
} from "./types";
export { buildPickerCatalog, useModelTargetCatalog } from "./catalog";
export {
  isNativeTarget,
  providerCompatibleForBackend,
  sameTarget,
} from "./types";
