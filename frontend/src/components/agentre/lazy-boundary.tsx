// 按需加载的视图壳：React.lazy + Suspense + 错误边界。
//
// 非聊天路由、设置对话框、命令面板、终端面板都不在启动包里，第一次用到才拉 chunk。
// chunk 拉取失败（请求被中断、磁盘上的包被换掉、窗口常驻很久后更新）时不能让整棵树
// 卸载成白屏，所以每个懒视图自带一层边界：显示「出错了 + 重试」。
//
// 重试 = 整页重载，不是在原地再 import 一次：浏览器按模块地址缓存失败的动态 import
// （module map），同一个地址再 import 拿到的永远是那次失败；React.lazy 也会记住被拒绝
// 的 promise。生产包里 import 已被改写成带哈希的 chunk 地址，没法加查询串换个地址
// 绕开缓存，只有换一个文档才有新的 module map。重载不丢东西：当前路由、打开的标签
// 都已持久化，会话状态在后端。
//
// Suspense 的 fallback 是 null：这些视图要么是浮层（打开前本就不可见），要么是 Outlet
// 里的页面（聊天视图常驻在 Outlet 之外，首屏不会被 fallback 挤动）。

import * as React from "react";
import { useTranslation } from "react-i18next";
import { Button, cn } from "@agentre-hub/agentre-ui";

type LazyBoundaryProps = {
  children: React.ReactNode;
  /** 点「重试」时调用：整页重载。 */
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

  render() {
    if (this.state.failed) {
      return (
        <LoadFailed onRetry={this.props.onRetry} overlay={this.props.overlay} />
      );
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

type LazyViewOptions = {
  /** 对话框 / 面板这类浮层：失败提示不占布局。 */
  overlay?: boolean;
  /** 「重试」做的整页重载；只有测试替换它。 */
  reload?: () => void;
};

function reloadDocument() {
  window.location.reload();
}

/** 把一个动态 import 包成「首次渲染才加载」的视图组件。 */
export function lazyView<
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- 组件的 props 类型由调用方的 import 决定，原样透传
  C extends React.ComponentType<any>,
>(
  load: () => Promise<{ default: C }>,
  { overlay, reload = reloadDocument }: LazyViewOptions = {},
): C {
  const Lazy = React.lazy(load);
  function LazyView(props: React.ComponentProps<C>) {
    return (
      <LazyBoundary onRetry={reload} overlay={overlay}>
        <React.Suspense fallback={null}>
          <Lazy {...props} />
        </React.Suspense>
      </LazyBoundary>
    );
  }
  return LazyView as unknown as C;
}
