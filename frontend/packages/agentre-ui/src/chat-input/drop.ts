/** 拖入后被收编成图片附件的那一类结果。字段与 composer 的提交载荷同形。 */
export type DroppedImageAttachment = {
  dataUrl: string;
  mediaType: string;
  name: string;
};

export type DroppedImageItem = {
  path: string;
  kind: "image" | "path";
  name?: string;
  mediaType?: string;
  dataUrl?: string;
};

// 对齐 chat.tsx 的 CHAT_IMAGE_ACCEPT(image/png,image/jpeg,image/webp)。
const DROP_IMAGE_EXTENSIONS = ["png", "jpg", "jpeg", "webp"];

// 取路径最后一段的小写扩展名;无扩展名/以点开头/以点结尾 → ""。
function extensionOf(path: string): string {
  const base = path.split(/[\\/]/).pop() ?? "";
  const dot = base.lastIndexOf(".");
  if (dot <= 0 || dot === base.length - 1) return "";
  return base.slice(dot + 1).toLowerCase();
}

export function classifyDroppedPaths(paths: string[]): {
  imageCandidates: string[];
  plainPaths: string[];
} {
  const imageCandidates: string[] = [];
  const plainPaths: string[] = [];
  for (const p of paths) {
    if (DROP_IMAGE_EXTENSIONS.includes(extensionOf(p))) imageCandidates.push(p);
    else plainPaths.push(p);
  }
  return { imageCandidates, plainPaths };
}

// 把绝对路径拼成插入文本:含空白的路径用双引号包裹,空格分隔,末尾补一个空格。
export function formatPathsForInput(paths: string[]): string {
  if (paths.length === 0) return "";
  return paths.map((p) => (/\s/.test(p) ? `"${p}"` : p)).join(" ") + " ";
}

// resolveDroppedPaths 把拖入路径解析成 {要追加的图片附件, 要插入的文本}。
// 纯逻辑 + 注入的 readImages 依赖,便于单测;拖拽永不死路——所有非附件项降级为路径。
export async function resolveDroppedPaths(
  paths: string[],
  opts: {
    allowImages: boolean;
    remainingImageSlots: number;
    readImages: (imagePaths: string[]) => Promise<DroppedImageItem[]>;
  },
): Promise<{ attachments: DroppedImageAttachment[]; text: string }> {
  const { imageCandidates, plainPaths } = classifyDroppedPaths(paths);
  const toInsert = [...plainPaths];
  const attachments: DroppedImageAttachment[] = [];

  if (!opts.allowImages || imageCandidates.length === 0) {
    toInsert.push(...imageCandidates); // 图片也降级为路径
  } else {
    const items = await opts.readImages(imageCandidates);
    let slots = opts.remainingImageSlots;
    for (const item of items) {
      if (
        item.kind === "image" &&
        slots > 0 &&
        item.dataUrl &&
        item.mediaType &&
        item.name !== undefined
      ) {
        attachments.push({
          dataUrl: item.dataUrl,
          mediaType: item.mediaType,
          name: item.name,
        });
        slots--;
      } else {
        toInsert.push(item.path); // path 归类 或 配额溢出 → 降级
      }
    }
  }

  return { attachments, text: formatPathsForInput(toInsert) };
}
