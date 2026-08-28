import {
  forwardRef,
  memo,
  useCallback,
  useEffect,
  useId,
  useImperativeHandle,
  useMemo,
  useRef,
  type RefObject,
} from "react";

import Document from "@tiptap/extension-document";
import HardBreak from "@tiptap/extension-hard-break";
import Paragraph from "@tiptap/extension-paragraph";
import Text from "@tiptap/extension-text";
import { Placeholder, UndoRedo } from "@tiptap/extensions";
import { EditorContent, useEditor, type Editor } from "@tiptap/react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

import { parsePlainTextClipboard } from "./clipboard";
import { ComposerPopovers } from "./composer-popovers";
import { extractPlainText } from "./content";
import { handleComposerKeyDown, handleComposerUpdate } from "./editor-handlers";
import { composerCapabilities, composerPlaceholder } from "./placeholder";
import { applyInputHistoryMessage } from "./keyboard";
import { submitLocalCommand } from "./local-command-submit";
import { useOptionalLocalCommandHistoryAccess } from "./local-command-history/access";
import { useLocalCommandHistoryCombobox } from "./local-command-history/use-history-combobox";
import { useLocalCommandHistoryMenu } from "./local-command-history/use-local-command-history-menu";
import { SlashHighlight } from "./slash/slash-highlight";
import type { SlashCommand, SlashExec } from "./slash/types";
import { useSlashMenu } from "./slash/use-slash-menu";
import {
  Mention,
  useMentionMenu,
  type MentionItem,
  type MentionSources,
} from "./mentions";
import type {
  AIChatInputHandle,
  LocalCommandHistoryScope,
  LocalCommandSubmitHandler,
  ProseMirrorLikeNode,
} from "./types";

// 组件自带的样式:空编辑器那行占位文字由它的 `content: attr(data-placeholder)`
// 画出来 —— TipTap 的 Placeholder 只加类名和属性,不产出可见文字。放在这里而不是
// 让宿主 import,是因为组件离了它就是坏的,而漏 import 不会报错(agentre-server
// 此前就是这样:文案拼好了,一个字都看不到)。守卫在 ./placeholder-style.test.ts。
import "./chat-input.css";

// 同 useSlashMenu 里的常量:行内 `[]` 默认值每次 render 都是新身份,会把 slash
// 菜单的订阅 effect 变成「每次提交都重跑」。
const EMPTY_SLASH_COMMANDS: SlashCommand[] = [];

export type {
  AIChatInputDraft,
  AIChatInputHandle,
  LocalCommandHistoryScope,
  LocalCommandSubmitHandler,
} from "./types";

export interface AIChatInputProps {
  onSubmit: (content: string) => void;
  onEmptyChange?: (empty: boolean) => void;
  /** 编辑器内容以 ! 开头时进入命令模式,回调通知父组件切换 UI(横幅/按钮)。 */
  onCommandModeChange?: (active: boolean) => void;
  /** 命令模式下按 Enter/Run 时触发；返回实际执行作用域后才写入 Shell 历史。 */
  onCommandSubmit?: LocalCommandSubmitHandler;
  sendOnEnter?: boolean;
  userMessageHistory?: string[];
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  /** 编辑器创建时是否自动 focus。透传给 TipTap 的 `autofocus` 选项 ——
   *  让 TipTap 自己在 view 挂好之后再 focus，避开 mount 时 view 还没附加到 DOM
   *  的时序问题。新建会话场景下由 chat-panel 传 true。 */
  autoFocus?: boolean;
  /** 仅用于测试：暴露 TipTap editor 以便测试代码直接操作富文本。 */
  editorRef?: RefObject<Editor | null>;
  /** 当前会话 backend 类型 (claudecode/codex/builtin)。选中命令时透传给
   *  `SlashCommand.resolve`;空串或省略 → 不启用 slash menu。 */
  backendType?: string;
  /** 用户在 slash menu 选中一项时触发。literal_text 由 AIChatInput 内部直接把
   *  命令文本填回编辑器(不自动发送,等用户回车),所以父组件只需要处理 rpc 类。
   *  省略则 slash menu 不启用(等价于没 backend)。 */
  onSlashSelect?: (cmd: SlashCommand, exec: SlashExec) => void;
  /** 项目 / agent 提及数据源。提供且非空时启用 @ 菜单;省略则不启用。 */
  mentionSources?: MentionSources;
  /** 当前 backend 下**可用的完整命令清单**:宿主已把静态注册表与该 agent 的技能
   *  命令合并、并按 backend 过滤好(见 `slash/types.ts` 里为什么清单归宿主)。
   *  Codex 技能用 $,Claude Code 用 /;两种 trigger 由同一 popover 渲染。 */
  slashCommands?: SlashCommand[];
  /** 当前本地命令执行目标。设备与 cwd 共同隔离持久化 Shell 历史。 */
  localCommandHistoryScope?: LocalCommandHistoryScope;
  /** `!` 是否真能执行。省略 = 按 `onCommandSubmit` 接没接推断。
   *  显式传 false 是给这么一种宿主用的:它接 `onCommandSubmit` 只为兜住静默吞字
   *  (缺这个回调时本组件会把 `!foo` clearContent 掉、既不发也不说),自己并没有
   *  执行能力 —— agentre-server 的 wire 上就没有任何 PTY / 本地执行方法。
   *  只影响占位文案:提示里写着、按下去没反应,比不写更糟。 */
  localCommandsEnabled?: boolean;
}

const AIChatInputComponent = forwardRef<AIChatInputHandle, AIChatInputProps>(
  function AIChatInput(
    {
      onSubmit,
      onEmptyChange,
      onCommandModeChange,
      onCommandSubmit,
      sendOnEnter = true,
      userMessageHistory = [],
      placeholder,
      disabled,
      className,
      autoFocus = false,
      editorRef,
      backendType,
      onSlashSelect,
      mentionSources,
      slashCommands = EMPTY_SLASH_COMMANDS,
      localCommandHistoryScope,
      localCommandsEnabled,
    },
    ref,
  ) {
    const localCommandHistoryInstanceId = useId();
    const localCommandHistoryListboxId = `local-command-history-listbox-${localCommandHistoryInstanceId.replace(/:/g, "")}`;
    // 可选宿主能力:null 表示这个宿主没有本地命令历史(见 local-command-history/access.tsx)。
    const localCommandHistory = useOptionalLocalCommandHistoryAccess();
    const localCommandHistoryRef = useRef(localCommandHistory);
    const submitRef = useRef(onSubmit);
    const sendOnEnterRef = useRef(sendOnEnter);
    const onEmptyChangeRef = useRef(onEmptyChange);
    const historyRef = useRef(userMessageHistory);
    const historyIndexRef = useRef(-1);
    const applyingHistoryRef = useRef(false);
    const lastIsEmptyRef = useRef<boolean | null>(null);
    const triggerSubmitRef = useRef<() => void>(() => {});
    const slashKeyDownRef = useRef<(e: KeyboardEvent) => boolean>(() => false);
    const mentionKeyDownRef = useRef<(e: KeyboardEvent) => boolean>(
      () => false,
    );
    const commandHistoryKeyDownRef = useRef<(e: KeyboardEvent) => boolean>(
      () => false,
    );
    const slashSelectRef = useRef(onSlashSelect);
    const onCommandModeChangeRef = useRef(onCommandModeChange);
    const onCommandSubmitRef = useRef(onCommandSubmit);
    /** 命令模式去重 ref —— 避免每次 onUpdate 都触发回调 */
    const commandModeRef = useRef(false);
    useEffect(() => {
      slashSelectRef.current = onSlashSelect;
    }, [onSlashSelect]);

    useEffect(() => {
      localCommandHistoryRef.current = localCommandHistory;
    }, [localCommandHistory]);

    useEffect(() => {
      onCommandModeChangeRef.current = onCommandModeChange;
    }, [onCommandModeChange]);

    useEffect(() => {
      onCommandSubmitRef.current = onCommandSubmit;
    }, [onCommandSubmit]);

    useEffect(() => {
      submitRef.current = onSubmit;
    }, [onSubmit]);

    useEffect(() => {
      sendOnEnterRef.current = sendOnEnter;
    }, [sendOnEnter]);

    useEffect(() => {
      onEmptyChangeRef.current = onEmptyChange;
    }, [onEmptyChange]);

    useEffect(() => {
      historyRef.current = userMessageHistory;
      historyIndexRef.current = -1;
    }, [userMessageHistory]);

    // 当前 backend 下合法的命令名集合 —— 喂给 SlashHighlight extension 做高亮。
    // 用 ref 持有最新值,extension 通过闭包 getter 读取,避免 backendType 变化时
    // 重建 editor。变化后由下面的 useEffect 派发 setSlashHighlightRefresh 让
    // plugin 重算 decoration。
    const validNames = useMemo(() => {
      if (!backendType) return new Set<string>();
      return new Set(
        slashCommands
          .filter(
            (command) =>
              command.trigger === "/" &&
              /^[a-zA-Z][a-zA-Z0-9_-]*$/.test(command.name),
          )
          .map((command) => command.name),
      );
    }, [backendType, slashCommands]);
    const validNamesRef = useRef(validNames);

    // ── 占位文案 ────────────────────────────────────────────────────────────
    // 省略 `placeholder` 时由这里按**本次真正接上的能力**拼(见 placeholder.ts):
    // 四样触发器启用没有,AIChatInput 自己比谁都清楚,不该让每个宿主再按
    // backendType 猜一遍。显式传值仍然由调用方说了算。
    const slashEnabled = !!(backendType && onSlashSelect);
    const { t: uiT } = useUiTranslation();
    const resolvedPlaceholder = useMemo(
      () =>
        placeholder ??
        composerPlaceholder(
          composerCapabilities({
            mentionSources,
            slashEnabled,
            slashCommands,
            localCommandsEnabled: localCommandsEnabled ?? !!onCommandSubmit,
          }),
          uiT,
        ),
      [
        localCommandsEnabled,
        mentionSources,
        onCommandSubmit,
        placeholder,
        slashCommands,
        slashEnabled,
        uiT,
      ],
    );
    const placeholderRef = useRef(resolvedPlaceholder);

    const editor = useEditor({
      autofocus: autoFocus ? "end" : false,
      extensions: [
        Document,
        HardBreak,
        Paragraph,
        Text,
        // 撤销/重做历史:ProseMirror 接管 contentEditable 后浏览器原生 Cmd/Ctrl+Z
        // 会失效,必须由该扩展提供 history 栈 + Mod-z/Mod-y/Shift-Mod-z 快捷键。
        UndoRedo,
        // 函数形式:编辑器只在挂载时建一次,而占位文案会变(skill 目录是挂载后
        // 异步拉的、语言可切)。configure 里定死的话用户看到的永远是第一版。
        Placeholder.configure({ placeholder: () => placeholderRef.current }),
        SlashHighlight.configure({
          getValidNames: () => validNamesRef.current,
        }),
        Mention,
      ],
      editorProps: {
        clipboardTextParser: parsePlainTextClipboard,
        attributes: {
          class: cn(
            "ProseMirror min-h-10 max-h-[25vh] overflow-y-auto text-sm outline-none resize-none",
            className,
          ),
          role: "textbox",
          // 关掉浏览器的自动纠正 / 大写 / 拼写检查，避免和 IME 冲突。
          autocapitalize: "off",
          autocomplete: "off",
          autocorrect: "off",
          spellcheck: "false",
        },
        // 显式标注返回类型：处理器要读同一句里正在声明的 `editor`，不标注的话
        // TS 得先推出它的返回类型才能推 `editor`，两边互相等（TS7022/7023）。
        handleKeyDown: (view, event): boolean =>
          handleComposerKeyDown(
            {
              editor,
              commandModeRef,
              commandHistoryKeyDownRef,
              mentionKeyDownRef,
              slashKeyDownRef,
              sendOnEnterRef,
              historyRef,
              historyIndexRef,
              applyingHistoryRef,
              triggerSubmitRef,
            },
            view,
            event,
          ),
      },
      onUpdate: ({ editor: ed }) =>
        handleComposerUpdate(
          {
            applyingHistoryRef,
            historyIndexRef,
            lastIsEmptyRef,
            onEmptyChangeRef,
            commandModeRef,
            onCommandModeChangeRef,
          },
          ed,
        ),
      editable: !disabled,
    });

    useEffect(() => {
      if (editorRef) editorRef.current = editor ?? null;
      return () => {
        if (editorRef) editorRef.current = null;
      };
    }, [editor, editorRef]);

    useEffect(() => {
      triggerSubmitRef.current = () => {
        if (!editor || disabled || editor.view.composing || editor.isEmpty)
          return;
        const content = extractPlainText(
          editor.state.doc as unknown as ProseMirrorLikeNode,
        );
        if (!content.trim()) return;

        // 命令模式分流:content 以 ! 开头则调 onCommandSubmit,否则走普通 onSubmit。
        if (content.trimStart().startsWith("!")) {
          const command = content.trimStart().slice(1).trim();
          historyIndexRef.current = -1;
          if (command) {
            submitLocalCommand({
              command,
              history: localCommandHistoryRef.current,
              onCommandSubmit: onCommandSubmitRef.current,
            });
          }
          editor.commands.clearContent(true);
          editor.commands.focus();
          return;
        }

        historyIndexRef.current = -1;
        submitRef.current(content);
        editor.commands.clearContent(true);
        // 走 button 点击发送时，浏览器会把焦点抓到按钮上；clearContent 不会重新聚焦，
        // 这里显式 focus 回编辑器，保证用户能连续敲下一条消息而不用手再点一次输入框。
        // Enter 路径下原本就是聚焦态，再调用一次是无副作用的幂等操作。
        editor.commands.focus();
      };
    }, [disabled, editor]);

    useEffect(() => {
      editor?.setEditable(!disabled);
    }, [editor, disabled]);

    // 上面的 Placeholder 实时读 ref,但 decoration 只在 state 变化时重算 ——
    // 空编辑器根本不会有 doc 变化来触发它。补一次空事务把它推一下。
    useEffect(() => {
      placeholderRef.current = resolvedPlaceholder;
      if (editor) editor.view.dispatch(editor.state.tr);
    }, [editor, resolvedPlaceholder]);

    // backendType 变化时,新的 validNames 已经写进 ref,但 ProseMirror plugin 只在
    // doc 变化或显式 meta 时重算 decoration —— 这里主动触发一次让旧文本立刻按
    // 新规则重新染色(例:claudecode → builtin 后 /compact 应该退回默认色)。
    useEffect(() => {
      validNamesRef.current = validNames;
      editor?.commands.setSlashHighlightRefresh();
    }, [editor, validNames]);

    useImperativeHandle(
      ref,
      () => ({
        focus: () => editor?.commands.focus(),
        clear: () => {
          historyIndexRef.current = -1;
          editor?.commands.clearContent(true);
        },
        isEmpty: () => editor?.isEmpty ?? true,
        submit: () => triggerSubmitRef.current(),
        loadDraft: (draft) => {
          if (!editor) return;
          historyIndexRef.current = -1;
          applyInputHistoryMessage(editor, draft);
        },
        insertText: (text) => {
          // 与 slash literal_text 插入同手法(slashSelectHandler):
          // focus + insertContent,插在当前光标处,不自动发送。
          editor?.chain().focus().insertContent(text).run();
        },
      }),
      [editor],
    );

    // ── ! 本地命令历史菜单集成 ──────────────────────────────────────────────
    const commandHistoryMenu = useLocalCommandHistoryMenu({
      editor: editor ?? null,
      scope: localCommandHistoryScope,
    });
    useEffect(() => {
      commandHistoryKeyDownRef.current = commandHistoryMenu.onKeyDown;
    }, [commandHistoryMenu.onKeyDown]);

    useLocalCommandHistoryCombobox({
      editor,
      listboxId: localCommandHistoryListboxId,
      open: commandHistoryMenu.state.open,
      clearFocused: commandHistoryMenu.state.clearFocused,
      selectedIndex: commandHistoryMenu.state.selectedIndex,
    });

    // ── slash command menu 集成 ─────────────────────────────────────────────
    // 只在 backendType + onSlashSelect 同时具备时启用。useSlashMenu 监听 editor
    // selectionUpdate/update,实时检测触发位置;onKeyDown 同步给上面 handleKeyDown
    // 拦截 Up/Down/Enter/Tab/Esc。pick 统一鼠标 + 键盘选中入口。
    const slashSelectHandler = useCallback(
      (cmd: SlashCommand, exec: SlashExec) => {
        if (exec.kind === "literal_text") {
          // 只把命令文本填回输入框,不自动发送 —— 斜杠菜单是"智能提示",
          // 用户随时可以继续编辑(追加参数、删掉、改成普通消息)再决定是否回车发送。
          // 末尾补一个空格,既给参数留位置,也让 detectSlashTrigger 因 query 含空白而返回 null,
          // 避免插入完成后 popover 立刻基于新文本(如 /compact)重新弹出。
          if (editor) {
            editor.chain().focus().insertContent(`${exec.text} `).run();
          }
          return;
        }
        slashSelectRef.current?.(cmd, exec);
      },
      [editor],
    );
    const slashMenu = useSlashMenu({
      editor: slashEnabled ? (editor ?? null) : null,
      backendType: backendType ?? "",
      commands: slashCommands,
      onSelect: slashSelectHandler,
    });
    useEffect(() => {
      slashKeyDownRef.current = slashMenu.onKeyDown;
    }, [slashMenu.onKeyDown]);

    // ── @ mention menu 集成 ──────────────────────────────────────────────────
    // 只在 mentionSources 存在且非空时启用,行为与上面的 slash menu 对称。
    const mentionEnabled = !!(
      mentionSources &&
      mentionSources.agents.length + mentionSources.projects.length > 0
    );
    const emptySources = useMemo<MentionSources>(
      () => ({ agents: [], projects: [] }),
      [],
    );
    const mentionMenu = useMentionMenu({
      editor: mentionEnabled ? (editor ?? null) : null,
      sources: mentionSources ?? emptySources,
      // 插入由 hook 内部完成(insert mention node);父组件无需处理。
      onPick: (_item: MentionItem) => {},
    });
    useEffect(() => {
      mentionKeyDownRef.current = mentionMenu.onKeyDown;
    }, [mentionMenu.onKeyDown]);

    return (
      <>
        <EditorContent editor={editor} />
        <ComposerPopovers
          editorElement={editor?.view.dom ?? null}
          commandHistoryEnabled={!!localCommandHistory}
          commandHistoryListboxId={localCommandHistoryListboxId}
          commandHistoryMenu={commandHistoryMenu}
          slashEnabled={slashEnabled}
          slashMenu={slashMenu}
          mentionEnabled={mentionEnabled}
          mentionMenu={mentionMenu}
        />
      </>
    );
  },
);

export const AIChatInput = memo(
  AIChatInputComponent,
) as typeof AIChatInputComponent;
