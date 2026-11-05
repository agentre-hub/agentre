// 按需加载的视图壳：React.lazy + Suspense + 错误边界。
//
// 非聊天路由、设置对话框、命令面板、终端面板都不在启动包里，第一次用到才拉 chunk。
// chunk 拉取失败（磁盘上的包被换掉、窗口常驻很久后更新）时不能让整棵树卸载成白屏，
// 所以每个懒视图自带一层边界：显示「出错了 + 重试」，重试 = 重建 lazy 再 import 一次
// （React.lazy 会缓存被拒绝的 promise，不重建的话重试永远是同一个失败）。
//
// Suspense 的 fallback 是 null：这些视图要么是浮层（打开前本就不可见），要么是 Outlet
// 里的页面（聊天视图常驻在 Outlet 之外，首屏不会被 fallback 挤动）。

import * as React from "react";
import { useTranslation } from "react-i18next";
import { Button, cn } from "@agentre-hub/agentre-ui";

type LazyBoundaryProps = {
  children: React.ReactNode;
  /** 点「重试」时先调用：宿主在这里重建 lazy 组件。 */
  onRetry: () => void;
  /** 浮层视图（对话框 / 面板）用固定定位的提示条，不占布局。 */
  overlay?: boolean;
};

type LazyBoundaryState = { failed: boolean };

class LazyBoundary extends React.Component<
  LazyBoundaryProps,
  LazyBoundaryState
> {
  state: LazyBoundaryState = { failed: false };

  static getDerivedStateFromError(): LazyBoundaryState {
    return { failed: true };
  }

  private retry = () => {
    this.props.onRetry();
    this.setState({ failed: false });
  };

  render() {
    if (this.state.failed) {
      return <LoadFailed onRetry={this.retry} overlay={this.props.overlay} />;
    }
    return this.props.children;
  }
}

function LoadFailed({
  onRetry,
  overlay,
}: {
  onRetry: () => void;
  overlay?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div
      role="alert"
      className={cn(
        "flex items-center gap-3 text-sm text-muted-foreground",
        overlay
          ? "fixed right-4 bottom-10 z-50 rounded-lg border bg-popover px-3 py-2 shadow-md"
          : "min-h-0 min-w-0 flex-1 items-center justify-center",
      )}
    >
      <span>{t("common.errorOccurred")}</span>
      <Button size="sm" variant="outline" onClick={onRetry}>
        {t("common.retry")}
      </Button>
    </div>
  );
}

/**
 * 把一个动态 import 包成「首次渲染才加载」的视图组件。
 * `overlay` 用于对话框 / 面板这类浮层，失败提示不占布局。
 */
export function lazyView<
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- 组件的 props 类型由调用方的 import 决定，原样透传
  C extends React.ComponentType<any>,
>(load: () => Promise<{ default: C }>, options: { overlay?: boolean } = {}): C {
  let Lazy = React.lazy(load);
  function LazyView(props: React.ComponentProps<C>) {
    // 重建 lazy 之后要让本组件重渲染一次，Suspense 里才拿得到新的 Lazy 引用。
    const [, rerender] = React.useReducer((n: number) => n + 1, 0);
    const reset = () => {
      Lazy = React.lazy(load);
      rerender();
    };
    return (
      <LazyBoundary onRetry={reset} overlay={options.overlay}>
        <React.Suspense fallback={null}>
          <Lazy {...props} />
        </React.Suspense>
      </LazyBoundary>
    );
  }
  return LazyView as unknown as C;
}
