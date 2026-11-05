import { useTranslation } from "react-i18next";

/**
 * 失败 toast 的正文（S5）：后端按语言渲染好的那句业务文案作正文；机器原话只进
 * 默认折叠的等宽「详情」，不当正文。两样都没有就不渲染。
 */
export function FailureBody({
  message,
  detail,
}: {
  message: string;
  detail?: string;
}) {
  const { t } = useTranslation();
  if (!message && !detail) return null;
  return (
    <div className="flex flex-col gap-1">
      {message ? <p>{message}</p> : null}
      {detail ? (
        <details>
          <summary className="cursor-pointer">
            {t("dataBackup.failure.details")}
          </summary>
          <pre
            data-selectable-text="true"
            className="mt-1 font-mono text-2xs break-all whitespace-pre-wrap"
          >
            {detail}
          </pre>
        </details>
      ) : null}
    </div>
  );
}
