/**
 * 让 tsc 认得组件里的 CSS 副作用 import。
 *
 * 这个包的构建是一句 `tsc`，而 tsc 不认识 `.css`：`import "./chat-input.css"`
 * 会被判成 TS2307「找不到模块」，整个 build 停在那里。声明成一个空模块之后
 * tsc 放行，而**语句本身原样发进 dist**（副作用 import 不会被擦除），宿主的
 * 打包器照常处理它——正是我们要的。
 *
 * 文件本身怎么进 dist：`scripts/copy-assets.mjs` 搬 src 下所有非 .ts/.tsx 文件，
 * 由 src/build-assets.test.ts 机械保证不漏。
 */
declare module "*.css";
