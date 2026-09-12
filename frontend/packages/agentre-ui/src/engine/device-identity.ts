export type PairedDeviceIdentity = {
  id: number;
  daemonFingerprint?: string;
};

/**
 * `local` and `remote` are two verdicts, not one flag: both false means
 * "undetermined" — the evidence needed to classify the target has not arrived.
 * Callers must not turn remote gating on for an undetermined target.
 */
export type ExecutionDeviceResolution = {
  local: boolean;
  remote: boolean;
  pairedDeviceId: number;
};

/**
 * Resolve a persisted/synced execution-device identity at the local UI boundary.
 * Canonical fingerprints remain the stored value; only RPCs backed by a local
 * paired-device row consume pairedDeviceId.
 *
 * `localFingerprint` is "" when this installation's identity is not known yet
 * (the RPC has not answered, or it failed). That is an absence of evidence, so
 * a bare fingerprint stays undetermined rather than being called remote: R13
 * canonicalization stores this desktop's own fingerprint on every local
 * backend, and mislabeling it would gate a purely local target as an
 * unreachable peer.
 */
export function resolveExecutionDevice(
  deviceId: string,
  localFingerprint: string,
  devices: PairedDeviceIdentity[],
): ExecutionDeviceResolution {
  const value = deviceId.trim();
  if (value === "" || (localFingerprint !== "" && value === localFingerprint)) {
    return { local: true, remote: false, pairedDeviceId: 0 };
  }
  if (/^\d+$/.test(value)) {
    const pairedDeviceId = Number(value);
    return {
      local: false,
      remote: pairedDeviceId > 0,
      pairedDeviceId: pairedDeviceId > 0 ? pairedDeviceId : 0,
    };
  }
  // A paired agentred row is another machine by construction — that verdict
  // never depends on knowing our own fingerprint.
  const paired = devices.find((device) => device.daemonFingerprint === value);
  if (paired) {
    return { local: false, remote: true, pairedDeviceId: paired.id };
  }
  return {
    local: false,
    remote: localFingerprint !== "",
    pairedDeviceId: 0,
  };
}

export function deviceSelectValue(
  deviceId: string,
  localFingerprint: string,
  localValue: string,
): string {
  return deviceId === "" ||
    (localFingerprint !== "" && deviceId === localFingerprint)
    ? localValue
    : deviceId;
}

export function persistedDeviceIdForSelection(
  value: string,
  localValue: string,
  localFingerprint: string,
): string {
  return value === localValue ? localFingerprint : value;
}

export function pairedDeviceSelectValue(device: PairedDeviceIdentity): string {
  return device.daemonFingerprint || String(device.id);
}
