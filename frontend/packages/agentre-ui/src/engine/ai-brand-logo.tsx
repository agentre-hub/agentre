// @ts-nocheck
import type { CSSProperties } from "react";

import agentreLogo from "./assets/images/logo-mark.png";
import anthropicLogo from "./assets/brands/anthropic.svg";
import claudeLogo from "./assets/brands/claude.svg";
import codexLogo from "./assets/brands/codex.svg";
import deepSeekLogo from "./assets/brands/deepseek.svg";
import glmLogo from "./assets/brands/glm.svg";
import geminiLogo from "./assets/brands/gemini.svg";
import kimiLogo from "./assets/brands/kimi.svg";
import metaLogo from "./assets/brands/meta.svg";
import minimaxLogo from "./assets/brands/minimax.svg";
import mistralLogo from "./assets/brands/mistral.svg";
import openaiLogo from "./assets/brands/openai.svg";
import openClawLogo from "./assets/brands/openclaw.svg";
import piLogo from "./assets/brands/pi.svg";
import qwenLogo from "./assets/brands/qwen.svg";
import xaiLogo from "./assets/brands/xai.svg";
import { cn } from "../lib/utils";

type Brand =
  | "agentre"
  | "anthropic"
  | "claude"
  | "codex"
  | "deepseek"
  | "gemini"
  | "glm"
  | "kimi"
  | "meta"
  | "minimax"
  | "mistral"
  | "openai"
  | "openclaw"
  | "pi"
  | "qwen"
  | "xai";

type LogoProps = {
  className?: string;
};

type BrandDefinition = {
  label: string;
  src: string;
  monochrome?: boolean;
  modelPatterns?: RegExp[];
  identityPatterns?: RegExp[];
};

const brandRegistry: Record<Brand, BrandDefinition> = {
  agentre: { label: "Agentre", src: agentreLogo },
  anthropic: {
    label: "Anthropic",
    src: anthropicLogo,
    monochrome: true,
  },
  claude: {
    label: "Claude",
    src: claudeLogo,
    modelPatterns: [/^claude(?:-|$)/],
  },
  codex: {
    label: "Codex",
    src: codexLogo,
    modelPatterns: [/^codex(?:-|$)/],
  },
  deepseek: {
    label: "DeepSeek",
    src: deepSeekLogo,
    modelPatterns: [/^deepseek(?:-|$)/],
    identityPatterns: [/deepseek/],
  },
  gemini: {
    label: "Gemini",
    src: geminiLogo,
    modelPatterns: [/^gemini(?:-|$)/],
    identityPatterns: [
      /\b(?:gemini|google ai)\b/,
      /generativelanguage\.googleapis\.com/,
    ],
  },
  glm: {
    label: "GLM",
    src: glmLogo,
    modelPatterns: [/^(?:glm|codegeex)(?:-|$)/],
    identityPatterns: [/\b(?:glm|zhipu|bigmodel)\b/],
  },
  kimi: {
    label: "Kimi",
    src: kimiLogo,
    monochrome: true,
    modelPatterns: [/^(?:kimi|moonshot)(?:-|$)/],
    identityPatterns: [/\b(?:kimi|moonshot)\b/],
  },
  meta: {
    label: "Meta",
    src: metaLogo,
    modelPatterns: [/^llama(?:-|$)/],
    identityPatterns: [/\b(?:meta ai|llama)\b/],
  },
  minimax: {
    label: "MiniMax",
    src: minimaxLogo,
    modelPatterns: [/^minimax(?:-|$)/, /^abab(?:\d|[-.]|$)/],
    identityPatterns: [
      /\bminimax\b/,
      /api\.minimax\.chat/,
      /(?:api|platform)\.minimaxi\.com/,
    ],
  },
  mistral: {
    label: "Mistral AI",
    src: mistralLogo,
    modelPatterns: [/^(?:mistral|mixtral|codestral)(?:-|$)/],
    identityPatterns: [/\b(?:mistral|mixtral|codestral)\b/, /api\.mistral\.ai/],
  },
  openai: {
    label: "OpenAI",
    src: openaiLogo,
    monochrome: true,
    modelPatterns: [/^(?:gpt|chatgpt)(?:-|$)/, /^o\d+(?:-|$)/],
  },
  openclaw: { label: "OpenClaw", src: openClawLogo },
  pi: { label: "Pi", src: piLogo },
  qwen: {
    label: "Qwen",
    src: qwenLogo,
    modelPatterns: [/^qwen(?:\d|-|$)/, /^qwq(?:-|$)/],
    identityPatterns: [
      /\b(?:qwen|dashscope|alibaba cloud)\b/,
      /dashscope\.aliyuncs\.com/,
    ],
  },
  xai: {
    label: "xAI",
    src: xaiLogo,
    monochrome: true,
    modelPatterns: [/^grok(?:-|$)/],
    identityPatterns: [/\b(?:xai|x\.ai|grok)\b/, /api\.x\.ai/],
  },
};

const brandEntries = Object.entries(brandRegistry) as Array<
  [Brand, BrandDefinition]
>;

const backendBrands: Record<string, Brand> = {
  builtin: "agentre",
  claudecode: "claude",
  codex: "codex",
  piagent: "pi",
  openclaw: "openclaw",
};

function BrandLogo({ brand, className }: LogoProps & { brand: Brand }) {
  const meta = brandRegistry[brand];
  return (
    <span
      role="img"
      aria-label={meta.label}
      data-brand={brand}
      className={cn(
        "inline-flex size-5 shrink-0 items-center justify-center overflow-hidden rounded-sm",
        className,
      )}
    >
      {meta.monochrome ? (
        <span
          aria-hidden="true"
          className="block size-full bg-foreground [mask-image:var(--brand-logo)] [mask-position:center] [mask-repeat:no-repeat] [mask-size:contain] [-webkit-mask-image:var(--brand-logo)] [-webkit-mask-position:center] [-webkit-mask-repeat:no-repeat] [-webkit-mask-size:contain]"
          style={
            {
              "--brand-logo": `url("${meta.src}")`,
            } as CSSProperties
          }
        />
      ) : (
        <img src={meta.src} alt="" className="size-full object-contain" />
      )}
    </span>
  );
}

export function AgentBackendLogo({
  backendType,
  className,
}: LogoProps & { backendType: string }) {
  const brand = backendBrands[backendType];
  return brand ? (
    <BrandLogo brand={brand} className={className} />
  ) : (
    <TextLogo value={backendType} className={className} />
  );
}

export function resolveModelBrand(model: string): Brand | null {
  const normalized = model.trim().toLowerCase();
  return (
    brandEntries.find(([, definition]) =>
      definition.modelPatterns?.some((pattern) => pattern.test(normalized)),
    )?.[0] ?? null
  );
}

function resolveProviderBrand(
  providerType: string,
  providerName = "",
  baseUrl = "",
): Brand | null {
  const identity = `${providerName} ${baseUrl}`.toLowerCase();
  const identified = brandEntries.find(([, definition]) =>
    definition.identityPatterns?.some((pattern) => pattern.test(identity)),
  )?.[0];
  if (identified) return identified;
  if (providerType === "anthropic") return "anthropic";
  if (providerType === "openai-chat" || providerType === "openai-response") {
    return "openai";
  }
  return null;
}

export function LlmProviderLogo({
  providerType,
  providerName = "",
  baseUrl = "",
  className,
}: LogoProps & {
  providerType: string;
  providerName?: string;
  baseUrl?: string;
}) {
  const brand = resolveProviderBrand(providerType, providerName, baseUrl);
  return brand ? (
    <BrandLogo brand={brand} className={className} />
  ) : (
    <TextLogo value={providerType} className={className} />
  );
}

export function LlmModelLogo({
  model,
  providerType,
  providerName = "",
  baseUrl = "",
  className,
}: LogoProps & {
  model: string;
  providerType: string;
  providerName?: string;
  baseUrl?: string;
}) {
  const brand =
    resolveModelBrand(model) ??
    resolveProviderBrand(providerType, providerName, baseUrl);
  return brand ? (
    <BrandLogo brand={brand} className={className} />
  ) : (
    <TextLogo value={model || providerType} className={className} />
  );
}

function TextLogo({ value, className }: LogoProps & { value: string }) {
  const words = value.trim().replace(/[-_]+/g, " ");
  const label = words
    ? words.charAt(0).toUpperCase() + words.slice(1).toLowerCase()
    : "Model";
  return (
    <span
      role="img"
      aria-label={label}
      className={cn(
        "inline-flex size-5 shrink-0 items-center justify-center rounded-sm bg-muted font-mono text-2xs font-bold text-muted-foreground",
        className,
      )}
    >
      {label.charAt(0)}
    </span>
  );
}
