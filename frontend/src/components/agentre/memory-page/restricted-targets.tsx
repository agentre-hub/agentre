import { useTranslation } from "react-i18next";

import type { memory_svc } from "../../../../wailsjs/go/models";

const REASON_KEY: Record<string, string> = {
  builtin_summaries: "memoryPage.restricted.builtinSummaries",
  unsupported_backend: "memoryPage.restricted.unsupportedBackend",
  needs_upgrade: "memoryPage.restricted.needsUpgrade",
};

/** agent 头部下方：哪些执行目标不带记忆或只能读摘要，及原因。没有受限目标时不渲染。 */
export function RestrictedTargets({
  targets,
}: {
  targets: memory_svc.RestrictedTarget[];
}) {
  const { t } = useTranslation();
  const rows = targets.filter((target) => REASON_KEY[target.reason]);
  if (rows.length === 0) return null;
  return (
    <ul
      aria-label={t("memoryPage.restricted.label")}
      className="flex flex-col gap-0.5 px-6 pb-2 text-2xs text-muted-foreground"
    >
      {rows.map((target) => (
        <li key={`${target.backendId}-${target.deviceName}`}>
          {t(REASON_KEY[target.reason], {
            target: target.deviceName
              ? t("memoryPage.restricted.remoteTarget", {
                  device: target.deviceName,
                  name: target.backendName,
                })
              : target.backendName,
          })}
        </li>
      ))}
    </ul>
  );
}
