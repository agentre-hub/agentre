// shell-command.ts —— 按 shell 的引号与转义读一条命令：剥掉 codex 等后端套的 `zsh -lc '…'`
// 外壳，把命令切成一条条简单命令的词。
//
// 摘要行（raw/summary.ts）与记忆写入判据（memory-write/parse.ts）都要看「里面那条命令」，
// 外壳与引号的读法只留这一份，免得两处各抄一份正则后漂移。只读 POSIX shell 的引号、转义、
// 分隔符与注释：变量展开、$(…) 的内容、heredoc 这类要执行才知道的写法不展开。一遍扫描，
// 转录里每条命令都会过这里，长命令不能退化。

const SHELLS = new Set(["sh", "bash", "zsh"]);
const SCRIPT_FLAGS = new Set(["-c", "-lc"]);
const COMMAND_SEPARATORS = new Set([";", "&", "|", "(", ")", "`", "\n"]);
const WORD_BREAKS = new Set([" ", "\t", "\r"]);
// 双引号里反斜杠只转义这几个字符（另有反斜杠换行是续行），其余反斜杠原样保留。
const DOUBLE_QUOTE_ESCAPES = new Set(["$", "`", '"', "\\"]);
const ALNUM = /[A-Za-z0-9]/;

/** 路径词的最后一段（`/` 与 Windows 的 `\` 都算分隔符）。 */
export function programName(word: string): string {
  return word.slice(
    Math.max(word.lastIndexOf("/"), word.lastIndexOf("\\")) + 1,
  );
}

/**
 * 把命令切成简单命令（被 ; & | ( ) ` 与换行隔开），每条是解完引号与转义的词。引号没闭合时
 * 返回 null（这样的命令 shell 也跑不起来）。不带引号的反斜杠后面跟字母或数字时原样保留：
 * POSIX 里那只是多余的转义，Windows 上那是路径分隔符（`C:\Users\…\agrctl.exe`）。
 */
export function splitShellCommands(script: string): string[][] | null {
  const commands: string[][] = [];
  let words: string[] = [];
  let word: string | null = null;
  const endWord = () => {
    if (word !== null) words.push(word);
    word = null;
  };
  const endCommand = () => {
    endWord();
    if (words.length > 0) commands.push(words);
    words = [];
  };
  for (let i = 0; i < script.length; i++) {
    const c = script[i];
    if (c === "'") {
      const close = script.indexOf("'", i + 1);
      if (close < 0) return null;
      word = (word ?? "") + script.slice(i + 1, close);
      i = close;
    } else if (c === '"') {
      let text = "";
      for (i++; i < script.length && script[i] !== '"'; i++) {
        const next = script[i + 1];
        if (script[i] === "\\" && next === "\n") i++;
        else if (script[i] === "\\" && DOUBLE_QUOTE_ESCAPES.has(next))
          text += script[++i];
        else text += script[i];
      }
      if (i >= script.length) return null;
      word = (word ?? "") + text;
    } else if (c === "\\") {
      const next = script[i + 1];
      if (next === undefined || ALNUM.test(next)) word = (word ?? "") + c;
      else if (next === "\n") i++;
      else word = (word ?? "") + script[++i];
    } else if (c === "#" && word === null) {
      const newline = script.indexOf("\n", i);
      i = newline < 0 ? script.length : newline - 1;
    } else if (WORD_BREAKS.has(c)) {
      endWord();
    } else if (COMMAND_SEPARATORS.has(c)) {
      endCommand();
    } else {
      word = (word ?? "") + c;
    }
  }
  endCommand();
  return commands;
}

/**
 * 去掉 `(zsh|bash|sh) -lc|-c <脚本>` 外壳（shell 可带路径），返回解完外壳引号的脚本；不是
 * 恰好这一形状（外壳后还有别的词、别的命令，或引号没闭合）时原样返回，两种情况都 trim。
 */
export function unwrapShellCommand(command: string): string {
  const trimmed = command.trim();
  const commands = splitShellCommands(trimmed);
  if (commands?.length !== 1) return trimmed;
  const [shell, flag, script, ...rest] = commands[0];
  if (
    rest.length > 0 ||
    script === undefined ||
    !SHELLS.has(programName(shell)) ||
    !SCRIPT_FLAGS.has(flag)
  ) {
    return trimmed;
  }
  return script.trim();
}
