// GFM 的裸链接(autolink literal)把「`http://` 之后直到空白为止」全都算进 URL,
// 收尾只裁 ASCII 标点。中文正文里 URL 后面紧跟的是 `），。` 和汉字,一个都裁不掉:
// 链接会一路划到句尾,href 里带着 percent-encode 的整句话,点开必然 404;URL 外层的
// `**强调**` 也会因为右定界符被吞进 URL 而失配,`**` 以字面量显示。
//
// 本包自己的 rehypeMarkdownAutolinks 有完整的全角边界表(HARD_END_BOUNDARY),
// 所以这里把 GFM 的 http/www 两条裸链接关掉,裸 URL 一律交给它。邮箱不在本包的
// 文法里,继续由 GFM 认——只关 URL,不关邮箱。
//
// GFM 走两条互相独立的路,**两条都要关**,只关一条会得到「localhost 好了、真实域名
// 还坏着」这种半修:
//
//  1. micromark 的 `protocolAutolink` / `wwwAutolink` 构造 —— 用 micromark 的
//     `disable` 名单按构造名关掉(create-tokenizer 在进构造前查这张表)。
//  2. mdast-util-gfm-autolink-literal 的 fromMarkdown `transforms` —— 解析完成后
//     再拿正则(`[^ \t\r\n]*` 吃路径)扫一遍文本节点,micromark 的 disable 管不到它。
//     只摘掉这个扩展的 `transforms`,`enter`/`exit` 处理器必须留下:仍然启用的
//     `emailAutolink` 构造要靠它们把 literalAutolink token 装配成 link 节点。

const DISABLED_CONSTRUCTS = ["protocolAutolink", "wwwAutolink"];

// unified / micromark 都不是本包的直接依赖(消费方也不该被迫装),这里按结构描述
// 用到的那几个字段就够。
type ProcessorData = {
  micromarkExtensions?: unknown[];
  fromMarkdownExtensions?: unknown[];
};

// `data()` 声明成 unknown 而不是 ProcessorData:unified 的 `Data` 是各插件声明合并
// 出来的接口,写死成本文件的形状会让这个插件无法赋给 `remarkPlugins`(Pluggable 的
// `this` 是完整的 Processor)。取值处再收窄。
type PluginContext = {
  data(): unknown;
};

type AutolinkLiteralExtension = { transforms?: unknown };

// 认这个扩展靠 `enter.literalAutolink` —— 它是 autolink literal 独有的 token 名,
// 也是 mdast-util-gfm 里唯一带 transforms 的那个扩展。
function isAutolinkLiteralExtension(
  value: unknown,
): value is AutolinkLiteralExtension {
  if (typeof value !== "object" || value === null) return false;
  const enter = (value as { enter?: unknown }).enter;
  return (
    typeof enter === "object" && enter !== null && "literalAutolink" in enter
  );
}

// remark-gfm 往 fromMarkdownExtensions 里 push 的是一个**数组**(gfmFromMarkdown()
// 返回多个扩展),所以要递归一层。
function stripAutolinkTransforms(list: readonly unknown[]): void {
  for (const item of list) {
    if (Array.isArray(item)) {
      stripAutolinkTransforms(item);
    } else if (isAutolinkLiteralExtension(item)) {
      delete item.transforms;
    }
  }
}

/**
 * 关掉 GFM 的 http/www 裸链接,把裸 URL 让给 rehypeMarkdownAutolinks。
 *
 * **必须排在 remark-gfm 之后**:第 2 步要摘的 fromMarkdown 扩展是 remark-gfm 在
 * `.use()` 时才挂上去的,排在前面会摘了个空。
 */
export function remarkDisableGfmUrlAutolinks(this: PluginContext): void {
  const data = this.data() as ProcessorData;
  const micromarkExtensions = (data.micromarkExtensions ??= []);
  micromarkExtensions.push({ disable: { null: [...DISABLED_CONSTRUCTS] } });
  stripAutolinkTransforms(data.fromMarkdownExtensions ?? []);
}
