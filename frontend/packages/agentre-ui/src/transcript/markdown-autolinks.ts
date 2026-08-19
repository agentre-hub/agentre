import { isLocalFileURL } from "../lib/link-classify";

import { previewKind } from "../lib/previewable";

export const MARKDOWN_AUTOLINK_TAG = "markdown-autolink";

export type MarkdownAutoLinkSegment =
  | { type: "text"; value: string }
  | { type: "link"; value: string; href: string };

type HastNode = {
  type: string;
  tagName?: string;
  value?: string;
  properties?: Record<string, unknown>;
  children?: HastNode[];
};

const URL_PREFIX = /^(?:https?:\/\/|mailto:|tel:)/i;
const WWW_PREFIX = /^www\./i;
const ABS_POSIX = /^\//;
const ABS_WINDOWS = /^[A-Za-z]:[\\/]/;
const SCHEME = /^[A-Za-z][A-Za-z0-9+.-]*:/;
const LINE_SUFFIX = /:(\d+)(?::(\d+))?$/;
const NUMERIC_PATH = /^\d+(?:[\\/]\d+)+[\\/]?$/;
const DOMAIN_PATH = /^[^\\/\s]+\.[A-Za-z]{2,}[\\/]/;
const OPEN_BOUNDARY = new Set([
  "(",
  "'",
  '"',
  "“",
  "‘",
  "[",
  "{",
  "<",
  "（",
  "【",
  "《",
]);
const HARD_END_BOUNDARY = new Set([
  ",",
  ";",
  "!",
  "，",
  "。",
  "；",
  "！",
  "？",
  "、",
  "：",
  ")",
  "]",
  "}",
  "）",
  "】",
  "》",
  ">",
  "”",
  "’",
]);
const TRAILING_PUNCTUATION = /[.?:]+$/;

function pathWithoutLineSuffix(value: string): string {
  const match = LINE_SUFFIX.exec(value);
  return match ? value.slice(0, match.index) : value;
}

// 枚举式多段「a/b/c」:各段都是单字符、末段无扩展名且不以分隔符结尾。
// 几乎总是「a/b/c 三处」这类并列枚举,而不是真实目录;frontend/src/components、
// x/y/z.txt 等真实多段路径不受影响。
function isSingleCharEnumeration(path: string): boolean {
  if (/[\\/]$/.test(path)) return false;
  const segments = path.split(/[\\/]/).filter(Boolean);
  if (segments.length < 2) return false;
  const basename = segments[segments.length - 1];
  if (basename.includes(".")) return false;
  return segments.every((segment) => segment.length === 1);
}

function isRelativeTarget(value: string, cwd?: string): boolean {
  if (!cwd || SCHEME.test(value) || value.startsWith("#")) return false;

  const path = pathWithoutLineSuffix(value);
  if (path === "" || NUMERIC_PATH.test(path) || DOMAIN_PATH.test(path)) {
    return false;
  }

  if (/^\.\.?[\\/]/.test(path)) return true;

  const separators = path.match(/[\\/]/g)?.length ?? 0;
  if (separators === 0) return previewKind(path) !== null;
  if (isSingleCharEnumeration(path)) return false;
  if (/[\\/]$/.test(path) || separators >= 2) return true;

  const basename = path.split(/[\\/]/).pop() ?? "";
  const dot = basename.lastIndexOf(".");
  return dot > 0 && dot < basename.length - 1;
}

function isMarkdownAutoLinkTarget(value: string, cwd?: string): boolean {
  if (value === "") return false;
  // 根目录单字符(/ 或 \)是「A / B」并列或口语里的根路径引用,不是可点击目标:
  // doSend / Regenerate 中间的斜杠会被误渲染成目录。
  if (value === "/" || value === "\\") return false;
  if (URL_PREFIX.test(value) || WWW_PREFIX.test(value)) {
    return !/\s/.test(value);
  }
  const firstWhitespace = value.search(/\s/);
  if (firstWhitespace >= 0) {
    const firstSeparator = value.search(/[\\/]/);
    if (firstSeparator < 0 || firstWhitespace < firstSeparator) return false;
  }
  if (
    isLocalFileURL(value) ||
    ABS_POSIX.test(value) ||
    ABS_WINDOWS.test(value)
  ) {
    return true;
  }
  return isRelativeTarget(value, cwd);
}

function pushText(segments: MarkdownAutoLinkSegment[], value: string): void {
  if (value === "") return;
  const previous = segments.at(-1);
  if (previous?.type === "text") {
    previous.value += value;
    return;
  }
  segments.push({ type: "text", value });
}

function isBoundaryPunctuation(value: string): boolean {
  return OPEN_BOUNDARY.has(value) || HARD_END_BOUNDARY.has(value);
}

function isStartBoundary(text: string, index: number): boolean {
  if (index === 0) return true;
  const previous = text[index - 1];
  return (
    /\s/.test(previous) ||
    previous === ":" ||
    previous === "?" ||
    isBoundaryPunctuation(previous)
  );
}

function followsSchemeColon(text: string, index: number): boolean {
  if (index === 0 || text[index - 1] !== ":") return false;
  let start = index - 2;
  while (start >= 0 && /[A-Za-z0-9+.-]/.test(text[start])) start -= 1;
  return SCHEME.test(text.slice(start + 1, index));
}

function isCandidateBoundary(text: string, index: number): boolean {
  if (index >= text.length) return true;
  const char = text[index];
  return (
    /\s/.test(char) ||
    char === '"' ||
    char === "'" ||
    HARD_END_BOUNDARY.has(char) ||
    TRAILING_PUNCTUATION.test(char)
  );
}

function candidateEnd(text: string, start: number): number {
  const source = text.slice(start);
  const urlTarget = URL_PREFIX.test(source) || WWW_PREFIX.test(source);
  const fileTarget = isLocalFileURL(source);
  const windowsTarget = ABS_WINDOWS.test(source);
  let end = start;
  while (end < text.length) {
    const char = text[end];
    if (/\s/.test(char) || char === '"' || char === "'") break;
    // 开括号(ASCII + 全角)也是硬边界:它们既允许紧跟在后面的目标(起始边界),
    // 也会在 token 中途出现——「四条已落地（#1/#2/#5/#6）」里的（ 不能被吞进候选,
    // 否则整句会被当成相对路径渲染成目录。
    if (end > start && isBoundaryPunctuation(char)) break;
    if (!urlTarget && (char === ":" || char === "?")) {
      const schemeColon = fileTarget && end === start + "file".length;
      const driveColon = windowsTarget && end === start + 1;
      if (schemeColon || driveColon) {
        end += 1;
        continue;
      }
      if (char === ":") {
        const lineSuffix = /^:\d+(?::\d+)?/.exec(text.slice(end));
        if (
          lineSuffix &&
          isCandidateBoundary(text, end + lineSuffix[0].length)
        ) {
          end += lineSuffix[0].length;
          continue;
        }
      }
      break;
    }
    end += 1;
  }
  return end;
}

function trimCandidate(value: string): string {
  return value.replace(TRAILING_PUNCTUATION, "");
}

function followsUnquotedPathFragment(text: string, start: number): boolean {
  let index = start - 1;
  if (index < 0 || !/\s/.test(text[index])) return false;
  while (index >= 0 && /\s/.test(text[index])) index -= 1;
  const end = index + 1;
  while (
    index >= 0 &&
    !/\s/.test(text[index]) &&
    !OPEN_BOUNDARY.has(text[index]) &&
    !HARD_END_BOUNDARY.has(text[index])
  ) {
    index -= 1;
  }
  return /[\\/]/.test(text.slice(index + 1, end));
}

function precedesUnquotedPathFragment(
  text: string,
  value: string,
  end: number,
): boolean {
  const path = pathWithoutLineSuffix(value);
  if (/[\\/]$/.test(path) || previewKind(path) !== null) return false;

  let index = end;
  if (index >= text.length || !/\s/.test(text[index])) return false;
  while (index < text.length && /\s/.test(text[index])) index += 1;
  const fragmentEnd = candidateEnd(text, index);
  const fragment = trimCandidate(text.slice(index, fragmentEnd));
  if (fragment === "") return false;
  const fragmentPath = pathWithoutLineSuffix(fragment);
  return /[\\/]/.test(fragmentPath) || previewKind(fragmentPath) !== null;
}

function quotedTargetAt(
  text: string,
  start: number,
  cwd?: string,
): { quote: string; value: string; end: number } | null {
  const quote = text[start];
  if (quote !== '"' && quote !== "'") return null;
  const end = text.indexOf(quote, start + 1);
  if (end < 0) return null;
  const value = text.slice(start + 1, end);
  if (!isMarkdownAutoLinkTarget(value, cwd)) return null;
  return { quote, value, end: end + 1 };
}

export function tokenizeMarkdownAutoLinks(
  text: string,
  cwd?: string,
): MarkdownAutoLinkSegment[] {
  const segments: MarkdownAutoLinkSegment[] = [];
  let plainStart = 0;
  let index = 0;

  const flushPlain = (end: number) => {
    pushText(segments, text.slice(plainStart, end));
  };

  while (index < text.length) {
    const quoted = quotedTargetAt(text, index, cwd);
    if (quoted) {
      flushPlain(index);
      pushText(segments, quoted.quote);
      segments.push({ type: "link", value: quoted.value, href: quoted.value });
      pushText(segments, quoted.quote);
      index = quoted.end;
      plainStart = index;
      continue;
    }

    if (
      !isStartBoundary(text, index) ||
      followsSchemeColon(text, index) ||
      text[index] === ":" ||
      isBoundaryPunctuation(text[index])
    ) {
      index += 1;
      continue;
    }

    const rawEnd = candidateEnd(text, index);
    const raw = text.slice(index, rawEnd);
    const candidate = trimCandidate(raw);
    const candidateEndIndex = index + candidate.length;
    const pathCandidate =
      !URL_PREFIX.test(candidate) && !WWW_PREFIX.test(candidate);
    const relativePathCandidate =
      pathCandidate &&
      !isLocalFileURL(candidate) &&
      !ABS_POSIX.test(candidate) &&
      !ABS_WINDOWS.test(candidate);

    if (
      candidate !== "" &&
      isMarkdownAutoLinkTarget(candidate, cwd) &&
      !(
        (relativePathCandidate && followsUnquotedPathFragment(text, index)) ||
        (pathCandidate && precedesUnquotedPathFragment(text, candidate, rawEnd))
      )
    ) {
      flushPlain(index);
      segments.push({ type: "link", value: candidate, href: candidate });
      index = candidateEndIndex;
      plainStart = index;
      continue;
    }

    index = Math.max(index + 1, rawEnd);
  }

  flushPlain(text.length);
  return segments;
}

function autolinkNode(
  segment: Extract<MarkdownAutoLinkSegment, { type: "link" }>,
): HastNode {
  return {
    type: "element",
    tagName: MARKDOWN_AUTOLINK_TAG,
    properties: { href: segment.href },
    children: [{ type: "text", value: segment.value }],
  };
}

function decorateTextChildren(
  node: HastNode,
  cwd: string | undefined,
  protectedTags: ReadonlySet<string>,
): void {
  if (node.type === "element") {
    const tagName = node.tagName ?? "";
    if (
      tagName === "a" ||
      tagName === "img" ||
      tagName === "pre" ||
      tagName === MARKDOWN_AUTOLINK_TAG ||
      protectedTags.has(tagName)
    ) {
      return;
    }

    if (tagName === "code") {
      const children = node.children ?? [];
      if (
        children.length === 1 &&
        children[0].type === "text" &&
        typeof children[0].value === "string" &&
        isMarkdownAutoLinkTarget(children[0].value, cwd)
      ) {
        node.children = [
          autolinkNode({
            type: "link",
            value: children[0].value,
            href: children[0].value,
          }),
        ];
      }
      return;
    }
  }

  const children = node.children;
  if (!children) return;
  for (let index = 0; index < children.length; ) {
    const child = children[index];
    if (child.type !== "text" || typeof child.value !== "string") {
      decorateTextChildren(child, cwd, protectedTags);
      index += 1;
      continue;
    }

    const segments = tokenizeMarkdownAutoLinks(child.value, cwd);
    if (!segments.some((segment) => segment.type === "link")) {
      index += 1;
      continue;
    }
    const replacement = segments.map(
      (segment): HastNode =>
        segment.type === "text"
          ? { type: "text", value: segment.value }
          : autolinkNode(segment),
    );
    children.splice(index, 1, ...replacement);
    index += replacement.length;
  }
}

export function rehypeMarkdownAutolinks(
  cwd?: string,
  protectedTags: readonly string[] = [],
) {
  const protectedTagSet = new Set(protectedTags);
  return () => (tree: HastNode) => {
    decorateTextChildren(tree, cwd, protectedTagSet);
  };
}
