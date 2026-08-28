import { useId, useState } from "react";
import { ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  Input,
  Spinner,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import { deriveDeviceName } from "./format";
import { TLSTrustDialog } from "./tls-trust-dialog";

// Local type — mirrors remote_device_svc.AddRequest; avoids transitive wailsjs import.
export type AddRequest = {
  url: string;
  pairingCode: string;
  displayName?: string;
  tlsMode: string;
  tlsCertPEM?: string;
};

const URL_RE = /^wss?:\/\/[^/]+\/rpc$/;

function limitCode(raw: string): string {
  return raw.toUpperCase().slice(0, 6);
}

type UseDevicePairingFormOptions = {
  onSubmit: (req: AddRequest) => Promise<void>;
  onSubmittingChange?: (submitting: boolean) => void;
};

function useDevicePairingForm({
  onSubmit,
  onSubmittingChange,
}: UseDevicePairingFormOptions) {
  const { t } = useTranslation();
  const [url, setUrl] = useState("");
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [tlsMode, setTlsMode] = useState("default");
  const [tlsCertPEM, setTlsCertPEM] = useState("");
  const [tlsOpen, setTlsOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const urlValid = URL_RE.test(url);
  const codeValid = code.trim().length === 6;
  const canSubmit = urlValid && codeValid && !submitting;

  const reset = () => {
    setUrl("");
    setCode("");
    setName("");
    setTlsMode("default");
    setTlsCertPEM("");
    setTlsOpen(false);
    setError(null);
    setSubmitting(false);
    onSubmittingChange?.(false);
  };

  const submit = async () => {
    if (!canSubmit) return;
    setError(null);
    setSubmitting(true);
    onSubmittingChange?.(true);
    try {
      await onSubmit({
        url,
        pairingCode: code.trim(),
        displayName: name.trim() || deriveDeviceName(url, []),
        tlsMode,
        tlsCertPEM,
      });
      reset();
    } catch (submitError: unknown) {
      const message =
        typeof submitError === "string"
          ? submitError
          : submitError instanceof Error
            ? submitError.message
            : t("remoteDevices.add.errors.pairFailed");
      setError(message);
      setSubmitting(false);
      onSubmittingChange?.(false);
    }
  };

  return {
    canSubmit,
    code,
    codeValid,
    error,
    name,
    reset,
    setCode: (value: string) => setCode(limitCode(value)),
    setName,
    setTLSCertPEM: setTlsCertPEM,
    setTLSMode: setTlsMode,
    setTLSOpen: setTlsOpen,
    setURL: (value: string) => setUrl(value.trim()),
    submit,
    submitting,
    tlsCertPEM,
    tlsMode,
    tlsOpen,
    url,
    urlValid,
  };
}

type DevicePairingController = ReturnType<typeof useDevicePairingForm>;

type DevicePairingFieldsProps = {
  pairing: DevicePairingController;
};

function DevicePairingFields({ pairing }: DevicePairingFieldsProps) {
  const { t } = useTranslation();
  const fieldId = useId();
  const urlId = `${fieldId}-url`;
  const codeId = `${fieldId}-code`;
  const nameId = `${fieldId}-name`;

  return (
    <>
      <FieldGroup className="gap-4">
        <Field data-invalid={pairing.url.length > 0 && !pairing.urlValid}>
          <FieldLabel htmlFor={urlId}>
            {t("remoteDevices.add.fields.url")}
          </FieldLabel>
          <Input
            id={urlId}
            value={pairing.url}
            onChange={(event) => pairing.setURL(event.target.value)}
            placeholder="ws://192.168.1.100:7456/rpc"
            disabled={pairing.submitting}
            aria-invalid={pairing.url.length > 0 && !pairing.urlValid}
          />
          {pairing.url.length > 0 && !pairing.urlValid ? (
            <FieldError>{t("remoteDevices.add.fields.urlInvalid")}</FieldError>
          ) : null}
        </Field>

        <Field data-invalid={pairing.code.length > 0 && !pairing.codeValid}>
          <FieldLabel htmlFor={codeId}>
            {t("remoteDevices.add.fields.pairingCode")}
          </FieldLabel>
          <Input
            id={codeId}
            value={pairing.code}
            onChange={(event) => pairing.setCode(event.target.value)}
            placeholder="ABC2DE"
            maxLength={6}
            disabled={pairing.submitting}
            aria-invalid={pairing.code.length > 0 && !pairing.codeValid}
            className="text-center font-mono text-lg tracking-widest"
          />
          <FieldDescription className="text-xs">
            {t("remoteDevices.add.fields.pairingCodeHint")}
          </FieldDescription>
          {pairing.code.length > 0 && !pairing.codeValid ? (
            <FieldError>{t("remoteDevices.add.fields.codeInvalid")}</FieldError>
          ) : null}
        </Field>

        <Field>
          <FieldLabel htmlFor={nameId}>
            {t("remoteDevices.add.fields.displayName")}
          </FieldLabel>
          <Input
            id={nameId}
            value={pairing.name}
            onChange={(event) => pairing.setName(event.target.value)}
            placeholder={t("remoteDevices.add.fields.displayNamePlaceholder")}
            disabled={pairing.submitting}
          />
        </Field>
      </FieldGroup>

      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="self-start"
        onClick={() => pairing.setTLSOpen(true)}
        disabled={pairing.submitting}
      >
        <ChevronRight data-icon="inline-start" aria-hidden="true" />
        {t("remoteDevices.add.advancedTls", {
          mode:
            pairing.tlsMode === "default"
              ? t("remoteDevices.tls.modes.default.label")
              : pairing.tlsMode,
        })}
      </Button>

      {pairing.error ? (
        <div role="alert" className="text-sm text-destructive">
          {pairing.error}
        </div>
      ) : null}

      <TLSTrustDialog
        open={pairing.tlsOpen}
        initialMode={pairing.tlsMode}
        initialPEM={pairing.tlsCertPEM}
        onClose={() => pairing.setTLSOpen(false)}
        onApply={(mode, pem) => {
          pairing.setTLSMode(mode);
          pairing.setTLSCertPEM(pem);
          pairing.setTLSOpen(false);
        }}
      />
    </>
  );
}

type DevicePairingFormProps = UseDevicePairingFormOptions & {
  actionsClassName?: string;
  cancelLabel?: string;
  className?: string;
  onCancel?: () => void;
  submitLabel?: string;
};

export function DevicePairingForm({
  actionsClassName,
  cancelLabel,
  className,
  onCancel,
  onSubmit,
  onSubmittingChange,
  submitLabel,
}: DevicePairingFormProps) {
  const { t } = useTranslation();
  const pairing = useDevicePairingForm({ onSubmit, onSubmittingChange });

  return (
    <form
      className={cn("flex flex-col gap-4", className)}
      noValidate
      onSubmit={(event) => {
        event.preventDefault();
        void pairing.submit();
      }}
    >
      <DevicePairingFields pairing={pairing} />

      <div className={cn("flex items-center gap-2", actionsClassName)}>
        {onCancel ? (
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              if (pairing.submitting) return;
              pairing.reset();
              onCancel();
            }}
            disabled={pairing.submitting}
          >
            {cancelLabel ?? t("common.cancel")}
          </Button>
        ) : null}
        <PairingSubmitButton
          canSubmit={pairing.canSubmit}
          submitting={pairing.submitting}
          submitLabel={submitLabel}
        />
      </div>
    </form>
  );
}

function PairingSubmitButton({
  canSubmit,
  submitLabel,
  submitting,
}: {
  canSubmit: boolean;
  submitLabel?: string;
  submitting: boolean;
}) {
  const { t } = useTranslation();

  return (
    <Button type="submit" disabled={!canSubmit}>
      {submitting ? (
        <Spinner
          data-icon="inline-start"
          aria-label={t("remoteDevices.add.pairing")}
          className="motion-reduce:animate-none"
        />
      ) : null}
      {submitting
        ? t("remoteDevices.add.pairing")
        : (submitLabel ?? t("remoteDevices.add.pair"))}
    </Button>
  );
}
