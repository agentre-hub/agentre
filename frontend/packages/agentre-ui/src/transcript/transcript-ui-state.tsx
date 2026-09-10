import * as React from "react";

type TranscriptUIState = {
  getBoolean: (key: string, fallback: boolean) => boolean;
  setBoolean: (key: string, value: boolean) => void;
  subscribe: (listener: () => void) => () => void;
};

const TranscriptUIStateContext = React.createContext<TranscriptUIState | null>(
  null,
);

export function TranscriptUIStateProvider({
  children,
}: {
  children: React.ReactNode;
}) {
  const valuesRef = React.useRef(new Map<string, boolean>());
  const listenersRef = React.useRef(new Set<() => void>());
  const api = React.useMemo<TranscriptUIState>(
    () => ({
      getBoolean: (key, fallback) => valuesRef.current.get(key) ?? fallback,
      setBoolean: (key, value) => {
        valuesRef.current.set(key, value);
        listenersRef.current.forEach((listener) => listener());
      },
      subscribe: (listener) => {
        listenersRef.current.add(listener);
        return () => {
          listenersRef.current.delete(listener);
        };
      },
    }),
    [],
  );

  return (
    <TranscriptUIStateContext.Provider value={api}>
      {children}
    </TranscriptUIStateContext.Provider>
  );
}

export function useTranscriptBooleanState(
  key: string | undefined,
  fallback: boolean,
): [boolean, React.Dispatch<React.SetStateAction<boolean>>] {
  const store = React.useContext(TranscriptUIStateContext);
  const [localValue, setLocalValue] = React.useState(fallback);
  const getSnapshot = React.useCallback(
    () => (key && store ? store.getBoolean(key, fallback) : localValue),
    [fallback, key, localValue, store],
  );
  const subscribe = React.useCallback(
    (listener: () => void) => (key && store ? store.subscribe(listener) : noop),
    [key, store],
  );
  const value = React.useSyncExternalStore(subscribe, getSnapshot, getSnapshot);

  const setValue = React.useCallback<
    React.Dispatch<React.SetStateAction<boolean>>
  >(
    (next) => {
      const prev = key && store ? store.getBoolean(key, fallback) : localValue;
      const resolved = typeof next === "function" ? next(prev) : next;
      if (key && store) {
        store.setBoolean(key, resolved);
      } else {
        setLocalValue(resolved);
      }
    },
    [fallback, key, localValue, store],
  );

  return [value, setValue];
}

function noop() {
  return undefined;
}
