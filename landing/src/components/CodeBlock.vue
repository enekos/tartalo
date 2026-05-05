<template>
  <div class="code-block" :class="{ 'with-title': title }">
    <div v-if="title || filename" class="code-header">
      <span v-if="filename" class="filename mono">{{ filename }}</span>
      <span v-if="title" class="title">{{ title }}</span>
      <button v-if="copyable" class="copy-btn mono" @click="copy">
        {{ copied ? "copied" : "copy" }}
      </button>
    </div>
    <pre class="mono"><code v-html="highlighted"></code></pre>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";

const props = withDefaults(
  defineProps<{
    code: string;
    lang?: "tartalo" | "sh" | "text";
    title?: string;
    filename?: string;
    copyable?: boolean;
  }>(),
  { lang: "tartalo", copyable: true }
);

const copied = ref(false);
const copy = async () => {
  try {
    await navigator.clipboard.writeText(props.code);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1400);
  } catch {
    /* noop */
  }
};

const escapeHtml = (s: string) =>
  s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");

const TARTALO_KEYWORDS = new Set([
  "let",
  "const",
  "func",
  "return",
  "if",
  "else",
  "for",
  "in",
  "true",
  "false",
  "null",
  "type",
  "import",
  "from",
  "export",
  "match",
]);

const TARTALO_TYPES = new Set([
  "string",
  "number",
  "float",
  "bool",
  "void",
  "Process",
  "Response",
  "Pair",
  "Person",
  "User",
]);

const TARTALO_BUILTINS = new Set([
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
]);

interface Token {
  type: string;
  value: string;
}

const tokenizeTartalo = (src: string): Token[] => {
  const tokens: Token[] = [];
  let i = 0;
  while (i < src.length) {
    const c = src[i]!;

    // line comment
    if (c === "/" && src[i + 1] === "/") {
      let end = src.indexOf("\n", i);
      if (end === -1) end = src.length;
      tokens.push({ type: "comment", value: src.slice(i, end) });
      i = end;
      continue;
    }

    // double-quoted string with ${...} interpolation
    if (c === '"') {
      let j = i + 1;
      let buf = '"';
      while (j < src.length) {
        const ch = src[j]!;
        if (ch === "\\" && j + 1 < src.length) {
          buf += src[j] + src[j + 1];
          j += 2;
          continue;
        }
        if (ch === "$" && src[j + 1] === "{") {
          tokens.push({ type: "string", value: buf });
          let depth = 1;
          let k = j + 2;
          let interp = "${";
          while (k < src.length && depth > 0) {
            const ck = src[k]!;
            if (ck === "{") depth++;
            else if (ck === "}") depth--;
            interp += ck;
            k++;
            if (depth === 0) break;
          }
          tokens.push({ type: "interp", value: interp });
          buf = "";
          j = k;
          continue;
        }
        buf += ch;
        if (ch === '"') {
          j++;
          break;
        }
        j++;
      }
      if (buf) tokens.push({ type: "string", value: buf });
      i = j;
      continue;
    }

    // backtick command literal
    if (c === "`") {
      let j = i + 1;
      while (j < src.length && src[j] !== "`") j++;
      tokens.push({ type: "command", value: src.slice(i, j + 1) });
      i = j + 1;
      continue;
    }

    // number
    if (/[0-9]/.test(c)) {
      let j = i;
      while (j < src.length && /[0-9.]/.test(src[j]!)) j++;
      tokens.push({ type: "number", value: src.slice(i, j) });
      i = j;
      continue;
    }

    // identifier / keyword
    if (/[A-Za-z_]/.test(c)) {
      let j = i;
      while (j < src.length && /[A-Za-z0-9_]/.test(src[j]!)) j++;
      const word = src.slice(i, j);
      let type = "ident";
      if (TARTALO_KEYWORDS.has(word)) type = "keyword";
      else if (TARTALO_TYPES.has(word)) type = "type";
      else if (TARTALO_BUILTINS.has(word)) type = "builtin";
      else if (/^[A-Z]/.test(word)) type = "type";
      tokens.push({ type, value: word });
      i = j;
      continue;
    }

    // punctuation / operators
    if (/[{}()[\];,.:?!|&+\-*/<>=]/.test(c)) {
      tokens.push({ type: "punc", value: c });
      i++;
      continue;
    }

    tokens.push({ type: "text", value: c });
    i++;
  }
  return tokens;
};

const tokenizeSh = (src: string): Token[] => {
  const tokens: Token[] = [];
  const SH_KEYWORDS = new Set([
    "if",
    "then",
    "else",
    "elif",
    "fi",
    "for",
    "do",
    "done",
    "while",
    "case",
    "esac",
    "in",
    "function",
    "return",
    "exit",
    "set",
    "export",
    "readonly",
    "local",
  ]);
  const lines = src.split("\n");
  for (let li = 0; li < lines.length; li++) {
    const line = lines[li]!;
    let i = 0;
    while (i < line.length) {
      const c = line[i]!;
      if (c === "#") {
        tokens.push({ type: "comment", value: line.slice(i) });
        i = line.length;
        continue;
      }
      if (c === '"' || c === "'") {
        const quote = c;
        let j = i + 1;
        while (j < line.length && line[j] !== quote) {
          if (line[j] === "\\") j++;
          j++;
        }
        tokens.push({ type: "string", value: line.slice(i, j + 1) });
        i = j + 1;
        continue;
      }
      if (c === "$") {
        let j = i + 1;
        if (line[j] === "{") {
          while (j < line.length && line[j] !== "}") j++;
          j++;
        } else {
          while (j < line.length && /[A-Za-z0-9_]/.test(line[j]!)) j++;
        }
        tokens.push({ type: "interp", value: line.slice(i, j) });
        i = j;
        continue;
      }
      if (/[A-Za-z_]/.test(c)) {
        let j = i;
        while (j < line.length && /[A-Za-z0-9_]/.test(line[j]!)) j++;
        const word = line.slice(i, j);
        tokens.push({ type: SH_KEYWORDS.has(word) ? "keyword" : "ident", value: word });
        i = j;
        continue;
      }
      tokens.push({ type: "text", value: c });
      i++;
    }
    if (li < lines.length - 1) tokens.push({ type: "text", value: "\n" });
  }
  return tokens;
};

const highlighted = computed(() => {
  const src = props.code.replace(/\t/g, "  ");
  if (props.lang === "text") return escapeHtml(src);
  const tokens =
    props.lang === "sh" ? tokenizeSh(src) : tokenizeTartalo(src);
  return tokens
    .map((t) => {
      const v = escapeHtml(t.value);
      switch (t.type) {
        case "comment":
          return `<span class="tk-comment">${v}</span>`;
        case "keyword":
          return `<span class="tk-keyword">${v}</span>`;
        case "type":
          return `<span class="tk-type">${v}</span>`;
        case "builtin":
          return `<span class="tk-builtin">${v}</span>`;
        case "string":
          return `<span class="tk-string">${v}</span>`;
        case "interp":
          return `<span class="tk-interp">${v}</span>`;
        case "command":
          return `<span class="tk-command">${v}</span>`;
        case "number":
          return `<span class="tk-number">${v}</span>`;
        case "punc":
          return `<span class="tk-punc">${v}</span>`;
        default:
          return v;
      }
    })
    .join("");
});
</script>

<style scoped>
.code-block {
  position: relative;
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  margin: 1.25rem 0;
  font-size: 0.9rem;
}

.code-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.55rem 0.9rem;
  background: rgba(255, 255, 255, 0.025);
  border-bottom: 1px solid var(--border);
  font-size: 0.75rem;
}

.filename {
  color: var(--text-muted);
  font-size: 0.78rem;
}

.title {
  color: var(--text-subtle);
  font-size: 0.78rem;
}

.copy-btn {
  margin-left: auto;
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-muted);
  font-size: 0.72rem;
  padding: 0.2rem 0.55rem;
  border-radius: 4px;
  transition: all 0.15s ease;
}

.copy-btn:hover {
  color: var(--accent);
  border-color: var(--accent);
}

pre {
  margin: 0;
  padding: 1.1rem 1.2rem;
  overflow-x: auto;
  line-height: 1.6;
}

pre code {
  white-space: pre;
  display: block;
  color: var(--code-punc);
  font-family: "JetBrains Mono", ui-monospace, monospace;
}

:deep(.tk-comment) {
  color: var(--code-comment);
  font-style: italic;
}
:deep(.tk-keyword) {
  color: var(--code-keyword);
  font-weight: 500;
}
:deep(.tk-type) {
  color: var(--code-type);
}
:deep(.tk-builtin) {
  color: var(--code-builtin);
}
:deep(.tk-string) {
  color: var(--code-string);
}
:deep(.tk-interp) {
  color: var(--accent-secondary);
}
:deep(.tk-command) {
  color: var(--code-string);
  background: rgba(182, 224, 138, 0.06);
  border-radius: 3px;
  padding: 0 2px;
}
:deep(.tk-number) {
  color: var(--code-number);
}
:deep(.tk-punc) {
  color: var(--code-punc);
}
</style>
