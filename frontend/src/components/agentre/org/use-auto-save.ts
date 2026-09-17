import * as React from "react";

export type AutoSaveStatus = "idle" | "saving" | "saved" | "error";

// 防抖窗口:保存前等用户停手;所有调用方都用同一个值,不再开成选项。
const AUTO_SAVE_DEBOUNCE_MS = 600;

export interface UseAutoSaveOptions<T> {
  initial: T;
  save: (values: T) => Promise<unknown>;
  isValid?: (values: T) => boolean;
}

export interface UseAutoSaveResult<T> {
  values: T;
  patch: (partial: Partial<T>, opts?: { immediate?: boolean }) => void;
  flush: () => void;
  wrap: <R>(fn: () => Promise<R>) => Promise<R | null>;
  status: AutoSaveStatus;
  pendingInvalid: boolean;
  retry: () => void;
}

export function useAutoSave<T extends object>(
  opts: UseAutoSaveOptions<T>,
): UseAutoSaveResult<T> {
  const [values, setValues] = React.useState<T>(opts.initial);
  const [status, setStatus] = React.useState<AutoSaveStatus>("idle");
  const [pendingInvalid, setPendingInvalid] = React.useState(false);

  // refs 让所有回调保持稳定身份,并且保存时读到最新值/最新 save/isValid。
  const valuesRef = React.useRef(values);
  const saveRef = React.useRef(opts.save);
  const isValidRef = React.useRef(opts.isValid);
  const timerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  // failedActionRef 是「当前显示的这次失败」本身，retry 重放的是它，而不是之后又发
  // 出去的别的动作（例如失败之后成功传了一张头像）。
  const failedActionRef = React.useRef<(() => Promise<unknown>) | null>(null);
  // dirtyRef：有一份还没落库的编辑。它和 timerRef 不是一回事——值不合法时保存被扣住
  // 不排定时器，缓冲仍然在，flush/卸载都要以它为准。
  const dirtyRef = React.useRef(false);
  // 串行化保存：同一时刻只允许一次保存在飞，后来的快照排在它后面，并且只留最新的
  // 一份（连点两次「上移」各带一份完整顺序，并发发出去谁后落库谁说了算）。
  const savingRef = React.useRef(false);
  const queuedRef = React.useRef<T | null>(null);
  // seq 让状态只由「最新一次派发」决定：慢的那次失败不能把新的成功涂回 error。
  const seqRef = React.useRef(0);

  // Sync refs after render using useLayoutEffect so they're available in callbacks
  React.useLayoutEffect(() => {
    valuesRef.current = values;
    saveRef.current = opts.save;
    isValidRef.current = opts.isValid;
  });

  const clearTimer = React.useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const run = React.useCallback(async (action: () => Promise<unknown>) => {
    const seq = ++seqRef.current;
    setStatus("saving");
    try {
      await action();
      if (seq === seqRef.current) setStatus("saved");
      return true;
    } catch {
      if (seq === seqRef.current) {
        failedActionRef.current = action;
        setStatus("error");
      }
      return false;
    }
  }, []);

  // drain 依次跑完在飞的那一份和排在它后面的最新快照，中间那些被合并掉。
  const drain = React.useCallback(
    async (first: T) => {
      savingRef.current = true;
      try {
        let next: T | null = first;
        while (next !== null) {
          const snapshot = next;
          queuedRef.current = null;
          const ok = await run(() => saveRef.current(snapshot));
          // 保存成功即覆盖了此前失败的那一份快照，没有可重试的东西了。
          if (ok) failedActionRef.current = null;
          next = queuedRef.current;
        }
      } finally {
        savingRef.current = false;
      }
    },
    [run],
  );

  const saveNow = React.useCallback(() => {
    clearTimer();
    const snapshot = valuesRef.current;
    const isValid = isValidRef.current;
    if (isValid && !isValid(snapshot)) {
      // 不发一次后端必拒的保存，但编辑要留着（dirtyRef 不清）：下一次 patch 把值改回
      // 合法时，缓冲的这些字段会随最新快照一起落库，用户不用重新输入。
      setPendingInvalid(true);
      return;
    }
    dirtyRef.current = false;
    if (savingRef.current) {
      queuedRef.current = snapshot;
      return;
    }
    void drain(snapshot);
  }, [clearTimer, drain]);

  const patch = React.useCallback(
    (partial: Partial<T>, patchOpts?: { immediate?: boolean }) => {
      const next = { ...valuesRef.current, ...partial };
      // 必须在此同步写 valuesRef：immediate patch 会紧接着同步调 saveNow()
      // 读取 valuesRef.current；下面的 useLayoutEffect 同步太晚（本次事件之后才跑），
      // 删掉这行会让即时保存读到合并前的旧值。
      valuesRef.current = next;
      setValues(next);
      dirtyRef.current = true;

      const isValid = isValidRef.current;
      if (isValid && !isValid(next)) {
        clearTimer();
        setPendingInvalid(true);
        return;
      }
      setPendingInvalid(false);

      if (patchOpts?.immediate) {
        saveNow();
      } else {
        clearTimer();
        timerRef.current = setTimeout(saveNow, AUTO_SAVE_DEBOUNCE_MS);
      }
    },
    [clearTimer, saveNow],
  );

  // flush 以「有没有没落库的编辑」为准，而不是「有没有排着定时器」：值不合法时保存被
  // 扣住、定时器早被清掉，此时 onBlur 的 flush 曾经是彻底的空操作。
  const flush = React.useCallback(() => {
    if (!dirtyRef.current) return;
    saveNow();
  }, [saveNow]);

  const wrap = React.useCallback(
    async <R>(fn: () => Promise<R>): Promise<R | null> => {
      let result: R | null = null;
      await run(async () => {
        result = await fn();
      });
      return result;
    },
    [run],
  );

  const retry = React.useCallback(() => {
    const action = failedActionRef.current;
    if (!action) return;
    void run(action).then((ok) => {
      if (ok) failedActionRef.current = null;
    });
  }, [run]);

  // 卸载（在树上点了另一个节点 / 离开页面）时把防抖中的编辑落库，而不是随定时器一起
  // 丢掉。值不合法那一份仍然发不出去（后端必拒），只能靠 pendingInvalid 的提示。
  React.useEffect(
    () => () => {
      if (dirtyRef.current) saveNow();
      clearTimer();
    },
    [clearTimer, saveNow],
  );

  return { values, patch, flush, wrap, status, pendingInvalid, retry };
}
