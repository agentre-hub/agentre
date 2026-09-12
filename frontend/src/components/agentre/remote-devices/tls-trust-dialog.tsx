import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import {
  AgentreDialog,
  Button,
  RadioGroup,
  RadioGroupItem,
  Textarea,
} from "@agentre-hub/agentre-ui";

type Props = {
  open: boolean;
  initialMode: string;
  initialPEM: string;
  onClose: () => void;
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
  // RadioGroupItem 渲染成内容为空的 <button role="radio">,button 的可及名称按
  // 「名称来自内容」算,包裹它的 <label> 并不会给它命名 —— 每一项都显式把名称指到
  // 自己那行标题,说明文字挂 aria-describedby(hook 不能进 map,故用同一个前缀派生)。
  const modeIdPrefix = useId();

  useEffect(() => {
    if (open) {
      setMode(initialMode || "default");
      setPem(initialPEM ?? "");
      setError(null);
    }
  }, [open, initialMode, initialPEM]);

  const needsPEM = mode === "pin-cert" || mode === "ca-bundle";

  const apply = async () => {
    setError(null);
    if (needsPEM) {
      const e = validatePEM(pem, t);
      if (e) {
        setError(e);
        return;
      }
    }
    await onApply(mode, needsPEM ? pem.trim() : "");
  };

  return (
    <AgentreDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
      title={t("remoteDevices.tls.title")}
      description={t("remoteDevices.tls.description")}
      contentClassName="sm:max-w-[540px]"
      bodyClassName="flex flex-col gap-3.5"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={apply}>{t("remoteDevices.tls.apply")}</Button>
        </>
      }
    >
      <RadioGroup value={mode} onValueChange={setMode} className="gap-3">
        {MODES.map((m) => (
          <label
            key={m.value}
            className={`flex items-start gap-3 rounded-md border p-3 cursor-pointer ${
              mode === m.value
                ? m.danger
                  ? "border-destructive bg-destructive/5"
                  : "border-primary bg-primary/5"
                : "border-border"
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
                        ? "bg-destructive/20 text-destructive"
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

      {error ? <div className="text-sm text-destructive">{error}</div> : null}
    </AgentreDialog>
  );
}
