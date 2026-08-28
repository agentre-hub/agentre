// Picker 的选项装配：最近使用 chip、目录选项（provider-default 首项 + fixed-model
// 列表）、失效保留项、搜索过滤与同步入口落点。
//
// 这一层是纯函数：不读 React 状态、不发事件，禁用与否和禁用原因全部由入参决定 ——
// 远端门控（daemon 目录 / 能力位）的判定规则集中在这里，index.tsx 只负责把 memo
// 的依赖钉好再调用。文案一律以已解析的字符串传入，避免这层再持有 i18n。
import type { ReactNode } from "react";

import { readRecentTargets } from "./recents";
import {
  isNativeTarget,
  type ModelTarget,
  type PickerProvider,
  type PickerScenario,
} from "./types";

// specialItemKey 顶部特殊项（native / inherit）在选项列表里的唯一 key。
export const SPECIAL_ITEM_KEY = "__special__";

export type Option = {
  key: string;
  kind: "special" | "invalid" | "provider-default" | "fixed";
  label: string;
  // sublabel 只用于渲染，可以是节点（特殊项的解析结果带品牌标识）；搜索一律走
  // searchText，否则节点会被拼成 "[object Object]" 混进匹配。
  sublabel?: ReactNode;
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

export type RecentChip = {
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
export function resolveTargetLabel(
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

// 最近使用（按执行位置指纹隔离）。只展示当前 backend 兼容的项；失效项禁用并给原因。
export function buildRecentChips(args: {
  scenario: PickerScenario;
  executionLocation: string;
  compatible: PickerProvider[];
  remoteByKey: Map<string, PickerProvider> | null;
  supportsFixedModel: boolean;
  disabledProviderHint: string;
  disabledModelHint: string;
  remoteFixedHint: string;
  remoteSyncHint: string;
  defaultLabel: string;
}): RecentChip[] {
  const {
    scenario,
    executionLocation,
    compatible,
    remoteByKey,
    supportsFixedModel,
    disabledProviderHint,
    disabledModelHint,
    remoteFixedHint,
    remoteSyncHint,
    defaultLabel,
  } = args;
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
          ? remoteProvider?.defaultModel?.modelKey === p.defaultModel?.modelKey
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
              : defaultLabel,
    });
  }
  return out.slice(0, 5);
}

// 目录选项（provider-default 首项，再 fixed-model 列表）。
export function buildCatalogOptions(args: {
  compatible: PickerProvider[];
  executionLocation: string;
  remoteByKey: Map<string, PickerProvider> | null;
  supportsFixedModel: boolean;
  followDefaultLabel: string;
  noDefaultModelLabel: string;
  remoteSyncHint: string;
  remoteFixedHint: string;
  disabledProviderHint: string;
  disabledModelHint: string;
  defaultCurrentPrefix: string;
}): Option[] {
  const {
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
    defaultCurrentPrefix,
  } = args;
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
          {defaultCurrentPrefix}{" "}
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
        remoteByKey != null && executionLocation !== "" && !supportsFixedModel;
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
}

// 失效目标：以禁用项保留在列表顶部（不被清除），目标本身即当前 selected。
export function buildInvalidOption(args: {
  invalid: boolean;
  selected: ModelTarget | null;
  catalog: PickerProvider[];
}): Option | null {
  const { invalid, selected, catalog } = args;
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
}

// 搜索过滤：匹配 provider 名 / model id / 特殊项。chips（最近使用）只在未搜索时显示。
export function filterFlatOptions(args: {
  search: string;
  specialLabel: string;
  specialResolution: ReactNode;
  invalidOption: Option | null;
  catalogOptions: Option[];
}): Option[] {
  const { search, specialLabel, specialResolution, invalidOption } = args;
  // 特殊项（native / inherit）在这里构造，让调用方的 memo 不必把它当依赖。
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
  base.push(...args.catalogOptions);
  if (q === "") return base;
  const match = (o: Option) => o.searchText.toLowerCase().includes(q);
  return base.filter(match);
}

// 同步入口落点：每个「本机独有 / 目录过期」的供应商，只在它第一条待修复的行内挂一个
// 「同步过去」，而不是在列表底部聚合。没有真实同步路由（未传 onSyncProvider）时一个都不挂。
export function buildSyncAnchors(args: {
  flatOptions: Option[];
  hasSyncRoute: boolean;
  remoteSyncHint: string;
}): Map<string, string> {
  const anchors = new Map<string, string>();
  if (!args.hasSyncRoute) return anchors;
  for (const o of args.flatOptions) {
    if (o.disabledHint !== args.remoteSyncHint) continue;
    if (anchors.has(o.target.providerKey)) continue;
    anchors.set(o.target.providerKey, o.key);
  }
  return anchors;
}
