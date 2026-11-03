# 预设头像素材：生成与清理方法

## 职业 / 项目 / 部门三组（48 张）：gpt-image-2.5-sunburst 生成（pi huu provider，/v1/images/generations，1024x1024 一张 4×4 拼图）

- 切图：`python3 slice2.py <sheet.png> <outdir> <prefix>`——按白色间隔切 4×4，每张按贴纸实际边界（fuzz 12%）裁紧，按角落底色补成正方形（边长 = max(bw,bh)×1.16），输出 128px webp（quality 88）。脚本里的临时路径 /tmp/presetgen 可改。
- 拼图源：a-bot2.png（职业）、p-soft.png（项目）、d-team.png（部门）。

STYLE（职业 / 部门共用）:
"Die-cut sticker style, optimized for tiny sizes: chunky shapes, one thick dark outline only around the silhouette, very few interior lines, flat soft marker colors, thick white sticker border, no sparkle or motion marks, each tile on its own pastel solid background color. IMPORTANT: generous empty margin, the whole sticker including its white border sits well inside the tile and fills only about 62 percent of it, nothing touches or crosses the tile edge. Bold distinct silhouette so every tile stays recognizable when shrunk to 16 pixels. 4x4 grid, tiles separated by uniform white gutters, no tile borders, no text, no words, no letters, consistent style across all 16. Clean and friendly, suitable for a professional developer tool, not childish, not game-like, no animals."

MASCOT: "the same small friendly abstract AI spirit mascot: a soft rounded blob-orb body with a big simple face (two dot eyes, small smile) and tiny stubby hands"

职业 a-bot2: "A 4x4 grid sheet of 16 sticker avatars of ONE consistent brand mascot, $MASCOT. The face is large and is the focus; the job accessory is small and secondary, held at the side. Every tile uses a DIFFERENT body color, no repeats, in this order with these accessories: 1 sky blue, round glasses and tablet; 2 violet, rolled blueprint; 3 sunflower yellow, magnifying glass; 4 mint green, clipboard with check marks; 5 tangerine orange, bug net; 6 hot pink, paintbrush; 7 teal, database disk; 8 cobalt blue, yellow hard hat; 9 lavender, shield with padlock; 10 lime green, pen and paper; 11 coral red, small bar chart; 12 indigo, stacked blocks; 13 amber, stopwatch; 14 rose, sticky notes; 15 aqua cyan, speech bubble; 16 charcoal grey, headphones and tiny terminal. Backgrounds are light pastel tints that contrast with each body color. $STYLE"

部门 d-team: "A 4x4 grid sheet of 16 sticker icons for TEAM DEPARTMENTS: each tile shows a small huddle of two or three of $MASCOT (in different colors) standing together behind one shared team sign object that names the department visually, in this order: 1 code angle brackets sign, 2 browser window, 3 server rack, 4 big check mark flag, 5 paint palette, 6 gear, 7 padlock, 8 bar chart board, 9 bullseye target, 10 open book, 11 megaphone, 12 headset, 13 lightbulb, 14 stacked layers, 15 smartphone, 16 small trophy. $STYLE"

项目 p-soft（STYLE 变体）:
STYLE_P="Die-cut sticker style, optimized for tiny sizes: chunky shapes, one thick dark outline only around the silhouette, very few interior lines, flat soft pastel-leaning marker colors (same soft palette as a cute pastel mascot set, not saturated), thick white sticker border, no sparkle marks, each tile on its own light pastel solid background tint. Generous empty margin: the sticker fills only about 62 percent of the tile and nothing touches the tile edge. 4x4 grid, tiles separated by uniform white gutters, no tile borders, no text, no words, no letters, no logos, consistent style across all 16. Clean and friendly, suitable for a professional developer tool."
"A 4x4 grid sheet of 16 sticker icons for kinds of software projects, each one bold object, in this order: 1 terminal window with a prompt chevron, 2 git branch graph with three nodes, 3 web browser window with a globe, 4 smartphone, 5 server rack, 6 database cylinder, 7 electric plug with cable (API), 8 cardboard package box, 9 stacked shipping containers, 10 cloud, 11 microchip, 12 friendly robot head, 13 open book, 14 dashboard gauge, 15 game controller, 16 puzzle piece. $STYLE_P"

## 模型 / 工具组（16 张）：确定性生成，不经模型

源 = 仓库内官方 logo `frontend/packages/agentre-ui/src/engine/assets/brands/*`。
1. `node raster.mjs`：headless Chromium（e2e 的 @playwright/test）把每个 svg/png 渲成 560px 透明 png（用 file:// 页面 goto，setContent 载不了 file:// 图）。
2. `./stk.sh r-<name>.png b-NN-<name> <粉彩底> <单色填充或空>`：dilate Disk:9 出 #26262b 深色描边、Disk:14 出白色贴纸边，强制 sRGB/PNG32，512 底上 logo 缩到 384，再缩 128 webp q90。
参数（顺序即 brand-01..16）：
claude #f6ddd0 - | anthropic #efe6da #191919 | openai #e6e8ec #111111 | codex #dfe3ee - | pi #ece6f5 - | openclaw #fadcdc - | hermes #f6ecd2 - | deepseek #dce6fb - | glm #dfe3fa - | gemini #e5e4fb - | qwen #e8e3fb - | kimi #e4e4ea #111111 | minimax #fbe0e7 - | mistral #fbe9d3 - | xai #e2e2e7 #111111 | meta #dbe8fb -
（"-" = 不填充，保留原色）官方 logo 更新时按此重跑。

## 入库前清理（48 张生成素材）

`clean_edges.py <输入目录> <输出目录>`：以每张左上角像素为底色，把与底色差异 >12（RGB 三通道绝对差之和）的像素按 8 邻域连通分量分组，保留最大的一块（贴纸本体，含白边与描边），把距它 2px 以外的其余非底色像素（相邻格残边、间隔杂色、JPEG 式噪点）统一刷回底色，使贴纸外只剩一块纯色粉彩底；不重新生成、不改贴纸本体。品牌组是确定性产物，不清理。

## 脚本

- `gen.py`：调图像生成接口的薄封装；运行时从使用者本机的 pi 配置读取密钥，文件本身不含任何密钥，也不要把密钥写进仓库。
- `slice2.py` / `clean_edges.py`：切图与清理（需要 ImageMagick `magick`、Python3 + Pillow + numpy）。
- `raster.mjs` / `stk.sh`：品牌组渲染与贴纸外框。`raster.mjs` 里的 logo 目录与 playwright 路径是作者机器上的绝对路径，重跑时按本机改；logo 目录是 `src/engine/assets/brands/`。
- 脚本与本说明不进发布包（`package.json` 的 `files` 只含 `src` 与 `styles`）。
