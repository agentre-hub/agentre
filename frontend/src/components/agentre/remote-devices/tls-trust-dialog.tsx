import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import {
  Button,
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
  DialogShellSubmit,
  RadioGroup,
  RadioGroupItem,
  Textarea,
} from "@agentre-hub/agentre-ui";
import { classifyAppError, type AppError } from "@/lib/app-error";

import { FailureCallout } from "../failure-toast";

type Props = {
  open: boolean;
  initialMode: string;
  initialPEM: string;
  onClose: () => void;
  /** 成功由调用方关窗；抛出即失败，窗留着、在窗里说清楚。 */
  onApply: (mode: string, pem: string) => void | Promise<void>;
};

const PEM_HEADER = "-----BEGIN CERTIFICATE-----";
const PEM_FOOTER = "-----END CERTIFICATE-----";

function validatePEM(pem: string, t: TFunction): string | null {
  const trimmed = pem.trim();
  if (!trimmed) return t("remoteDevices.tls.errors.pemRequired");
  if (!trimmed.includes(PEM_HEADER) || !trimmed.includes(PEM_FOOTER)) {
    return t("remoteDevices.tls.errors.pemInvalid");
  }
  return null;
}

const MODES = [
  {
    value: "default",
    labelKey: "remoteDevices.tls.modes.default.label",
    badgeKey: "remoteDevices.tls.badges.recommended",
    descriptionKey: "remoteDevices.tls.modes.default.description",
  },
  {
    value: "pin-cert",
    labelKey: "remoteDevices.tls.modes.pinCert.label",
    descriptionKey: "remoteDevices.tls.modes.pinCert.description",
  },
  {
    value: "ca-bundle",
    labelKey: "remoteDevices.tls.modes.caBundle.label",
    descriptionKey: "remoteDevices.tls.modes.caBundle.description",
  },
  {
    value: "skip-verify",
    labelKey: "remoteDevices.tls.modes.skipVerify.label",
    badgeKey: "remoteDevices.tls.badges.notRecommended",
    descriptionKey: "remoteDevices.tls.modes.skipVerify.description",
    danger: true,
  },
];

/** 一种 TLS 模式的名称 key；认不得的模式按默认那一种说。配对表单的入口与本窗共用。 */
export function tlsModeLabelKey(mode: string): string {
  return (MODES.find((m) => m.value === mode) ?? MODES[0]).labelKey;
}

export function TLSTrustDialog({
  open,
  initialMode,
  initialPEM,
  onClose,
  onApply,
}: Props) {
  const { t } = useTranslation();
  const [mode, setMode] = useState(initialMode || "default");
  const [pem, setPem] = useState(initialPEM ?? "");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<AppError | null>(null);
  // RadioGroupItem 渲染成内容为空的 <button role="radio">,button 的可及名称按
  // 「名称来自内容」算,包裹它的 <label> 并不会给它命名 —— 每一项都显式把名称指到
  // 自己那行标题,说明文字挂 aria-describedby(hook 不能进 map,故用同一个前缀派生)。
  const modeIdPrefix = useId();

  useEffect(() => {
    if (open) {
      setMode(initialMode || "default");
      setPem(initialPEM ?? "");
      setError(null);
      setFailure(null);
    }
  }, [open, initialMode, initialPEM]);

  const needsPEM = mode === "pin-cert" || mode === "ca-bundle";

  const apply = async () => {
    if (busy) return;
    setError(null);
    setFailure(null);
    if (needsPEM) {
      const e = validatePEM(pem, t);
      if (e) {
        setError(e);
        return;
      }
    }
    setBusy(true);
    try {
      await onApply(mode, needsPEM ? pem.trim() : "");
    } catch (e) {
      // 留窗、留所选：原样再点一次「应用」就是重试。
      setFailure(classifyAppError(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <DialogShell
      open={open}
      busy={busy}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
    >
      <DialogShellHeader
        title={t("remoteDevices.tls.title")}
        subtitle={t("remoteDevices.tls.description")}
        onClose={onClose}
        busy={busy}
      />
      <DialogShellBody className="flex flex-col gap-3.5">
        <RadioGroup value={mode} onValueChange={setMode} className="gap-3">
          {MODES.map((m) => (
            <label
              key={m.value}
              className={`flex items-start gap-3 rounded-md p-3 cursor-pointer ${
                mode === m.value
                  ? m.danger
                    ? "bg-destructive/5"
                    : "bg-primary/5"
                  : "bg-field-fill"
              }`}
            >
              <RadioGroupItem
                value={m.value}
                className="mt-1"
                aria-labelledby={`${modeIdPrefix}-${m.value}-label`}
                aria-describedby={`${modeIdPrefix}-${m.value}-desc`}
              />
              <div className="flex flex-col gap-1">
                <div className="flex items-center gap-2">
                  <span
                    id={`${modeIdPrefix}-${m.value}-label`}
                    className="text-sm font-medium"
                  >
                    {t(m.labelKey)}
                  </span>
                  {m.badgeKey ? (
                    <span
                      className={`text-3xs uppercase tracking-wide px-1.5 py-0.5 rounded ${
                        m.danger
                          ? "bg-destructive/20 text-destructive-text"
                          : "bg-secondary text-secondary-foreground"
                      }`}
                    >
                      {t(m.badgeKey)}
                    </span>
                  ) : null}
                </div>
                <p
                  id={`${modeIdPrefix}-${m.value}-desc`}
                  className="text-xs text-muted-foreground"
                >
                  {t(m.descriptionKey)}
                </p>
              </div>
            </label>
          ))}
        </RadioGroup>

        {needsPEM ? (
          <label className="flex flex-col gap-1.5">
            <span className="text-sm font-medium">
              {mode === "pin-cert"
                ? t("remoteDevices.tls.pem.cert")
                : t("remoteDevices.tls.pem.caBundle")}
            </span>
            <Textarea
              value={pem}
              onChange={(e) => setPem(e.target.value)}
              placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
              rows={8}
              className="font-mono text-xs"
            />
          </label>
        ) : null}

        {error ? (
          <div className="text-sm text-destructive-text">{error}</div>
        ) : null}

        {failure ? (
          <FailureCallout
            title={t("remoteDevices.tls.applyFailed")}
            err={failure}
          />
        ) : null}
      </DialogShellBody>
      <DialogShellFooter>
        <Button variant="ghost" disabled={busy} onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <DialogShellSubmit busy={busy} onClick={() => void apply()}>
          {t("remoteDevices.tls.apply")}
        </DialogShellSubmit>
      </DialogShellFooter>
    </DialogShell>
  );
}
