<template>
  <div class="playground">
    <section class="pg-hero">
      <div class="container">
        <span class="eyebrow mono">// playground</span>
        <h1>Compile <code>.tt</code> in your browser.</h1>
        <p class="lead">
          The Tartalo compiler runs as WebAssembly directly on this page —
          no server round-trips, nothing leaves your laptop. Edit on the
          left, the emitted POSIX <code>sh</code> appears on the right.
        </p>
      </div>
    </section>

    <section class="pg-main">
      <div class="container">
        <div class="pg-toolbar mono">
          <label class="pg-select">
            <span>sample</span>
            <select v-model="selectedSample" @change="loadSample">
              <option v-for="s in samples" :key="s.name" :value="s.name">
                {{ s.name }}
              </option>
            </select>
          </label>

          <span class="pg-status" :class="statusClass">
            <span class="pg-status-dot"></span>
            {{ status }}
          </span>

          <span v-if="compileMs !== null" class="pg-stat">
            compiled in <strong>{{ compileMs }}ms</strong>
          </span>

          <span v-if="outputBytes !== null" class="pg-stat">
            <strong>{{ outputBytes }}</strong> B sh
          </span>

          <div class="pg-spacer"></div>

          <button class="pg-btn" :disabled="!canCopy" @click="copySh">
            {{ copied ? "copied!" : "copy .sh" }}
          </button>
          <button class="pg-btn" :disabled="!canCopy" @click="downloadSh">
            download
          </button>
          <button class="pg-btn" @click="shareLink">
            {{ shared ? "link copied!" : "share" }}
          </button>
          <button class="pg-btn pg-btn-primary" @click="forceCompile">
            run <span class="pg-kbd">⌘↵</span>
          </button>
        </div>

        <div class="pg-grid" :style="{ gridTemplateColumns: `${leftPct}% 6px ${100 - leftPct}%` }">
          <div class="pg-pane">
            <div class="pg-pane-head mono">
              <span class="pg-filename">playground.tt</span>
              <span class="pg-hint">tartalo source</span>
            </div>
            <div ref="editorMount" class="pg-cm-host"></div>
          </div>

          <div
            class="pg-resizer"
            :class="{ active: dragging }"
            @mousedown="startDrag"
          ></div>

          <div class="pg-pane">
            <div class="pg-pane-head mono">
              <span class="pg-filename">{{ outputFilename }}</span>
              <span v-if="errors.length" class="pg-error-count">
                {{ errors.length }} diagnostic{{ errors.length === 1 ? "" : "s" }}
              </span>
              <span v-else-if="outputSh" class="pg-ok-badge">POSIX sh · set -eu</span>
            </div>
            <div ref="outputMount" class="pg-cm-host"></div>
          </div>
        </div>

        <div v-if="errors.length" class="pg-errors-panel mono">
          <div
            v-for="(d, i) in parsedDiagnostics"
            :key="i"
            class="pg-error-row"
            @click="jumpTo(d)"
          >
            <span class="pg-error-pos">{{ d.line }}:{{ d.col }}</span>
            <span class="pg-error-msg">{{ d.msg }}</span>
          </div>
        </div>

        <p class="pg-fineprint mono">
          <span>compiler: <strong>{{ wasmSizeKB }} KB</strong> wasm</span>
          <span class="dot-sep">·</span>
          <span>runs locally — nothing leaves the page</span>
          <span class="dot-sep">·</span>
          <span>imports disabled in playground</span>
          <span class="dot-sep">·</span>
          <a href="https://github.com/enekos/tartalo" target="_blank" rel="noopener">
            source on github
          </a>
        </p>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { EditorState, Compartment } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { basicSetup } from "codemirror";
import { indentWithTab, defaultKeymap } from "@codemirror/commands";
import { setDiagnostics } from "@codemirror/lint";
import type { Diagnostic } from "@codemirror/lint";
import { StreamLanguage } from "@codemirror/language";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { tartalo, tartaloHighlight } from "../playground/tartalo-lang";
import { tartaloTheme, shHighlighting } from "../playground/theme";

interface CompileResult {
  sh: string;
  errors: string[];
  rendered: string;
}

declare global {
  interface Window {
    Go: new () => {
      run: (instance: WebAssembly.Instance) => Promise<void>;
      importObject: WebAssembly.Imports;
    };
    tartaloCompile?: (src: string) => CompileResult;
  }
}

interface ParsedDiag {
  line: number;
  col: number;
  msg: string;
}

const samples = [
  {
    name: "hello",
    code: `// classic
func greet(name: string): string {
  return "Hello, \${name}!"
}

func main(): void {
  let who: string = env("USER") ?? "world"
  echo(greet(who))
}
`,
  },
  {
    name: "records",
    code: `// nominal record types; literals must name the type
type Person = {
  name: string,
  age: number,
}

func birthday(p: Person): Person {
  return Person{ name: p.name, age: p.age + 1 }
}

func main(): void {
  let people: Person[] = [
    Person{ name: "Alice", age: 30 },
    Person{ name: "Bob",   age: 41 },
  ]
  for p in people {
    let older: Person = birthday(p)
    echo("\${older.name} is now \${older.age}")
  }
}
`,
  },
  {
    name: "commands",
    code: `// backticks shell out and capture stdout
func main(): void {
  let files: string = \`ls -1\`
  for line in split(files, "\\n") {
    if endsWith(line, ".tt") {
      echo("source: \${line}")
    }
  }
}
`,
  },
  {
    name: "match",
    code: `func main(): void {
  let action: string = env("ACTION") ?? ""
  match action {
    "build" | "compile" => echo("compiling")
    "run"               => echo("running")
    ""                  => echo("usage: ACTION=...")
    _                   => echo("unknown: \${action}")
  }
}
`,
  },
  {
    name: "optionals",
    code: `// env() returns string?; ?? unwraps with a default
func main(): void {
  let token: string? = env("API_TOKEN")
  let key: string = token ?? "anon"
  if token == null {
    eprint("warning: no API_TOKEN set")
  }
  echo("key=\${key}")
}
`,
  },
  {
    name: "boundaries",
    code: `// asInt/asFloat/asBool convert untyped strings into typed values
// at the boundary. On a mismatch the script aborts with a runtime
// type error citing the call site — no silent NaN, no zero-default.
func main(): void {
  let raw: string = env("PORT") ?? "8080"
  let port: number = asInt(raw)
  if port < 1024 {
    eprint("warning: privileged port \${port}")
  }
  echo("listening on :\${port}")
}
`,
  },
  {
    name: "type-error",
    code: `// the checker catches this at compile time
func main(): void {
  let x: number = "this is not a number"
  echo(str(x))
}
`,
  },
];

const selectedSample = ref(samples[0]!.name);
const source = ref("");
const outputSh = ref("");
const errors = ref<string[]>([]);
const status = ref("loading compiler…");
const copied = ref(false);
const shared = ref(false);
const wasmSizeKB = ref<number | "?">("?");
const compileMs = ref<number | null>(null);
const outputBytes = ref<number | null>(null);

const editorMount = ref<HTMLDivElement | null>(null);
const outputMount = ref<HTMLDivElement | null>(null);

let editorView: EditorView | null = null;
let outputView: EditorView | null = null;
let debounceTimer: number | undefined;

const leftPct = ref(50);
const dragging = ref(false);

const placeholder = "// emitted shell script appears here\n";

const outputFilename = computed(() =>
  errors.value.length ? "diagnostics" : "playground.sh"
);

const canCopy = computed(
  () => outputSh.value !== "" && errors.value.length === 0
);

const statusClass = computed(() => {
  if (status.value.startsWith("error") || status.value.startsWith("compile error")) return "pg-status-err";
  if (status.value === "ready" || status.value === "ok") return "pg-status-ok";
  return "pg-status-info";
});

const parsedDiagnostics = computed<ParsedDiag[]>(() => {
  return errors.value.map(parseDiag).filter((d): d is ParsedDiag => d !== null);
});

function parseDiag(s: string): ParsedDiag | null {
  // expected: "playground.tt:LINE:COL: message"
  const m = /^[^:]+:(\d+):(\d+):\s*(.*)$/.exec(s);
  if (!m) return { line: 1, col: 1, msg: s };
  return { line: parseInt(m[1]!, 10), col: parseInt(m[2]!, 10), msg: m[3]! };
}

function loadSample() {
  const s = samples.find((x) => x.name === selectedSample.value);
  if (s && editorView) {
    setEditorContent(s.code);
  }
}

function setEditorContent(text: string) {
  if (!editorView) return;
  editorView.dispatch({
    changes: {
      from: 0,
      to: editorView.state.doc.length,
      insert: text,
    },
  });
  source.value = text;
}

function setOutputContent(text: string, isError: boolean) {
  if (!outputView) return;
  // Swap language: shell highlight for sh, plain for error text.
  const langExt = isError
    ? []
    : [StreamLanguage.define(shell)];
  outputView.dispatch({
    changes: {
      from: 0,
      to: outputView.state.doc.length,
      insert: text,
    },
    effects: outputLangCompartment.reconfigure(langExt),
  });
}

function compile() {
  if (!window.tartaloCompile || !editorView) return;
  const src = editorView.state.doc.toString();
  source.value = src;
  const t0 = performance.now();
  const res = window.tartaloCompile(src);
  const elapsed = performance.now() - t0;
  compileMs.value = Math.max(1, Math.round(elapsed));

  errors.value = res.errors ?? [];
  outputSh.value = res.sh ?? "";

  if (errors.value.length === 0) {
    status.value = "ok";
    outputBytes.value = new TextEncoder().encode(outputSh.value).length;
    setOutputContent(outputSh.value || placeholder, false);
    setDiags([]);
  } else {
    status.value = `compile error · ${errors.value.length}`;
    outputBytes.value = null;
    setOutputContent(res.rendered || errors.value.join("\n"), true);
    setDiags(parsedDiagnostics.value);
  }
}

function setDiags(diags: ParsedDiag[]) {
  if (!editorView) return;
  const cmDiags: Diagnostic[] = [];
  const doc = editorView.state.doc;
  for (const d of diags) {
    const lineNum = Math.max(1, Math.min(doc.lines, d.line));
    const line = doc.line(lineNum);
    const col = Math.max(0, Math.min(line.length, d.col - 1));
    const from = line.from + col;
    const to = Math.min(line.to, from + 1);
    cmDiags.push({
      from,
      to,
      severity: "error",
      message: d.msg,
    });
  }
  editorView.dispatch(setDiagnostics(editorView.state, cmDiags));
}

function jumpTo(d: ParsedDiag) {
  if (!editorView) return;
  const doc = editorView.state.doc;
  const lineNum = Math.max(1, Math.min(doc.lines, d.line));
  const line = doc.line(lineNum);
  const col = Math.max(0, Math.min(line.length, d.col - 1));
  const pos = line.from + col;
  editorView.focus();
  editorView.dispatch({
    selection: { anchor: pos, head: pos },
    scrollIntoView: true,
  });
}

function forceCompile() {
  window.clearTimeout(debounceTimer);
  compile();
}

async function copySh() {
  if (!canCopy.value) return;
  try {
    await navigator.clipboard.writeText(outputSh.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1400);
  } catch {
    /* noop */
  }
}

function downloadSh() {
  if (!canCopy.value) return;
  const blob = new Blob([outputSh.value], { type: "application/x-sh" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "playground.sh";
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

async function shareLink() {
  if (!editorView) return;
  const src = editorView.state.doc.toString();
  const enc = encodeURIComponent(btoa(unescape(encodeURIComponent(src))));
  const url = `${window.location.origin}${window.location.pathname}#code=${enc}`;
  try {
    await navigator.clipboard.writeText(url);
    shared.value = true;
    setTimeout(() => (shared.value = false), 1400);
  } catch {
    window.location.hash = `code=${enc}`;
  }
  window.history.replaceState(null, "", `#code=${enc}`);
}

function tryLoadFromHash(): string | null {
  const h = window.location.hash;
  const m = /^#code=(.+)$/.exec(h);
  if (!m) return null;
  try {
    return decodeURIComponent(escape(atob(decodeURIComponent(m[1]!))));
  } catch {
    return null;
  }
}

// --- resizable split ---

function startDrag(e: MouseEvent) {
  dragging.value = true;
  e.preventDefault();
  const startX = e.clientX;
  const startPct = leftPct.value;
  const grid = (e.currentTarget as HTMLElement).parentElement;
  if (!grid) return;
  const w = grid.getBoundingClientRect().width;

  const onMove = (ev: MouseEvent) => {
    const dx = ev.clientX - startX;
    const dPct = (dx / w) * 100;
    leftPct.value = Math.max(20, Math.min(80, startPct + dPct));
  };
  const onUp = () => {
    dragging.value = false;
    window.removeEventListener("mousemove", onMove);
    window.removeEventListener("mouseup", onUp);
  };
  window.addEventListener("mousemove", onMove);
  window.addEventListener("mouseup", onUp);
}

// --- editor lifecycle ---

const outputLangCompartment = new Compartment();

function makeEditor(host: HTMLDivElement, initial: string): EditorView {
  const customKeymap = keymap.of([
    {
      key: "Mod-Enter",
      run: () => {
        forceCompile();
        return true;
      },
    },
    indentWithTab,
    ...defaultKeymap,
  ]);

  const state = EditorState.create({
    doc: initial,
    extensions: [
      basicSetup,
      tartalo(),
      tartaloHighlight,
      tartaloTheme,
      customKeymap,
      EditorView.updateListener.of((u) => {
        if (u.docChanged) {
          source.value = u.state.doc.toString();
          window.clearTimeout(debounceTimer);
          if (window.tartaloCompile) {
            debounceTimer = window.setTimeout(compile, 220);
          }
        }
      }),
    ],
  });
  return new EditorView({ state, parent: host });
}

function makeOutput(host: HTMLDivElement, initial: string): EditorView {
  const state = EditorState.create({
    doc: initial,
    extensions: [
      basicSetup,
      outputLangCompartment.of([StreamLanguage.define(shell)]),
      shHighlighting,
      tartaloTheme,
      EditorState.readOnly.of(true),
      EditorView.editable.of(false),
    ],
  });
  return new EditorView({ state, parent: host });
}

async function loadWasm() {
  const base = (import.meta.env.BASE_URL ?? "/").replace(/\/$/, "") + "/";
  const execScript = base + "wasm_exec.js";
  const wasmUrl = base + "tartalo.wasm";

  if (!window.Go) {
    await new Promise<void>((resolve, reject) => {
      const s = document.createElement("script");
      s.src = execScript;
      s.onload = () => resolve();
      s.onerror = () => reject(new Error("failed to load wasm_exec.js"));
      document.head.appendChild(s);
    });
  }

  const go = new window.Go();
  const resp = await fetch(wasmUrl);
  if (!resp.ok) throw new Error(`fetch ${wasmUrl}: ${resp.status}`);
  const buf = await resp.arrayBuffer();
  wasmSizeKB.value = Math.round(buf.byteLength / 1024);
  const result = await WebAssembly.instantiate(buf, go.importObject);
  void go.run(result.instance);

  for (let i = 0; i < 100 && !window.tartaloCompile; i++) {
    await new Promise((r) => setTimeout(r, 20));
  }
  if (!window.tartaloCompile) {
    throw new Error("tartaloCompile was not registered by the wasm runtime");
  }
}

onMounted(async () => {
  if (!editorMount.value || !outputMount.value) return;

  const fromHash = tryLoadFromHash();
  const initial = fromHash ?? samples[0]!.code;

  editorView = makeEditor(editorMount.value, initial);
  outputView = makeOutput(outputMount.value, placeholder);
  source.value = initial;

  try {
    await loadWasm();
    status.value = "ready";
    compile();
  } catch (e) {
    status.value = "error · failed to load compiler";
    errors.value = [String(e)];
    setOutputContent(String(e), true);
  }
});

onBeforeUnmount(() => {
  editorView?.destroy();
  outputView?.destroy();
});

// keep <select> in sync if the user pastes a hash-loaded snippet that doesn't
// match any sample — leave the dropdown showing whatever was last picked.
watch(source, () => {
  // no-op, but binding the watcher ensures Vue tracks source.value properly.
});
</script>

<style scoped>
.playground {
  position: relative;
}

.pg-hero {
  padding: 8rem 0 2rem;
}

.pg-hero .container {
  max-width: 1180px;
  margin: 0 auto;
  padding: 0 2rem;
}

.pg-hero h1 {
  font-size: clamp(2rem, 3.6vw, 2.8rem);
  letter-spacing: -0.025em;
  margin: 1rem 0 1.2rem;
  font-weight: 700;
  line-height: 1.1;
}

.pg-hero .lead {
  color: var(--text-muted);
  max-width: 720px;
  font-size: 1.05rem;
}

.eyebrow {
  color: var(--text-subtle);
  font-size: 0.78rem;
  text-transform: uppercase;
  letter-spacing: 0.16em;
}

.pg-main {
  padding: 0 0 5rem;
}

.pg-main .container {
  max-width: 1180px;
  margin: 0 auto;
  padding: 0 2rem;
}

/* Toolbar */
.pg-toolbar {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.55rem 0.7rem;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 12px 12px 0 0;
  font-size: 0.78rem;
  flex-wrap: wrap;
}

.pg-select {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  color: var(--text-muted);
}

.pg-select select {
  background: var(--bg);
  border: 1px solid var(--border);
  color: var(--text);
  font-family: "JetBrains Mono", monospace;
  font-size: 0.78rem;
  padding: 0.3rem 0.55rem;
  border-radius: 6px;
}

.pg-status {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  font-size: 0.75rem;
  padding: 0.22rem 0.6rem;
  border: 1px solid var(--border);
  border-radius: 999px;
  color: var(--text-muted);
}

.pg-status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
  box-shadow: 0 0 6px currentColor;
}

.pg-status-ok {
  color: #b6e08a;
  border-color: rgba(182, 224, 138, 0.4);
}

.pg-status-info {
  color: var(--accent-secondary);
  border-color: rgba(255, 181, 71, 0.4);
}

.pg-status-err {
  color: var(--accent);
  border-color: rgba(255, 122, 61, 0.5);
}

.pg-stat {
  font-size: 0.74rem;
  color: var(--text-subtle);
}

.pg-stat strong {
  color: var(--text-muted);
  font-weight: 500;
}

.pg-spacer {
  flex: 1;
}

.pg-btn {
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-muted);
  font-family: "JetBrains Mono", monospace;
  font-size: 0.74rem;
  padding: 0.32rem 0.7rem;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.15s ease;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}

.pg-btn:hover:not(:disabled) {
  color: var(--accent);
  border-color: var(--accent);
}

.pg-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.pg-btn-primary {
  color: var(--bg);
  background: var(--accent);
  border-color: var(--accent);
}

.pg-btn-primary:hover {
  background: var(--accent-hover);
  border-color: var(--accent-hover);
  color: var(--bg);
}

.pg-kbd {
  font-size: 0.7rem;
  padding: 0.05rem 0.3rem;
  border: 1px solid rgba(0, 0, 0, 0.25);
  border-radius: 3px;
  background: rgba(0, 0, 0, 0.18);
}

/* Grid layout */
.pg-grid {
  display: grid;
  grid-template-columns: 50% 6px 50%;
  border: 1px solid var(--border);
  border-top: none;
  border-radius: 0 0 12px 12px;
  overflow: hidden;
  background: var(--code-bg);
  height: 600px;
}

.pg-pane {
  display: flex;
  flex-direction: column;
  min-width: 0;
  height: 100%;
  overflow: hidden;
}

.pg-pane-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  padding: 0.45rem 0.9rem;
  background: rgba(255, 255, 255, 0.018);
  border-bottom: 1px solid var(--border);
  font-size: 0.74rem;
  color: var(--text-muted);
  flex-shrink: 0;
}

.pg-filename {
  color: var(--text-muted);
}

.pg-hint {
  color: var(--text-subtle);
}

.pg-error-count {
  color: var(--accent);
  font-weight: 500;
}

.pg-ok-badge {
  color: #b6e08a;
  border: 1px solid rgba(182, 224, 138, 0.3);
  border-radius: 999px;
  padding: 0.05rem 0.5rem;
  font-size: 0.7rem;
}

.pg-cm-host {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.pg-cm-host :deep(.cm-editor) {
  height: 100%;
}

/* Resizer */
.pg-resizer {
  background: var(--border);
  cursor: col-resize;
  position: relative;
  transition: background 0.15s ease;
}

.pg-resizer:hover,
.pg-resizer.active {
  background: var(--accent);
}

.pg-resizer::after {
  content: "";
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 2px;
  height: 30px;
  background: var(--text-subtle);
  border-radius: 1px;
  opacity: 0.4;
}

/* Errors panel below the editor */
.pg-errors-panel {
  margin-top: 0.6rem;
  border: 1px solid rgba(255, 122, 61, 0.25);
  border-radius: 8px;
  background: rgba(255, 122, 61, 0.04);
  overflow: hidden;
  font-size: 0.78rem;
}

.pg-error-row {
  display: flex;
  align-items: flex-start;
  gap: 0.8rem;
  padding: 0.55rem 0.9rem;
  border-bottom: 1px solid rgba(255, 122, 61, 0.12);
  cursor: pointer;
  transition: background 0.12s ease;
}

.pg-error-row:hover {
  background: rgba(255, 122, 61, 0.08);
}

.pg-error-row:last-child {
  border-bottom: none;
}

.pg-error-pos {
  color: var(--accent);
  flex-shrink: 0;
  min-width: 3.5rem;
  font-variant-numeric: tabular-nums;
}

.pg-error-msg {
  color: var(--text);
  white-space: pre-wrap;
}

.pg-fineprint {
  margin-top: 1rem;
  font-size: 0.72rem;
  color: var(--text-subtle);
  display: flex;
  gap: 0.6rem;
  flex-wrap: wrap;
  align-items: center;
}

.pg-fineprint strong {
  color: var(--text-muted);
  font-weight: 500;
}

.pg-fineprint a {
  color: var(--text-muted);
}

.pg-fineprint a:hover {
  color: var(--accent);
}

.dot-sep {
  opacity: 0.5;
}

@media (max-width: 880px) {
  .pg-grid {
    grid-template-columns: 1fr !important;
    grid-template-rows: 1fr 6px 1fr;
    height: 800px;
  }

  .pg-resizer {
    cursor: row-resize;
  }

  .pg-resizer::after {
    width: 30px;
    height: 2px;
  }
}
</style>
