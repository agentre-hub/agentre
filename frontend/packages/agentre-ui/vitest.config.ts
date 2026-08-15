import { defineConfig } from "vitest/config";

/**
 * 包自己的 `pnpm test` 入口。
 *
 * 平时这些用例是被宿主 app 的 vitest 一并收走跑的（根 vite.config.ts 的默认
 * include 覆盖到 packages/），宿主提供了 happy-dom 环境。但包的 package.json 里
 * 声明了 `"test": "vitest run"`，独立跑时若没有这份配置就会因为没有 DOM 而在
 * `render()` 处炸——一个声明了却跑不起来的脚本比没有更糟。
 *
 * setupFiles 用包自己的那份（只做 RTL cleanup），不复用宿主的：宿主那份装的是
 * wails mock 等宿主专属的东西，包按定义不该依赖它们（有 boundary.test.ts 机械保证）。
 */
export default defineConfig({
  test: {
    environment: "happy-dom",
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
    setupFiles: ["./vitest.setup.ts"],
  },
});
