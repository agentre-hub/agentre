// @ts-nocheck
import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";
import {
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Download,
  Inbox,
  Loader2,
  Pencil,
  RefreshCw,
  Search,
  TriangleAlert,
} from "lucide-react";

import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";

import {
  ImportLLMModels,
  PreviewLLMModels,
} from "../port-bridge";
import { llm_provider_svc } from "../port-bridge";
import { LlmModelLogo } from "../ai-brand-logo";
import {
  type Model,
  type ModelInfo,
  type Provider,
  errMessage,
  formatTokens,
} from "./index";

// 模型族可读标签：内置目录 vendor 为小写标识，映射为品牌名；未收录的 vendor
// 兜底为大小写可读形式，认不出的（空 vendor）由调用方归入「其它」分组。
const VENDOR_LABELS: Record<string, string> = {
  anthropic: "Anthropic",
  deepseek: "DeepSeek",
  gemini: "Gemini",
  glm: "GLM",
  kimi: "Kimi",
  meta: "Meta",
  minimax: "MiniMax",
  mistral: "Mistral AI",
  openai: "OpenAI",
  qwen: "Qwen",
  xai: "xAI",
};

function vendorLabel(vendor: string): string {
  if (!vendor) return "";
  return (
    VENDOR_LABELS[vendor] ?? vendor.charAt(0).toUpperCase() + vendor.slice(1)
  );
}

type FailureKind =
  | "auth"
  | "endpoint"
  | "server"
  | "status"
  | "network"
  | "generic";

// 把上游错误归类成可理解的失败原因；原始文本只用于折叠区，不作为主文案。
// 状态码一路带到标题里（「认证失败（401）」），不要在归类时丢掉。
function discoverFailure(err: unknown): { kind: FailureKind; code?: string } {
  const msg = errMessage(err);
  const status = msg.match(/http\s+(\d{3})/i)?.[1];
  if (status) {
    const code = Number(status);
    if (code === 401 || code === 403) return { kind: "auth", code: status };
    if (code === 404) return { kind: "endpoint", code: status };
    if (code >= 500) return { kind: "server", code: status };
    return { kind: "status", code: status };
  }
  if (
    /(?:connection refused|no such host|getaddrinfo|timed out|timeout|econnrefused|enotfound|dial tcp|network)/i.test(
      msg,
    )
  ) {
    return { kind: "network" };
  }
  return { kind: "generic" };
}

type VendorGroup = { key: string; label: string; items: ModelInfo[] };

export function DiscoverDialog({
  provider,
  existingModels,
  onClose,
  onEditConnection,
  onImported,
}: {
  provider: Provider | null;
  existingModels: Model[];
  onClose: () => void;
  onEditConnection: () => void;
  onImported: (resp: llm_provider_svc.ImportModelsResponse) => void;
}) {
  const { t } = useTranslation();
  const open = provider !== null;
  const [items, setItems] = React.useState<ModelInfo[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [fetchError, setFetchError] = React.useState<string | null>(null);
  const [importError, setImportError] = React.useState<string | null>(null);
  const [showRaw, setShowRaw] = React.useState(false);
  const [selected, setSelected] = React.useState<Set<string>>(new Set());
  const [search, setSearch] = React.useState("");
  const [importing, setImporting] = React.useState(false);
  const requestRef = React.useRef(0);

  const existingIds = React.useMemo(
    () => new Set(existingModels.map((m) => m.modelId)),
    [existingModels],
  );

  const fetchPreview = React.useCallback(async () => {
    if (!provider) return;
    const requestId = ++requestRef.current;
    setLoading(true);
    setFetchError(null);
    setImportError(null);
    setShowRaw(false);
    try {
      const resp = await PreviewLLMModels(
        new llm_provider_svc.PreviewModelsRequest({
          id: provider.id,
          type: provider.type,
          apiKey: "",
          baseUrl: "",
        }),
      );
      if (requestId !== requestRef.current) return;
      const list = resp.items ?? [];
      setItems(list);
      setSelected(
        new Set(list.filter((m) => !existingIds.has(m.id)).map((m) => m.id)),
      );
    } catch (err) {
      if (requestId === requestRef.current) setFetchError(errMessage(err));
    } finally {
      if (requestId === requestRef.current) setLoading(false);
    }
  }, [existingIds, provider]);

  React.useEffect(() => {
    if (open) void fetchPreview();
  }, [open, fetchPreview]);

  const newCount = items.filter((m) => !existingIds.has(m.id)).length;
  const existingCount = items.length - newCount;
  const selectedCount = [...selected].filter(
    (id) => !existingIds.has(id),
  ).length;

  const trimmed = search.trim().toLowerCase();
  const visible = trimmed
    ? items.filter(
        (m) =>
          m.id.toLowerCase().includes(trimmed) ||
          m.vendor.toLowerCase().includes(trimmed),
      )
    : items;

  const groups: VendorGroup[] = React.useMemo(() => {
    const map = new Map<string, ModelInfo[]>();
    for (const m of visible) {
      const key = m.vendor?.trim().toLowerCase() || "__unknown__";
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(m);
    }
    return [...map.entries()]
      .map(([key, groupItems]) => ({
        key,
        label:
          key === "__unknown__"
            ? t("llmProviders.discover.otherVendor")
            : vendorLabel(key),
        items: groupItems,
      }))
      .sort((a, b) => {
        if (a.key === "__unknown__") return 1;
        if (b.key === "__unknown__") return -1;
        return a.label.localeCompare(b.label);
      });
  }, [visible, t]);

  const toggle = React.useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  const toggleAllNew = React.useCallback(
    (checked: boolean) => {
      setSelected((prev) => {
        const next = new Set(prev);
        for (const m of items) {
          if (existingIds.has(m.id)) continue;
          if (checked) {
            next.add(m.id);
          } else {
            next.delete(m.id);
          }
        }
        return next;
      });
    },
    [existingIds, items],
  );

  const allNewChecked =
    newCount > 0 &&
    items
      .filter((m) => !existingIds.has(m.id))
      .every((m) => selected.has(m.id));

  const importModels = React.useCallback(async () => {
    if (!provider || selectedCount === 0) return;
    setImporting(true);
    setImportError(null);
    try {
      const resp = await ImportLLMModels(
        new llm_provider_svc.ImportModelsRequest({
          providerId: provider.id,
          models: items
            .filter((m) => !existingIds.has(m.id) && selected.has(m.id))
            .map(
              (m) =>
                new llm_provider_svc.ModelInput({
                  modelId: m.id,
                  name: "",
                  contextWindow: m.contextWindow,
                  maxOutput: m.maxOutput,
                }),
            ),
        }),
      );
      onImported(resp);
      onClose();
    } catch (err) {
      setImportError(errMessage(err));
    } finally {
      setImporting(false);
    }
  }, [
    existingIds,
    items,
    onClose,
    onImported,
    provider,
    selected,
    selectedCount,
  ]);

  const providerName = provider ? provider.name : "";
  const failure = fetchError ? discoverFailure(fetchError) : null;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !importing) onClose();
      }}
    >
      <DialogContent className="max-w-[640px]">
        <DialogHeader>
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0 flex-1">
              <DialogTitle>
                {t("llmProviders.discover.title", { name: providerName })}
              </DialogTitle>
              <DialogDescription>
                {t("llmProviders.discover.description")}
              </DialogDescription>
            </div>
            {/* 读取成功的状态 chip：读到了什么在头部一眼可见 */}
            {!loading && !fetchError && items.length > 0 ? (
              <span className="shrink-0 rounded-sm bg-status-running/10 px-1.5 py-0.5 text-2xs font-medium text-status-running">
                {t("llmProviders.discover.readOk")}
              </span>
            ) : null}
          </div>
        </DialogHeader>

        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-5 py-3">
          <div className="relative min-w-0 flex-1">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
              placeholder={t("llmProviders.discover.searchPlaceholder")}
              className="h-8 pl-8 text-xs"
              aria-label={t("llmProviders.discover.searchAria")}
            />
          </div>
          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
            {t("llmProviders.discover.count", {
              remote: items.length,
              new: newCount,
              existing: existingCount,
            })}
          </span>
        </div>

        <DialogBody className="space-y-2">
          {fetchError && failure ? (
            <div
              role="alert"
              className="flex flex-col items-start gap-2 rounded-md border border-status-error/40 bg-destructive-soft px-3 py-2.5 text-status-error"
            >
              <div className="flex items-start gap-2 text-xs">
                <TriangleAlert
                  className="mt-0.5 size-4 shrink-0"
                  aria-hidden="true"
                />
                {/* 标题一句话说清是什么失败，解释单独一段 */}
                <div className="flex min-w-0 flex-col gap-0.5">
                  <span className="font-semibold">
                    {t(`llmProviders.discover.errorTitle.${failure.kind}`, {
                      code: failure.code,
                    })}
                  </span>
                  <span className="text-2xs leading-relaxed">
                    {t(`llmProviders.discover.error.${failure.kind}`, {
                      code: failure.code,
                    })}
                  </span>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-7 gap-1.5 text-2xs"
                  onClick={onEditConnection}
                >
                  <Pencil className="size-3" aria-hidden="true" />
                  {t("llmProviders.discover.goEditConnection")}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="h-7 gap-1.5 text-2xs"
                  onClick={() => void fetchPreview()}
                  disabled={loading}
                >
                  {loading ? (
                    <Loader2
                      className="size-3 animate-spin"
                      aria-hidden="true"
                    />
                  ) : (
                    <RefreshCw className="size-3" aria-hidden="true" />
                  )}
                  {t("llmProviders.discover.retry")}
                </Button>
              </div>
              <button
                type="button"
                className="flex items-center gap-1 text-2xs text-muted-foreground underline-offset-2 hover:underline"
                onClick={() => setShowRaw((v) => !v)}
                aria-expanded={showRaw}
              >
                {showRaw ? (
                  <ChevronUp className="size-3" aria-hidden="true" />
                ) : (
                  <ChevronDown className="size-3" aria-hidden="true" />
                )}
                {t("llmProviders.discover.viewRawResponse")}
              </button>
              {showRaw ? (
                <pre className="max-h-40 w-full overflow-auto whitespace-pre-wrap break-words rounded border border-border bg-secondary px-2.5 py-2 font-mono text-2xs text-foreground">
                  {fetchError}
                </pre>
              ) : null}
            </div>
          ) : loading ? (
            <div className="flex items-center gap-2 py-6 text-2xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              {t("llmProviders.discover.loading")}
            </div>
          ) : items.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-6 text-center">
              <Inbox
                className="size-5 text-decorative-foreground"
                aria-hidden="true"
              />
              <p className="text-xs font-semibold text-foreground">
                {t("llmProviders.discover.empty")}
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              <label className="flex items-center gap-2 rounded-md border border-border bg-secondary px-2.5 py-2">
                <Checkbox
                  checked={allNewChecked}
                  onCheckedChange={(next) => toggleAllNew(next === true)}
                  disabled={newCount === 0}
                  aria-label={t("llmProviders.discover.selectAllNew")}
                />
                <span className="text-2xs font-semibold">
                  {t("llmProviders.discover.selectAllNew")}
                </span>
                <span className="ml-auto text-2xs text-muted-foreground">
                  {t("llmProviders.discover.selectAllHint")}
                </span>
              </label>
              {groups.map((group) => (
                <div key={group.key} className="space-y-1">
                  <div className="flex items-center gap-1.5 pt-1 text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                    {group.key !== "__unknown__" ? (
                      <LlmModelLogo
                        providerType={provider?.type ?? ""}
                        model={group.items[0]?.id ?? ""}
                        className="size-3.5"
                      />
                    ) : null}
                    <span>{group.label}</span>
                  </div>
                  {group.items.map((m) => {
                    const existing = existingIds.has(m.id);
                    const checked = selected.has(m.id);
                    return (
                      <div
                        key={m.id}
                        className="flex items-center gap-2.5 rounded-md border border-border px-2.5 py-2"
                      >
                        <Checkbox
                          checked={existing ? false : checked}
                          disabled={existing}
                          onCheckedChange={() => toggle(m.id)}
                          aria-label={m.id}
                        />
                        <LlmModelLogo
                          providerType={provider?.type ?? ""}
                          model={m.id}
                          className="size-4"
                        />
                        <span className="min-w-0 flex-1 truncate font-mono text-xs">
                          {m.id}
                        </span>
                        {m.contextWindow > 0 || m.maxOutput > 0 ? (
                          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                            {formatTokens(m.contextWindow)} ctx ·{" "}
                            {formatTokens(m.maxOutput)} out
                          </span>
                        ) : null}
                        <span
                          className={
                            existing
                              ? "shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground"
                              : "shrink-0 rounded-sm bg-primary-soft px-1.5 py-0.5 text-2xs text-primary-text"
                          }
                        >
                          {existing
                            ? t("llmProviders.discover.existingSkip")
                            : t("llmProviders.discover.new")}
                        </span>
                      </div>
                    );
                  })}
                </div>
              ))}
            </div>
          )}
        </DialogBody>

        {importError ? (
          <p role="alert" className="px-5 pb-1 text-2xs text-status-error">
            {importError}
          </p>
        ) : null}

        <DialogFooter className="justify-between gap-2">
          <span className="text-2xs leading-relaxed text-muted-foreground">
            {t("llmProviders.discover.importSummary", { count: selectedCount })}
            {existingCount > 0
              ? ` · ${t("llmProviders.discover.importSummaryExisting")}`
              : ""}
          </span>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-8 text-xs"
              onClick={onClose}
              disabled={importing}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              className="h-8 gap-1.5 text-xs"
              onClick={() => void importModels()}
              disabled={importing || selectedCount === 0}
            >
              {importing ? (
                <Loader2
                  className="size-3.5 animate-spin"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              ) : (
                <Download
                  className="size-3.5"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              )}
              {t("llmProviders.discover.importButton", {
                count: selectedCount,
              })}
            </Button>
          </div>
        </DialogFooter>

        {importing ? (
          <p
            role="status"
            className="flex items-center gap-1.5 px-5 pb-3 text-2xs text-muted-foreground"
          >
            <CheckCircle2
              className="size-3 text-status-running"
              aria-hidden="true"
            />
            {t("llmProviders.discover.importing")}
          </p>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
