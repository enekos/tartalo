<template>
  <div class="home">
    <!-- Hero -->
    <section class="hero">
      <div class="container hero-grid">
        <div class="hero-text">
          <div class="badge mono">
            <span class="badge-dot"></span>
            <span>pre-alpha · v0</span>
          </div>
          <h1>
            A typed scripting language<br />
            that compiles to <span class="gradient-text">POSIX&nbsp;sh</span>.
          </h1>
          <p class="subtitle">
            Tartalo is a thin TypeScript-ish layer over shell scripting. Catch type
            errors at compile time, get readable <code>.sh</code> at the other end —
            no bashisms, no runtime surprises, no <code>"foo" + 1</code>.
          </p>
          <div class="actions">
            <router-link to="/reference" class="btn-primary">
              Read the spec
              <span class="arrow">→</span>
            </router-link>
            <a href="#install" class="btn-secondary">
              <span class="mono">$</span> install
            </a>
          </div>
          <div class="meta mono">
            <span><span class="dot"></span> statically typed</span>
            <span><span class="dot"></span> POSIX-portable output</span>
            <span><span class="dot"></span> quote-by-default</span>
          </div>
        </div>

        <div class="hero-code">
          <CodeBlock
            filename="hello.tt"
            :code="heroCode"
          />
          <div class="arrow-down mono">↓ tartalo build hello.tt -o hello.sh</div>
          <CodeBlock
            filename="hello.sh"
            lang="sh"
            :code="heroSh"
          />
        </div>
      </div>
    </section>

    <!-- Features -->
    <section class="features" id="features">
      <div class="container">
        <div class="section-head">
          <span class="eyebrow mono">// design goals</span>
          <h2>Shell, but with the safety rails.</h2>
          <p class="lead">
            Tartalo compiles to <code>#!/bin/sh</code> with <code>set -eu</code>.
            All expansions are double-quoted. There's no implicit string-vs-number
            coercion. Runs anywhere <code>sh</code> runs.
          </p>
        </div>

        <div class="feature-grid">
          <article class="feature-card" v-for="f in features" :key="f.title">
            <div class="feature-icon mono">{{ f.icon }}</div>
            <h3>{{ f.title }}</h3>
            <p>{{ f.body }}</p>
          </article>
        </div>
      </div>
    </section>

    <!-- Examples -->
    <section class="examples" id="examples">
      <div class="container">
        <div class="section-head">
          <span class="eyebrow mono">// examples</span>
          <h2>Recognisable, but stricter.</h2>
          <p class="lead">
            If you've written TypeScript or Go, you already know how to read this.
            The runtime target is the surprise — every snippet below compiles to
            plain <code>sh</code>.
          </p>
        </div>

        <div class="example-grid">
          <div class="example-block">
            <h3 class="example-title mono">// records & functions</h3>
            <CodeBlock :code="exRecords" />
          </div>

          <div class="example-block">
            <h3 class="example-title mono">// commands as syntax</h3>
            <CodeBlock :code="exCommands" />
          </div>

          <div class="example-block">
            <h3 class="example-title mono">// optionals, no nulls in disguise</h3>
            <CodeBlock :code="exOptionals" />
          </div>

          <div class="example-block">
            <h3 class="example-title mono">// match on primitives</h3>
            <CodeBlock :code="exMatch" />
          </div>
        </div>
      </div>
    </section>

    <!-- Pipeline -->
    <section class="pipeline">
      <div class="container">
        <div class="section-head">
          <span class="eyebrow mono">// compilation model</span>
          <h2>Lexer · parser · checker · emitter.</h2>
        </div>
        <div class="pipeline-row mono">
          <div class="pipe-stage" v-for="(s, i) in stages" :key="s">
            <span class="stage-num">{{ String(i + 1).padStart(2, "0") }}</span>
            <span class="stage-name">{{ s }}</span>
          </div>
        </div>
        <p class="pipeline-note">
          Every reachable <code>.tt</code> file is bundled into one
          <code>.sh</code>. Imported names are mangled to
          <code class="mono">__m&lt;id&gt;__&lt;name&gt;</code> to avoid collisions.
          The entry module's symbols keep their plain names.
        </p>
      </div>
    </section>

    <!-- Install -->
    <section class="install" id="install">
      <div class="container">
        <div class="section-head">
          <span class="eyebrow mono">// install</span>
          <h2>Build it. Run it.</h2>
          <p class="lead">
            The compiler is written in Go. There's no package, no installer — just
            <code>go build</code>.
          </p>
        </div>

        <div class="install-grid">
          <div>
            <h4 class="block-title mono">// build</h4>
            <CodeBlock lang="sh" :code="buildCmd" />
          </div>
          <div>
            <h4 class="block-title mono">// usage</h4>
            <CodeBlock lang="sh" :code="usageCmd" />
          </div>
        </div>
      </div>
    </section>

    <!-- CTA -->
    <section class="cta">
      <div class="container">
        <div class="cta-card">
          <div>
            <span class="eyebrow mono">// next</span>
            <h2>Read the language reference.</h2>
            <p class="lead">
              Every keyword, every built-in, every codegen rule — on one page.
            </p>
          </div>
          <router-link to="/reference" class="btn-primary cta-btn">
            Open the spec
            <span class="arrow">→</span>
          </router-link>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import CodeBlock from "../components/CodeBlock.vue";

const heroCode = `// hello.tt
func greet(name: string): string {
  return "Hello, \${name}!"
}

func main(): void {
  let who: string = env("USER")
  echo(greet(who))
}`;

const heroSh = `#!/bin/sh
set -eu

greet() {
  __p1="$1"
  __ret="Hello, \${__p1}!"
}

main() {
  who="\${USER:-}"
  greet "$who"
  echo "$__ret"
}

main "$@"`;

const features = [
  {
    icon: "T",
    title: "Strong static typing",
    body:
      "No undefined-variable surprises. No implicit string-vs-number bugs. Function parameter and return types are required; everything else is inferred.",
  },
  {
    icon: "”",
    title: "Quote-by-default safety",
    body:
      "Every expansion is double-quoted in codegen. Spaces, globs, and weird filenames don't bite — the generated sh is boring on purpose.",
  },
  {
    icon: "$",
    title: "Shell as syntax",
    body:
      "Backticks run commands and substitute their stdout. A backtick statement runs for side effects. exec(), execTimeout(), fetch() — all first-class.",
  },
  {
    icon: "?",
    title: "Real optional types",
    body:
      "T? is distinct from T. Use ?? to coalesce, ! to force-unwrap. env() returns string?, so empty-string and unset are no longer the same thing.",
  },
  {
    icon: "{}",
    title: "Records by value",
    body:
      "Named record types group fixed fields. Field types are primitives in v0. Records compile to a name-prefixed family of shell variables.",
  },
  {
    icon: "λ",
    title: "Functions as values",
    body:
      "Pass top-level functions by name to map / filter / reduce. Stored as the mangled function name, callable via \"$f\" args.",
  },
  {
    icon: "⇄",
    title: "Modules, not magic",
    body:
      "import { X } from \"./lib.tt\". Cycles are errors. Every reachable file is bundled into one .sh — no runtime module loader.",
  },
  {
    icon: "✓",
    title: "POSIX-portable output",
    body:
      "No bashisms. No [[ ]], no arrays-as-arrays, no process substitution. The .sh runs anywhere /bin/sh runs — macOS, Alpine, BusyBox.",
  },
];

const exRecords = `type Person = {
  name: string,
  age: number,
}

func birthday(p: Person): Person {
  return Person{ name: p.name, age: p.age + 1 }
}

func main(): void {
  let alice: Person = { name: "Alice", age: 30 }
  let older: Person = birthday(alice)
  echo(older.name + " is " + str(older.age))
}`;

const exCommands = `func main(): void {
  let files: string = \`ls -1\`
  for line in files {
    if endsWith(line, ".tt") {
      echo("source: " + line)
    }
  }

  // statement-position command runs for side effects
  \`mkdir -p build\`

  let r: Process = exec("go vet ./...")
  if !r.ok { exit(r.code) }
}`;

const exOptionals = `func main(): void {
  let token: string? = env("API_TOKEN")
  let key: string  = token ?? "anon"

  if token == null {
    eprint("warning: no API_TOKEN set")
  }

  // forced unwrap aborts the script with a diagnostic if null
  let must: string = env("HOME")!
  echo("home is " + must)
}`;

const exMatch = `func main(): void {
  let action: string = env("ACTION")
  match action {
    "build" | "compile" => echo("compiling")
    "run"               => echo("running")
    ""                  => echo("usage: ACTION=...")
    _                   => echo("unknown: " + action)
  }
}`;

const stages = ["source.tt", "lexer", "parser", "type checker", "sh emitter", "source.sh"];

const buildCmd = `# clone & build
git clone https://github.com/enekos/tartalo.git
cd tartalo
go build -o tartalo ./cmd/tartalo`;

const usageCmd = `# compile to sh
tartalo build hello.tt -o hello.sh

# compile to a temp file and exec /bin/sh
tartalo run hello.tt

# type-check only, no codegen
tartalo check ./examples/*.tt

# run test declarations
tartalo test hello.tt

# format source
tartalo fmt -w ./examples/*.tt`;
</script>

<style scoped>
.home {
  position: relative;
  overflow: hidden;
}

/* HERO */
.hero {
  padding: 9rem 0 5rem;
  position: relative;
}

.hero::before {
  content: "";
  position: absolute;
  inset: 0;
  background:
    linear-gradient(180deg, transparent, rgba(0, 0, 0, 0.4)),
    repeating-linear-gradient(
      90deg,
      transparent 0,
      transparent 79px,
      rgba(255, 255, 255, 0.025) 80px
    );
  pointer-events: none;
  mask-image: linear-gradient(180deg, black 30%, transparent);
}

.hero-grid {
  display: grid;
  grid-template-columns: 1.05fr 1fr;
  gap: 4rem;
  align-items: center;
  position: relative;
  z-index: 1;
}

.hero-text h1 {
  font-size: clamp(2.4rem, 4.6vw, 3.9rem);
  letter-spacing: -0.025em;
  margin: 1.2rem 0 1.5rem;
  font-weight: 700;
  line-height: 1.05;
}

.subtitle {
  font-size: 1.1rem;
  color: var(--text-muted);
  max-width: 560px;
  line-height: 1.6;
  margin-bottom: 2rem;
}

.subtitle code {
  font-size: 0.9em;
}

.badge {
  display: inline-flex;
  align-items: center;
  gap: 0.55rem;
  padding: 0.4rem 0.85rem;
  border: 1px solid var(--border);
  border-radius: 100px;
  font-size: 0.78rem;
  color: var(--text-muted);
  background: rgba(255, 255, 255, 0.02);
}

.badge-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 12px var(--accent);
  animation: pulse 2.5s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.5; transform: scale(0.85); }
}

.actions {
  display: flex;
  gap: 0.8rem;
  flex-wrap: wrap;
  margin-bottom: 2.2rem;
}

.arrow {
  transition: transform 0.2s ease;
}

.btn-primary:hover .arrow {
  transform: translateX(3px);
}

.meta {
  display: flex;
  gap: 1.6rem;
  flex-wrap: wrap;
  font-size: 0.78rem;
  color: var(--text-muted);
}

.meta span {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}

.dot {
  display: inline-block;
  width: 4px;
  height: 4px;
  background: var(--accent);
  border-radius: 50%;
}

.hero-code {
  position: relative;
}

.arrow-down {
  display: block;
  text-align: center;
  margin: 0.4rem 0 0.4rem;
  color: var(--text-subtle);
  font-size: 0.78rem;
  letter-spacing: 0.04em;
}

@media (max-width: 960px) {
  .hero-grid {
    grid-template-columns: 1fr;
    gap: 3rem;
  }
  .hero {
    padding: 7rem 0 3rem;
  }
}

/* SECTIONS */
section {
  padding: 5rem 0;
}

.section-head {
  margin-bottom: 3rem;
  max-width: 720px;
}

.eyebrow {
  display: inline-block;
  color: var(--accent);
  font-size: 0.8rem;
  margin-bottom: 0.75rem;
  font-weight: 500;
  letter-spacing: 0.02em;
}

.section-head h2 {
  font-size: clamp(1.9rem, 3.2vw, 2.6rem);
  letter-spacing: -0.025em;
  margin-bottom: 1rem;
  font-weight: 700;
}

.lead {
  font-size: 1.05rem;
  color: var(--text-muted);
  line-height: 1.65;
  margin: 0;
  max-width: 640px;
}

.lead code {
  font-size: 0.88em;
}

/* FEATURES */
.features {
  border-top: 1px solid var(--border);
}

.feature-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 1px;
  background: var(--border);
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
}

.feature-card {
  background: var(--bg);
  padding: 1.8rem 1.6rem;
  transition: background 0.2s ease;
}

.feature-card:hover {
  background: var(--surface);
}

.feature-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  border: 1px solid var(--border-strong);
  color: var(--accent);
  border-radius: 8px;
  font-weight: 700;
  font-size: 1rem;
  margin-bottom: 1rem;
}

.feature-card h3 {
  font-size: 1.05rem;
  font-weight: 600;
  margin-bottom: 0.5rem;
  letter-spacing: -0.01em;
}

.feature-card p {
  font-size: 0.92rem;
  color: var(--text-muted);
  line-height: 1.55;
  margin: 0;
}

/* EXAMPLES */
.examples {
  border-top: 1px solid var(--border);
}

.example-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(420px, 1fr));
  gap: 2rem;
}

.example-title {
  color: var(--text-muted);
  font-size: 0.82rem;
  font-weight: 400;
  margin: 0 0 0.4rem;
}

@media (max-width: 720px) {
  .example-grid {
    grid-template-columns: 1fr;
  }
}

/* PIPELINE */
.pipeline {
  border-top: 1px solid var(--border);
}

.pipeline-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0;
  border: 1px solid var(--border);
  border-radius: 10px;
  overflow: hidden;
  margin-top: 1rem;
}

.pipe-stage {
  flex: 1 1 0;
  min-width: 130px;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  padding: 1.2rem 1rem;
  border-right: 1px solid var(--border);
  background: var(--bg-secondary);
}

.pipe-stage:last-child {
  border-right: none;
}

.stage-num {
  color: var(--text-subtle);
  font-size: 0.75rem;
}

.stage-name {
  color: var(--text);
  font-size: 0.95rem;
  font-weight: 500;
}

.pipeline-note {
  margin-top: 1.6rem;
  color: var(--text-muted);
  font-size: 0.95rem;
  max-width: 760px;
  line-height: 1.65;
}

/* INSTALL */
.install {
  border-top: 1px solid var(--border);
}

.install-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 2rem;
}

.block-title {
  color: var(--text-muted);
  font-size: 0.82rem;
  font-weight: 400;
  margin: 0 0 0.4rem;
}

@media (max-width: 720px) {
  .install-grid {
    grid-template-columns: 1fr;
  }
}

/* CTA */
.cta {
  border-top: 1px solid var(--border);
  padding-bottom: 6rem;
}

.cta-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 2rem;
  flex-wrap: wrap;
  background:
    linear-gradient(135deg, rgba(255, 122, 61, 0.08), rgba(255, 181, 71, 0.04));
  border: 1px solid var(--border-strong);
  border-radius: 12px;
  padding: 2.5rem 2.5rem;
}

.cta-card h2 {
  font-size: clamp(1.6rem, 2.4vw, 2.1rem);
  margin: 0.6rem 0 0.5rem;
}

.cta-btn {
  flex-shrink: 0;
}
</style>
