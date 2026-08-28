import * as React from "react";

import {
  ProviderPillTrigger,
  resolveProviderPillState,
  type ModelTarget,
} from "@agentre-hub/agentre-ui";
import { useTranslation } from "react-i18next";

import {
  isProviderCompatible,
  isProviderSelectableBackend,
} from "../model-pill";
import { useModelTargetCatalog } from "../model-target-picker/catalog";
import { ModelTargetPicker } from "../model-target-picker";
import { ListLLMProviders } from "../../../../wailsjs/go/app/App";
import type { llm_provider_svc } from "../../../../wailsjs/go/models";

export type BoardModelPillProps = {
  /** 共享包递过来的 pill 形状；三颗触发器摆在一排必须是同一串。 */
  className: string;
  /** 生效执行档的后端类型；兼容判据与最终能选什么全看它。 */
  backendType: string;
  /** Agent 自己绑的供应商；`undefined` = 还不知道，不能当成「没绑」。 */
  boundProviderKey?: string | null;
  providerKey: string;
  modelKey: string;
  onChange: (target: ModelTarget) => void;
  disabled?: boolean;
};

/**
 * 任务表单执行段的**模型**那一颗。
 *
 * 选择器与触发器都是共享包的（`ModelTargetPicker` + `ProviderPillTrigger`），四态与
 * 「脸上写实际会跑哪个模型」的推导也是包里那一份 —— 这里只做宿主那一段：把本机的
 * 供应商目录拉回来，按后端兼容判据过一遍。
 *
 * 与 composer 的 `useProviderPill` **不同**的是这一颗不落库、不做远端门控：本轮没有
 * 任何路径读这三个值（规格决策 9），任务只是把「打算怎么跑」记下来。
 */
export function BoardModelPill({
  className,
  backendType,
  boundProviderKey,
  providerKey,
  modelKey,
  onChange,
  disabled,
}: BoardModelPillProps) {
  const { t } = useTranslation();
  const [providers, setProviders] = React.useState<
    llm_provider_svc.ProviderItem[]
  >([]);
  const [loading, setLoading] = React.useState(
    isProviderSelectableBackend(backendType),
  );
  const [failed, setFailed] = React.useState(false);

  // 换后端后旧一轮的迟到结果直接丢弃，否则上一个后端的兼容集合会盖住当前的。
  const requestRef = React.useRef(0);
  React.useEffect(() => {
    const request = ++requestRef.current;
    if (!isProviderSelectableBackend(backendType)) {
      setProviders([]);
      setLoading(false);
      setFailed(false);
      return;
    }
    setLoading(true);
    setFailed(false);
    void ListLLMProviders()
      .then((response) => {
        if (request !== requestRef.current) return;
        setProviders(
          (response.items ?? []).filter((provider) =>
            isProviderCompatible(backendType, provider.type),
          ),
        );
      })
      .catch(() => {
        if (request !== requestRef.current) return;
        setProviders([]);
        setFailed(true);
      })
      .finally(() => {
        if (request === requestRef.current) setLoading(false);
      });
  }, [backendType]);

  const {
    catalog,
    loading: catalogLoading,
    error: catalogError,
  } = useModelTargetCatalog(providers);

  const target = React.useMemo(
    () => ({ providerKey, modelKey }),
    [modelKey, providerKey],
  );
  const pillState = React.useMemo(
    () =>
      resolveProviderPillState({
        boundProviderKey,
        boundModelKey: null,
        target,
        catalog,
      }),
    [boundProviderKey, catalog, target],
  );

  const invalid = pillState.mode === "invalid";

  // 换 Agent / 换机器之后这个供应商可能已经不兼容新后端了：退回「跟随 Agent 绑定」，
  // 不留一个死指向。判据是**兼容列表**而不是「目标解析不出来」——目录是分两步到的
  // （先供应商、再每家的模型），拿解析结果当判据的话，中间那一帧会把兼容的选择也
  // 一并抹掉。供应商还在、只是模型被停用/删掉，由 Picker 的失效态说出来，不替用户
  // 清空。
  const incompatible =
    !loading &&
    !failed &&
    providerKey !== "" &&
    !providers.some((provider) => provider.providerKey === providerKey);

  React.useEffect(() => {
    if (incompatible) onChange({ providerKey: "", modelKey: "" });
  }, [incompatible, onChange]);

  return (
    <ModelTargetPicker
      scenario="chat"
      backendType={backendType}
      selected={target}
      onChange={onChange}
      catalog={catalog}
      loading={loading || catalogLoading}
      error={catalogError || failed}
      disabled={disabled || !isProviderSelectableBackend(backendType)}
      invalid={invalid}
      triggerLabel={<ProviderPillTrigger state={pillState} />}
      data-testid="board-model-pill"
      aria-label={t("issues.exec.modelAria")}
      className={className}
    />
  );
}
