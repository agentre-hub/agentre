import * as React from "react";

import type { IssueTone } from "./types";
import type { LabelMutation } from "./query-types";

export interface UseLabelManagerResult {
  /** 正在改名的那一行；`null` = 没有。 */
  renaming: number | null;
  draftName: string;
  setDraftName: (name: string) => void;
  /** 编辑那一行的色调；换色与改名一起发出去（`update` 一次带齐两者）。 */
  draftTone: IssueTone;
  setDraftTone: (tone: IssueTone) => void;
  startRename: (id: number, name: string, tone: IssueTone) => void;
  cancelRename: () => void;
  /** 正在确认删除的那一行；确认框里要说清爆炸半径。 */
  deleting: number | null;
  askDelete: (id: number) => void;
  cancelDelete: () => void;
  newName: string;
  setNewName: (name: string) => void;
  newTone: IssueTone;
  setNewTone: (tone: IssueTone) => void;
  mutate: (mutation: LabelMutation) => void;
  /** 正在往外发的那一次写；期间按钮禁用。 */
  busy: boolean;
  /** 上一次写没能过去。面板据此把编辑行留在原位并说一句话。 */
  failed: boolean;
  /**
   * 失败的原因，宿主给的原文（不翻译）。宿主自己已经把报错收进别处、只回一个
   * 「没过去」时是 `null`，面板改说一句通用的。
   */
  error: string | null;
}

/**
 * 一次写的结果。宿主可以 reject（原因随异常带出来），也可以 resolve 一个 `false`
 * ——桌面宿主的四条写路径刻意都不 reject（`void` 调用会变成没人接的未处理拒绝），
 * 它们回的就是这个布尔。两种都算「没过去」。
 */
export type LabelMutateResult = Promise<void | boolean> | void | boolean;

/**
 * 标签管理**唯一**的一支 hook：改名 / 删除确认 / 新建三段状态都在这里，面板只画。
 */
export function useLabelManager(
  onLabelMutate: (mutation: LabelMutation) => LabelMutateResult,
): UseLabelManagerResult {
  const [renaming, setRenaming] = React.useState<number | null>(null);
  const [draftName, setDraftName] = React.useState("");
  const [draftTone, setDraftTone] = React.useState<IssueTone>("gray");
  const [deleting, setDeleting] = React.useState<number | null>(null);
  const [newName, setNewName] = React.useState("");
  const [newTone, setNewTone] = React.useState<IssueTone>("gray");
  const [busy, setBusy] = React.useState(false);
  const [failed, setFailed] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // 写成功才收起编辑行 / 确认框、才清掉刚输入的名字：失败也一起收的话，用户看到
  // 的是「面板复位、什么都没发生」——刚敲的名字没了，也没有一句话说为什么。
  const mutate = React.useCallback(
    (mutation: LabelMutation) => {
      setBusy(true);
      setFailed(false);
      setError(null);
      void Promise.resolve(onLabelMutate(mutation))
        .then((result) => {
          if (result === false) {
            setFailed(true);
            return;
          }
          setRenaming(null);
          setDeleting(null);
          if (mutation.kind === "create") setNewName("");
        })
        .catch((cause: unknown) => {
          setFailed(true);
          setError(cause instanceof Error ? cause.message : String(cause));
        })
        .finally(() => setBusy(false));
    },
    [onLabelMutate],
  );

  return {
    renaming,
    draftName,
    setDraftName,
    draftTone,
    setDraftTone,
    startRename: (id, name, tone) => {
      setDraftName(name);
      // 从这一行**现在**的色调起步：不改色时发出去的还是它自己那一档。
      setDraftTone(tone);
      setRenaming(id);
    },
    cancelRename: () => setRenaming(null),
    deleting,
    askDelete: setDeleting,
    cancelDelete: () => setDeleting(null),
    newName,
    setNewName,
    newTone,
    setNewTone,
    mutate,
    busy,
    failed,
    error,
  };
}
