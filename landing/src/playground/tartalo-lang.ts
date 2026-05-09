import {
  HighlightStyle,
  LanguageSupport,
  StreamLanguage,
  syntaxHighlighting,
} from "@codemirror/language";
import { Tag, tags as t } from "@lezer/highlight";

const KEYWORDS = new Set([
  "let",
  "const",
  "func",
  "return",
  "if",
  "else",
  "for",
  "while",
  "in",
  "true",
  "false",
  "null",
  "type",
  "import",
  "from",
  "export",
  "match",
  "break",
  "continue",
  "test",
  "eval",
  "defer",
  "as",
]);

const PRIMITIVE_TYPES = new Set([
  "string",
  "number",
  "float",
  "bool",
  "void",
  "Process",
  "Response",
  "Pair",
]);

const BUILTINS = new Set([
  "echo",
  "eprint",
  "str",
  "num",
  "len",
  "env",
  "exit",
  "upper",
  "lower",
  "trim",
  "replace",
  "contains",
  "startsWith",
  "endsWith",
  "slice",
  "split",
  "join",
  "readFile",
  "writeFile",
  "appendFile",
  "removeFile",
  "mkdir",
  "listDir",
  "exists",
  "isFile",
  "isDir",
  "readStdin",
  "pathJoin",
  "basename",
  "dirname",
  "extname",
  "exec",
  "execTimeout",
  "fetch",
  "fetchTimeout",
  "fetchHeaders",
  "postJson",
  "postForm",
  "request",
  "header",
  "urlEncode",
  "regexMatch",
  "regexFind",
  "regexFindAll",
  "regexReplace",
  "map",
  "filter",
  "reduce",
  "args",
  "now",
  "sleep",
  "formatTime",
  "jsonGet",
  "jsonHas",
  "jsonArray",
  "jsonEscape",
  "abs",
  "min",
  "max",
  "floor",
  "ceil",
  "round",
  "pow",
  "sqrt",
  "keys",
  "values",
]);

interface State {
  inString: boolean;
  inCmd: boolean;
  interpDepth: number;
}

const builtinTag = Tag.define();
const interpTag = Tag.define();
const commandTag = Tag.define();

const tartaloMode = StreamLanguage.define<State>({
  name: "tartalo",

  startState: () => ({ inString: false, inCmd: false, interpDepth: 0 }),

  copyState: (s) => ({ ...s }),

  token(stream, state) {
    // Inside backtick command literal — consume until the closing backtick.
    if (state.inCmd) {
      while (!stream.eol()) {
        const ch = stream.next();
        if (ch === "`") {
          state.inCmd = false;
          return "command";
        }
        if (ch === "\\" && !stream.eol()) {
          stream.next();
        }
      }
      return "command";
    }

    // Inside double-quoted string — handle ${...} interpolation handoff.
    if (state.inString) {
      if (stream.match("${", true)) {
        state.interpDepth = 1;
        return "interp";
      }
      while (!stream.eol()) {
        if (stream.peek() === "$" && stream.string[stream.pos + 1] === "{") {
          return "string";
        }
        const ch = stream.next();
        if (ch === "\\" && !stream.eol()) {
          stream.next();
          continue;
        }
        if (ch === '"') {
          state.inString = false;
          return "string";
        }
      }
      return "string";
    }

    if (stream.eatSpace()) return null;

    // Inside ${ ... } — track nested braces and switch back when balanced.
    if (state.interpDepth > 0) {
      if (stream.eat("{")) {
        state.interpDepth++;
        return "punctuation";
      }
      if (stream.eat("}")) {
        state.interpDepth--;
        if (state.interpDepth === 0) return "interp";
        return "punctuation";
      }
    }

    // line comment
    if (stream.match("//")) {
      stream.skipToEnd();
      return "comment";
    }

    // double-quoted string
    if (stream.eat('"')) {
      state.inString = true;
      return "string";
    }

    // backtick command
    if (stream.eat("`")) {
      state.inCmd = true;
      return "command";
    }

    // number
    if (stream.match(/^-?\d+(\.\d+)?/)) return "number";

    // identifier / keyword / type / builtin
    if (stream.match(/^[A-Za-z_][A-Za-z0-9_]*/)) {
      const word = stream.current();
      if (KEYWORDS.has(word)) return "keyword";
      if (PRIMITIVE_TYPES.has(word)) return "typeName";
      if (BUILTINS.has(word)) return "builtin";
      if (/^[A-Z]/.test(word)) return "typeName";
      return "variableName";
    }

    // multi-char operators first
    if (stream.match(/^(==|!=|<=|>=|&&|\|\||\?\?|=>|->|\.\.\.)/)) {
      return "operator";
    }
    if (stream.eat(/[+\-*/<>=!&|^%~]/)) return "operator";
    if (stream.eat(/[{}()\[\];,.:?@]/)) return "punctuation";

    stream.next();
    return null;
  },

  tokenTable: {
    builtin: builtinTag,
    interp: interpTag,
    command: commandTag,
  },

  languageData: {
    commentTokens: { line: "//" },
    closeBrackets: { brackets: ["(", "[", "{", '"', "`"] },
  },
});

const highlight = HighlightStyle.define([
  { tag: t.keyword, color: "#ff7a3d", fontWeight: "500" },
  { tag: t.typeName, color: "#ffb547" },
  { tag: builtinTag, color: "#6cc5ff" },
  { tag: t.string, color: "#b6e08a" },
  { tag: commandTag, color: "#b6e08a", fontStyle: "italic" },
  { tag: interpTag, color: "#ffb547", fontWeight: "500" },
  { tag: t.number, color: "#c896ff" },
  { tag: t.comment, color: "#6a6a65", fontStyle: "italic" },
  { tag: t.operator, color: "#c0c0bc" },
  { tag: t.punctuation, color: "#7a7a75" },
  { tag: t.variableName, color: "#f1f1ef" },
]);

export function tartalo(): LanguageSupport {
  return new LanguageSupport(tartaloMode);
}

export const tartaloHighlight = syntaxHighlighting(highlight);
