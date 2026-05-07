<template>
  <div class="reference">
    <header class="ref-hero">
      <div class="container">
        <span class="eyebrow mono">// language reference · v0</span>
        <h1>Tartalo language spec</h1>
        <p class="lead">
          Everything in this document is a snapshot of <code>SPEC.md</code> at the
          current state of the compiler. The language is pre-alpha — every line
          here is subject to change.
        </p>
        <div class="ref-meta mono">
          <span>file extension <strong>.tt</strong></span>
          <span>·</span>
          <span>targets <strong>sh · native</strong></span>
          <span>·</span>
          <span>flags <strong>set -eu</strong></span>
        </div>
      </div>
    </header>

    <div class="container ref-layout">
      <!-- TOC -->
      <aside class="toc">
        <p class="toc-title mono">// contents</p>
        <ul>
          <li v-for="entry in toc" :key="entry.id" :class="{ sub: entry.sub }">
            <a
              :href="`#${entry.id}`"
              :class="{ active: active === entry.id }"
              @click.prevent="goTo(entry.id)"
            >{{ entry.label }}</a>
          </li>
        </ul>
      </aside>

      <article class="ref-content">
        <!-- Goals -->
        <section :id="ids.goals" ref="secRefs">
          <h2>Goals</h2>
          <ul class="bullets">
            <li><strong>Strong static typing.</strong> No undefined-variable surprises, no implicit string-vs-number bugs.</li>
            <li><strong>Readable shell output.</strong> The generated <code>.sh</code> should be POSIX-portable and reasonable to read.</li>
            <li><strong>Quote-by-default safety.</strong> All expansions are double-quoted in codegen so spaces and globs do not bite.</li>
            <li><strong>Shell as a first-class concern.</strong> Running commands and using their output is part of the syntax, not a wart.</li>
          </ul>

          <h3>Non-goals (for v0)</h3>
          <ul class="bullets">
            <li>Full TS/JS feature parity. No classes, no async.</li>
            <li>Bash-isms (arrays, <code>[[ ]]</code>, process substitution). The output is plain <code>sh</code>.</li>
            <li>Performance competitive with hand-tuned shell.</li>
          </ul>
        </section>

        <!-- File extension / Modules -->
        <section :id="ids.modules" ref="secRefs">
          <h2>Modules</h2>
          <p>
            File extension: <code>.tt</code>.
            A program may span multiple files. Imports go at the top of a file;
            everything else (functions, types, variables) follows. Only declarations
            prefixed with <code>export</code> are visible outside their module.
          </p>

          <CodeBlock filename="lib/math.tt" :code="codeModuleA" />
          <CodeBlock filename="main.tt" :code="codeModuleB" />

          <p>
            Module paths are interpreted relative to the importing file's directory.
            The compiler bundles every reachable file into one <code>.sh</code> output,
            with global names from imported modules mangled to
            <code class="mono">__m&lt;id&gt;__&lt;name&gt;</code> to avoid collisions.
            The entry module's symbols keep their plain names for readability.
          </p>

          <h3>Constraints in v0</h3>
          <ul class="bullets">
            <li>Only the named-import form: <code>import {{ '{ a, b }' }} from "./path.tt"</code>.</li>
            <li>Imports must reference names that the target module declared with <code>export</code>. Cycles are reported as errors.</li>
            <li>Two record types declared with the same name in different modules are distinct types; nominal equality is by-pointer, not by-name.</li>
          </ul>
        </section>

        <!-- Lexical -->
        <section :id="ids.lexical" ref="secRefs">
          <h2>Lexical structure</h2>
          <ul class="bullets">
            <li>Line comments: <code>// ...</code></li>
            <li>Identifiers: <code>[A-Za-z_][A-Za-z0-9_]*</code></li>
            <li>Numbers: integer literals only in v0 (<code>42</code>, <code>-3</code>).</li>
            <li>Strings: double-quoted, with <code>\n \t \\ \" \$</code> escapes and <code>${'${expr}'}</code> interpolation.</li>
            <li>Command literals: backticks, e.g. <code>`ls -1`</code>. Substitutes to a <code>string</code> (stdout, trailing newline trimmed).</li>
            <li>Keywords: <code>let</code>, <code>const</code>, <code>func</code>, <code>return</code>, <code>if</code>, <code>else</code>, <code>for</code>, <code>in</code>, <code>match</code>, <code>type</code>, <code>import</code>, <code>export</code>, <code>test</code>, <code>defer</code>, <code>parallel</code>, <code>task</code>, <code>tool</code>, <code>agent</code>, <code>as</code>, <code>null</code>, <code>true</code>, <code>false</code>, <code>string</code>, <code>number</code>, <code>float</code>, <code>bool</code>, <code>void</code>.</li>
          </ul>
        </section>

        <!-- Types -->
        <section :id="ids.types" ref="secRefs">
          <h2>Types</h2>
          <p>The v0 type set is small and maps tightly to a shell representation:</p>

          <table class="spec-table">
            <thead>
              <tr>
                <th>Tartalo</th>
                <th>Generated sh representation</th>
              </tr>
            </thead>
            <tbody>
              <tr><td>string</td><td>a shell variable holding text</td></tr>
              <tr><td>number</td><td>a shell variable holding a base-10 int</td></tr>
              <tr><td>float</td><td>a shell variable holding a textual float; arithmetic done via <code>awk</code></td></tr>
              <tr><td>bool</td><td>a shell variable holding <code>1</code> (true) or <code>0</code> (false) — same encoding as <code>$(( ))</code> comparisons</td></tr>
              <tr><td>void</td><td>functions with no return value</td></tr>
              <tr><td>T[]</td><td>a shell variable holding the elements joined by newlines</td></tr>
              <tr><td>func(T...): R</td><td>a shell variable holding the mangled function name (callable via <code>"$f" args</code>)</td></tr>
            </tbody>
          </table>

          <p>
            There is no implicit conversion. <code>"foo" + 1</code> is a type error.
            Use <code>str(n)</code> to convert a number to a string.
          </p>

          <div class="callout">
            <strong>Caveat for arrays:</strong> because the codegen represents
            <code>T[]</code> as a newline-joined string, individual elements must
            not contain literal newlines. This is enough for typical scripting use
            (filenames, ids, words) and keeps the generated sh predictable, but it
            is a real limitation worth knowing about.
          </div>
        </section>

        <!-- Declarations -->
        <section :id="ids.declarations" ref="secRefs">
          <h2>Declarations</h2>
          <CodeBlock :code="codeDecls" />
          <p>
            Empty array literals always need an annotation, since there is nothing
            to infer the element type from:
          </p>
          <CodeBlock :code="codeEmptyArr" />
          <p>Function parameter and return types are still always required.</p>
        </section>

        <!-- Optional types -->
        <section :id="ids.optionals" ref="secRefs">
          <h2>Optional types</h2>
          <p>
            Any non-array, non-optional type <code>T</code> can be made nullable
            with the postfix <code>?</code>:
          </p>
          <CodeBlock :code="codeOptionals" />

          <h3>Allowed operations on optional values</h3>
          <ul class="bullets">
            <li><code>expr ?? default</code> — coalesce. Result is <code>T</code> (non-optional). The default must have type <code>T</code>.</li>
            <li><code>expr!</code> — forced unwrap. Aborts the script with a diagnostic if the operand is null.</li>
            <li><code>expr == null</code>, <code>expr != null</code> — null check.</li>
          </ul>

          <p>
            Direct equality, ordering, arithmetic, etc. are <em>rejected</em> on
            optional values — use <code>??</code> or <code>!</code> first. There is
            no flow-narrowing in v0, so even inside an
            <code>if x != null { … }</code> block <code>x</code> is still
            <code>T?</code>; use <code>x!</code> to access the underlying value.
          </p>
          <p>
            <code>null</code> may not appear by itself (<code>let z = null</code> is
            rejected); always provide the type via an annotation, the surrounding
            context (param/return), or a non-null sibling expression.
          </p>

          <h3>Optional fields in records</h3>
          <CodeBlock :code="codeOptFields" />
          <p>
            <code>env(name): string?</code> — note that the empty string and "unset"
            are now distinct: an env var set to <code>""</code> returns the empty
            string (non-null), an unset var returns <code>null</code>.
          </p>

          <h3>Codegen sketch</h3>
          <p>
            Each optional value is two shell variables: the value, and a
            <code>__null</code> flag (1 = null, 0 = present). Function parameters of
            optional type consume two positional args; optional fields in records
            carry their flag inline; the <code>__ret</code> return slot has a
            sibling <code>__ret__null</code>.
          </p>
        </section>

        <!-- Records -->
        <section :id="ids.records" ref="secRefs">
          <h2>Records</h2>
          <p>Named record types group a fixed set of fields:</p>
          <CodeBlock :code="codeRecords" />
          <p>
            Record literals must appear in a context where the expected type is
            known — either as the initialiser of an annotated <code>let</code>/<code>const</code>,
            the right-hand side of an assignment to a record-typed variable, the
            argument of a record-typed parameter, or the value of a
            <code>return</code> whose function returns a record.
          </p>
          <p>
            Records are passed and returned by <strong>value</strong>: assigning one
            record to another copies every field, and mutations on the copy do not
            affect the original.
          </p>

          <p>
            Field types may be primitives (<code>string</code>, <code>number</code>,
            <code>bool</code>), optional primitives (<code>string?</code>,
            <code>number?</code>, <code>bool?</code>), other records (nested arbitrarily
            deep, as long as the type graph is acyclic), or arrays of primitives
            (<code>string[]</code>, <code>number[]</code>, <code>bool[]</code>,
            <code>float[]</code>):
          </p>
          <CodeBlock :code="codeNestedRecords" />

          <h3>v0 limitations</h3>
          <ul class="bullets">
            <li>No optional records (<code>Addr?</code>) as fields or values.</li>
            <li>Scalar <code>float</code> is not allowed as a record field (use <code>float[]</code> if you need float storage in a record).</li>
            <li>No structural typing — record types are always referred to by name.</li>
            <li>No equality between records yet — compare individual fields.</li>
            <li>Arrays of records are supported (<code>Person[]</code>), but the element record's leaves must all be primitives — no array fields inside.</li>
          </ul>

          <h3>Record spread</h3>
          <p>
            A record literal may begin with a <code>...source</code> spread that
            copies every field from <code>source</code> (which must have the same
            record type as the literal). Named fields after the spread override
            the corresponding entries from the source:
          </p>
          <CodeBlock :code="codeRecordSpread" />
          <p>
            The spread must be the first entry in the literal. Cross-type spread
            (copying fields from a structurally-similar but different record
            type) is not allowed — use <code>as</code> instead.
          </p>

          <h3>Type casts</h3>
          <p>
            <code>expr as TargetRecord</code> reinterprets a record value as a
            different record type when the target's field set is a subset of the
            source's, with each shared field's type assignable from source to
            target. Useful for narrowing a wide record into a purpose-built
            shape:
          </p>
          <CodeBlock :code="codeRecordCast" />
          <p>
            Casts are restricted to record-to-record conversions in v0;
            primitives, arrays, and sums use their existing builtins
            (<code>str</code>, <code>num</code>, <code>floatOf</code>,
            <code>intOf</code>).
          </p>

          <h3>Codegen</h3>
          <p>
            Each record value is represented as a <strong>name prefix</strong>:
            a record-typed variable named <code>p</code> lives as the set of shell
            variables <code>p__name</code>, <code>p__age</code>, etc. There is no
            top-level <code>p</code> variable. Nested records flatten by extending
            the prefix (<code>p.addr.city</code> lives at <code>p__addr__city</code>);
            array fields are a single newline-joined slot (<code>p__tags</code>).
            Function calls expand record arguments into one positional parameter per
            leaf field; record returns write into <code>__ret__&lt;leaf&gt;</code>
            and the caller copies them into the destination prefix.
          </p>

          <h3>Arrays of records</h3>
          <p>
            <code>Person[]</code> is supported when each leaf of the element
            record is a primitive or optional primitive (no array leaves).
            Indexing, iteration, and <code>len()</code> all work; mutating
            element fields in place is not yet supported.
          </p>
          <CodeBlock :code="codeArrayOfRecords" />
          <p>
            The array lives in one shell variable: rows separated by newlines,
            leaf fields within a row separated by ASCII Unit Separator
            (<code>\037</code>, exposed at runtime as <code>${'$'}{__tt_us}</code>).
          </p>
        </section>

        <!-- Tagged unions -->
        <section :id="ids.sums" ref="secRefs">
          <h2>Tagged unions</h2>
          <p>
            A <code>type</code> declaration may list <code>|</code>-separated
            variants. Each variant is either a unit tag or carries a record-
            style payload. <code>match</code> destructures by name.
          </p>
          <CodeBlock :code="codeSum" />
          <p>
            Construction uses the variant name. Unit variants are bare
            identifiers (<code>let s: Shape = Empty</code>); data-carrying
            variants use the record-literal form
            (<code>Circle{r: 4}</code>).
          </p>
          <h3>v0 limitations</h3>
          <ul class="bullets">
            <li>Variant fields must be primitives or optional primitives.</li>
            <li><code>match</code> is a statement, not an expression.</li>
            <li>No exhaustiveness check beyond the wildcard requirement.</li>
          </ul>
        </section>

        <!-- Defer -->
        <section :id="ids.defer" ref="secRefs">
          <h2>Defer</h2>
          <p>
            <code>defer { ... }</code> registers a block to run when the
            enclosing function exits. Multiple defers run last-registered-
            first-run.
          </p>
          <CodeBlock :code="codeDefer" />
          <p>
            Defer fires on every explicit <code>return</code>, on fall-through
            end-of-body, and on the early-return path of the <code>?</code>
            operator. It does <strong>not</strong> fire when the script is
            aborted via <code>exit()</code>. <code>return</code> is rejected
            inside a defer body.
          </p>
        </section>

        <!-- Parallel -->
        <section :id="ids.parallel" ref="secRefs">
          <h2>Parallel tasks</h2>
          <p>
            <code>parallel { task { ... } task { ... } ... }</code> runs every
            inner task concurrently and joins them all before continuing past
            the closing brace. Sh lowers each task to a backgrounded subshell
            joined by <code>wait</code>; native lowers to a goroutine driven
            by a <code>sync.WaitGroup</code>.
          </p>
          <CodeBlock :code="codeParallel" />
          <p>
            The body of <code>parallel</code> may only contain
            <code>task</code> statements. A task body may not assign to outer-
            scope variables, may not <code>return</code> or <code>defer</code>,
            and may not nest another <code>parallel</code>. These restrictions
            keep both backends observably equivalent.
          </p>
        </section>

        <!-- Result and ? -->
        <section :id="ids.result" ref="secRefs">
          <h2>Result and the <code>?</code> operator</h2>
          <p>
            There is no built-in <code>Result</code> type — define your own
            sum that matches the Result shape: variants
            <code>Ok{value: T}</code> and <code>Err{error: E}</code>. The
            <code>?</code> postfix operator on a Result-shaped value short-
            circuits to the enclosing function's matching <code>Err</code>.
          </p>
          <CodeBlock :code="codeResult" />
          <p>
            Constraints: the operand must be Result-shaped; the enclosing
            function's return type must be Result-shaped with the same
            <code>Err</code> payload type; defer blocks registered before
            <code>?</code> still run on the early-return path.
          </p>
        </section>

        <!-- Pipelines -->
        <section :id="ids.pipelines" ref="secRefs">
          <h2>Pipelines</h2>
          <p>
            <code>|&gt;</code> threads its left-hand side as the first
            argument of a function call.
          </p>
          <CodeBlock :code="codePipeline" />
          <p>
            Pipelines desugar to nested calls at parse time, so they cost
            nothing at runtime and play with every other feature (records,
            sums, optionals, <code>?</code>, defer) by default.
          </p>
        </section>

        <!-- Generics -->
        <section :id="ids.generics" ref="secRefs">
          <h2>Generics</h2>
          <p>
            A function may declare one or more type parameters in <code>&lt;...&gt;</code>
            between its name and parameter list. Type arguments are inferred from
            the call site — there is no explicit type-argument syntax.
          </p>
          <CodeBlock :code="codeGenerics" />
          <p>
            Type parameters are unbounded: they accept any Tartalo type that's legal
            as an array element, function parameter, or record field. The operations a
            generic body may apply to a value of type <code>T</code> are limited to
            passthrough (let / return / call), array construction, array indexing, and
            the optional operators (<code>??</code>, <code>!</code>, <code>== null</code>).
            Arithmetic, ordering, field access, and function calls on a bare <code>T</code>
            are rejected.
          </p>
          <p>
            Both backends use <strong>monomorphization</strong>: each unique combination
            of type arguments produces one specialised copy of the function. Unused
            generic functions are dead-code eliminated automatically.
          </p>
          <h3>Limitations</h3>
          <ul class="bullets">
            <li>No explicit type-argument syntax (<code>f&lt;int&gt;(x)</code>); inference only.</li>
            <li>No bounded constraints — all type parameters are universally quantified.</li>
            <li>No generic record or sum types.</li>
            <li>Generics on <code>tool</code> and <code>agent</code> declarations are rejected.</li>
          </ul>
        </section>

        <!-- Functions -->
        <section :id="ids.functions" ref="secRefs">
          <h2>Functions</h2>
          <CodeBlock :code="codeFuncs" />
          <p>
            Functions compile to sh functions. Parameters are positional. Return
            values are passed back via a hidden <code>__ret</code> variable (sh has
            no return values in the language sense, only exit codes).
          </p>

          <h3>Functions as values</h3>
          <p>
            Top-level functions can be stored in variables typed
            <code>func(T...): R</code> and called through the variable. The
            stored value is the mangled function name, callable via
            <code>"$f" args</code> in sh and as a Go <code>func</code> value
            on the native target.
          </p>
          <CodeBlock :code="codeFuncValue" />

          <h3>Anonymous functions (closures)</h3>
          <p>
            Function literals can appear in any expression position. They have
            the same shape as a named function, just without the name:
          </p>
          <CodeBlock :code="codeFuncLit" />
          <p>
            Lambdas may capture variables from the enclosing scope:
          </p>
          <CodeBlock :code="codeFuncCapture" />
          <div class="callout">
            <strong>Target compatibility:</strong> on the native target,
            captures work like Go's closures — including for closures that
            <em>escape</em> their defining function (e.g.
            <code>func makeAdder(n) { return func(x) { return x + n } }</code>).
            On the sh target, captures work via dynamic scoping, which is fine
            for the common case where the lambda runs <em>while</em> the
            defining frame is still live (e.g. inside <code>map</code>). A
            closure that escapes its defining frame on the sh target will read
            uninitialised free variables at runtime; if you need escaping
            closures, use <code>--target=native</code>.
          </div>
        </section>

        <!-- Control flow -->
        <section :id="ids.controlflow" ref="secRefs">
          <h2>Control flow</h2>
          <CodeBlock :code="codeControl" />
          <p><code>a..b</code> is a half-open numeric range.</p>

          <h3><code>match</code></h3>
          <p><code>match</code> dispatches on a primitive value:</p>
          <CodeBlock :code="codeMatch" />
          <p>
            Patterns are literal <code>string</code>, <code>number</code>, or
            <code>bool</code> values, with <code>|</code> for alternatives and
            <code>_</code> for the wildcard. Arms compile to a sh <code>case</code>.
            String and numeric patterns are single-quoted, so glob metacharacters in
            the pattern match literally.
          </p>
        </section>

        <!-- Strings -->
        <section :id="ids.interpolation" ref="secRefs">
          <h2>String interpolation</h2>
          <CodeBlock :code="codeInterp" />
          <p>Compiles to <code>echo "Hello, ${'${who}'}!"</code> with proper quoting.</p>
        </section>

        <!-- Commands -->
        <section :id="ids.commands" ref="secRefs">
          <h2>Commands</h2>
          <p>
            Backticks run a command and substitute its stdout (trailing newline
            stripped):
          </p>
          <CodeBlock :code="codeCmd1" />
          <p>A command in statement position runs for side effects:</p>
          <CodeBlock :code="codeCmd2" />
        </section>

        <!-- Builtins -->
        <section :id="ids.builtins" ref="secRefs">
          <h2>Builtins</h2>

          <div class="builtins" v-for="group in builtins" :key="group.title">
            <h3 :id="group.id">{{ group.title }}</h3>
            <p v-if="group.intro" class="group-intro">{{ group.intro }}</p>
            <ul class="builtin-list">
              <li v-for="b in group.items" :key="b.sig">
                <code class="sig">{{ b.sig }}</code>
                <span class="desc" v-if="b.desc">— <span v-html="b.desc"></span></span>
              </li>
            </ul>
          </div>

          <h3>Predeclared types</h3>
          <CodeBlock :code="codePredeclared" />
          <p>
            <code>fetch</code> shells out to <code>curl -sS -L</code>; connection /
            DNS failures produce <code>status: 0, ok: false</code>. <code>exec</code>
            runs the command via <code>sh -c</code>, captures streams to temp files,
            and uses <code>|| code=$?</code> so the host script's
            <code>set -e</code> doesn't propagate non-zero exits.
          </p>
        </section>

        <!-- Operators -->
        <section :id="ids.operators" ref="secRefs">
          <h2>Operators</h2>
          <table class="spec-table">
            <thead>
              <tr><th>Category</th><th>Operators</th></tr>
            </thead>
            <tbody>
              <tr><td>Arithmetic on <code>number</code></td><td><code>+ - * / %</code></td></tr>
              <tr><td>String</td><td><code>+</code> (concat), <code>== !=</code>, ordering <code>&lt; &lt;= &gt; &gt;=</code> (lexicographic via awk)</td></tr>
              <tr><td>Comparison on <code>number</code></td><td><code>== != &lt; &lt;= &gt; &gt;=</code></td></tr>
              <tr><td>Boolean</td><td><code>&amp;&amp; || !</code></td></tr>
              <tr><td>Indexing on arrays</td><td><code>arr[i]</code> (0-based)</td></tr>
              <tr><td>Grouping</td><td><code>( ... )</code></td></tr>
              <tr><td>Optional</td><td><code>?? !</code> (coalesce / forced unwrap), <code>?</code> (Result short-circuit)</td></tr>
              <tr><td>Cast</td><td><code>expr as Type</code> (record-to-record only)</td></tr>
              <tr><td>Record spread</td><td><code>Foo{...source, field: value}</code> (must be first in literal)</td></tr>
            </tbody>
          </table>
        </section>

        <!-- Compilation model -->
        <section :id="ids.model" ref="secRefs">
          <h2>Compilation model</h2>
          <CodeBlock lang="text" :code="codeModel" />
          <p>
            Two backends share the same frontend (lexer, parser, checker).
            The default <code>--target=sh</code> produces <code>#!/bin/sh</code>
            with <code>set -eu</code>; <code>--target=native</code> emits a
            self-contained Go program and compiles it with the host's
            <code>go build</code> into a statically-linked native executable.
          </p>
          <p>
            <code>bool</code> in the sh backend follows POSIX exit-code
            convention (0 = true) so boolean tests can pass through to native
            shell when useful. The native backend uses Go's native
            <code>bool</code>; only <code>str(true)</code> /
            <code>str(false)</code> deliberately produce <code>"1"</code> /
            <code>"0"</code> so that program output is identical across
            backends.
          </p>

          <h3>Native target</h3>
          <CodeBlock lang="text" :code="codeNativeUsage" />
          <p>
            Requires a <code>go</code> toolchain on <code>PATH</code> at
            compile time. The resulting binary has no runtime dependency on
            Go (or on a shell). Cross-compilation uses Go's
            <code>GOOS</code> / <code>GOARCH</code> machinery: every (os,
            arch) pair Go supports works, including <code>darwin/arm64</code>,
            <code>linux/amd64</code>, <code>linux/arm64</code>, and
            <code>windows/amd64</code>.
          </p>

          <h3>Type mapping (Tartalo → Go)</h3>
          <table class="spec-table">
            <thead>
              <tr><th>Tartalo</th><th>Go</th></tr>
            </thead>
            <tbody>
              <tr><td><code>string</code> / <code>number</code> / <code>float</code> / <code>bool</code></td><td><code>string</code> / <code>int64</code> / <code>float64</code> / <code>bool</code></td></tr>
              <tr><td><code>T[]</code></td><td><code>[]T</code></td></tr>
              <tr><td><code>T?</code></td><td><code>*T</code> (<code>nil</code> = none)</td></tr>
              <tr><td>record <code>type Foo = {...}</code></td><td><code>type Tt_Foo struct {...}</code></td></tr>
              <tr><td><code>func(a: T): R</code></td><td><code>func(a T) R</code></td></tr>
            </tbody>
          </table>

          <h3>Documented divergences</h3>
          <ul class="bullets">
            <li><strong>Regex.</strong> Sh runs POSIX ERE through awk; native uses Go's <code>regexp</code> (RE2). For the patterns Tartalo programs actually use the two agree, but RE2 has no backreferences, so a pattern that uses <code>\1</code> is rejected at runtime by the native backend.</li>
            <li><strong>JSON.</strong> Sh shells out to <code>jq</code>; the native backend implements the subset of jq paths Tartalo programs use (<code>.</code>, <code>.field</code>, <code>.field.nested</code>, <code>.field[N]</code>) without depending on <code>jq</code> being on <code>PATH</code>.</li>
            <li><strong>Backtick command literals.</strong> Both backends route through a shell — <code>/bin/sh -c</code> on POSIX, <code>cmd /c</code> on Windows. Pipelines that depend on POSIX-only utilities will not survive on a Windows target.</li>
          </ul>
        </section>

        <div class="ref-end">
          <span class="eyebrow mono">// EOF</span>
          <p>
            That's the whole spec, for now. If you find a contradiction between this
            page and the compiler, the compiler wins — and please file an issue.
          </p>
          <router-link to="/" class="btn-secondary">
            <span class="mono">←</span>
            Back home
          </router-link>
        </div>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import CodeBlock from "../components/CodeBlock.vue";

const ids = {
  goals: "goals",
  modules: "modules",
  lexical: "lexical",
  types: "types",
  declarations: "declarations",
  generics: "generics",
  optionals: "optionals",
  records: "records",
  sums: "sums",
  defer: "defer",
  parallel: "parallel",
  result: "result",
  pipelines: "pipelines",
  functions: "functions",
  controlflow: "control-flow",
  interpolation: "interpolation",
  commands: "commands",
  builtins: "builtins",
  operators: "operators",
  model: "model",
};

const toc = [
  { id: ids.goals, label: "Goals" },
  { id: ids.modules, label: "Modules" },
  { id: ids.lexical, label: "Lexical structure" },
  { id: ids.types, label: "Types" },
  { id: ids.declarations, label: "Declarations" },
  { id: ids.generics, label: "Generics" },
  { id: ids.optionals, label: "Optional types" },
  { id: ids.records, label: "Records" },
  { id: ids.sums, label: "Tagged unions" },
  { id: ids.defer, label: "Defer" },
  { id: ids.parallel, label: "Parallel tasks" },
  { id: ids.result, label: "Result & ?" },
  { id: ids.pipelines, label: "Pipelines" },
  { id: ids.functions, label: "Functions" },
  { id: ids.controlflow, label: "Control flow" },
  { id: ids.interpolation, label: "String interpolation" },
  { id: ids.commands, label: "Commands" },
  { id: ids.builtins, label: "Builtins" },
  { id: "core", label: "core", sub: true },
  { id: "strings", label: "strings", sub: true },
  { id: "float", label: "float", sub: true },
  { id: "fileio", label: "file I/O", sub: true },
  { id: "paths", label: "paths", sub: true },
  { id: "subprocess", label: "subprocess & HTTP", sub: true },
  { id: "regex", label: "regex", sub: true },
  { id: "higher-order", label: "higher-order", sub: true },
  { id: "process", label: "process / time", sub: true },
  { id: "json", label: "json", sub: true },
  { id: "test", label: "test framework", sub: true },
  { id: "mocks", label: "mocks", sub: true },
  { id: "evals", label: "evals", sub: true },
  { id: "scoring", label: "scoring metrics", sub: true },
  { id: ids.operators, label: "Operators" },
  { id: ids.model, label: "Compilation model" },
];

const builtins = [
  {
    id: "core",
    title: "Core",
    items: [
      { sig: "echo(s: string): void", desc: "print line to stdout" },
      { sig: "eprint(s: string): void", desc: "print line to stderr" },
      { sig: "str(n: number | float | bool): string", desc: "scalar → string" },
      { sig: "num(s: string): number", desc: "string → int (errors at runtime if not numeric)" },
      { sig: "len(s | T[]): number", desc: "UTF-8 codepoint (rune) count for strings; element count for arrays. Use <code>byteLen</code> for raw byte length." },
      { sig: "env(name: string): string?", desc: "read env var (<code>null</code> if unset, empty string if set to <code>\"\"</code>)" },
      { sig: "exit(code: number): void", desc: "exit with code" },
    ],
  },
  {
    id: "strings",
    title: "Strings",
    items: [
      { sig: "upper(s: string): string" },
      { sig: "lower(s: string): string" },
      { sig: "trim(s: string): string", desc: "strips leading/trailing whitespace (space, tab, CR, LF)" },
      { sig: "replace(s, from, to: string): string", desc: "literal substring replace, no regex" },
      { sig: "contains(s, sub: string): bool" },
      { sig: "startsWith(s, prefix: string): bool" },
      { sig: "endsWith(s, suffix: string): bool" },
      { sig: "slice(s: string, start, end: number): string", desc: "half-open <code>[start, end)</code>, 0-based; <code>start</code> and <code>end</code> are UTF-8 codepoint indices, so the result is always valid UTF-8" },
      { sig: "byteLen(s: string): number", desc: "raw byte length (POSIX <code>wc -c</code> semantics)" },
      { sig: "byteSlice(s: string, start, end: number): string", desc: "half-open byte-index slice. May return an invalid UTF-8 substring when cutting through a multi-byte sequence; prefer <code>slice</code> unless you specifically need bytes." },
      { sig: "split(s, sep: string): string[]" },
      { sig: "join(arr: string[], sep: string): string" },
    ],
  },
  {
    id: "float",
    title: "Float",
    items: [
      { sig: "floatOf(n: number): float", desc: "widen an integer to a float" },
      { sig: "intOf(f: float): number", desc: "truncate a float toward zero" },
      { sig: "parseFloat(s: string): float?", desc: "parse a float, or <code>null</code> if not numeric" },
      { sig: "formatFloat(f: float, decimals: number): string", desc: "format with the given number of decimal places" },
      { sig: "floor(f: float): number", desc: "largest integer ≤ f" },
      { sig: "ceil(f: float): number", desc: "smallest integer ≥ f" },
      { sig: "round(f: float): number", desc: "round to nearest integer (half away from zero)" },
    ],
  },
  {
    id: "fileio",
    title: "File I/O",
    intro:
      'The "abort on error" behaviour is intentional for v0; if you need to inspect the failure, drop down to exec(...) which gives you code, stdout, and stderr.',
    items: [
      { sig: "readFile(path: string): string", desc: "read file contents; aborts on error" },
      { sig: "writeFile(path: string, content: string): void", desc: "write content (overwriting); aborts on error" },
      { sig: "appendFile(path: string, content: string): void", desc: "append content; aborts on error" },
      { sig: "removeFile(path: string): void", desc: "remove a file; idempotent (no error if absent)" },
      { sig: "mkdir(path: string): void", desc: "create a directory and any missing parents; idempotent" },
      { sig: "listDir(path: string): string[]", desc: "list entries (basenames, sorted, including dotfiles)" },
      { sig: "exists(path: string): bool" },
      { sig: "isFile(path: string): bool" },
      { sig: "isDir(path: string): bool" },
      { sig: "stat(path: string): FileInfo", desc: "one-shot metadata bundle. Falls back to BSD <code>stat -f</code> when GNU <code>stat -c</code> isn't available. For a missing path, <code>exists</code> is false and numeric fields are 0." },
      { sig: "readStdin(): string", desc: "read all of stdin" },
    ],
  },
  {
    id: "paths",
    title: "Path manipulation (no I/O)",
    items: [
      { sig: "pathJoin(a: string, b: string): string", desc: "joins two path segments; if <code>b</code> is absolute it wins (Node-style)" },
      { sig: "basename(path: string): string" },
      { sig: "dirname(path: string): string" },
      { sig: "extname(path: string): string", desc: 'extension <em>including</em> the leading dot, or <code>""</code> when the basename has no dot' },
      { sig: "parsePath(path: string): PathParts", desc: "split a path into <code>{ dir, base, name, ext }</code> in one go" },
    ],
  },
  {
    id: "subprocess",
    title: "Subprocesses and HTTP",
    items: [
      { sig: "exec(cmd: string): Process", desc: "run a shell command, capture stdout, stderr, and exit code" },
      { sig: "execTimeout(cmd: string, secs: number): Process", desc: "like <code>exec</code> but kills the command after <code>secs</code>. Aborts the script if neither <code>timeout</code> (GNU) nor <code>gtimeout</code> (macOS coreutils) is on PATH. <code>Process.code</code> is <code>124</code> on timeout." },
      { sig: "fetch(url: string): Response", desc: "HTTP GET (via <code>curl -sS -L</code>)" },
    ],
  },
  {
    id: "regex",
    title: "Regex (POSIX ERE via awk)",
    items: [
      { sig: "regexMatch(s, pat: string): bool", desc: "<code>s ~ pat</code>" },
      { sig: "regexFind(s, pat): string?", desc: "first match, or null" },
      { sig: "regexFindAll(s, pat): string[]", desc: "all non-overlapping matches" },
      { sig: "regexReplace(s, pat, repl: string): string", desc: "<code>gsub(pat, repl, s)</code>. Backslashes and <code>&amp;</code> in <code>repl</code> are escaped before substitution so the replacement is treated as literal text." },
    ],
  },
  {
    id: "higher-order",
    title: "Higher-order",
    intro:
      "The function argument can be a reference to a top-level user-declared function (pass its name: map(xs, double)) or an inline function literal (map(xs, func(x: number): number { return x * x })). Builtins cannot be passed by reference.",
    items: [
      { sig: "map(arr: T[], f: func(T): U): U[]" },
      { sig: "filter(arr: T[], pred: func(T): bool): T[]" },
      { sig: "reduce(arr: T[], init: U, f: func(U, T): U): U" },
    ],
  },
  {
    id: "process",
    title: "Process / time",
    items: [
      { sig: "args(): string[]", desc: "positional args passed to the script (stable from any call site)" },
      { sig: "now(): number", desc: "current Unix timestamp in seconds (<code>date +%s</code>)" },
      { sig: "sleep(seconds: number): void", desc: "POSIX <code>sleep</code> (no fractional seconds guarantee)" },
      { sig: "formatTime(secs: number, fmt: string): string", desc: "format a Unix time using <code>date</code>. Tries BSD <code>-r</code> then GNU <code>-d @</code>, so the same script runs on macOS and Linux." },
    ],
  },
  {
    id: "json",
    title: "JSON",
    intro:
      "The JSON helpers shell out to jq at runtime, so any host running a script that uses them must have jq on PATH.",
    items: [
      { sig: "jsonGet(json: string, path: string): string?", desc: 'extract a value at a jq path. Both "missing path" and "path → null" surface as <code>null</code> on the tartalo side; use <code>jsonHas</code> to disambiguate.' },
      { sig: "jsonHas(json: string, path: string): bool", desc: "true iff the path exists <em>and</em> its value is non-null." },
      { sig: "jsonArray(json: string, path: string): string[]", desc: "array elements as a string[]; each element is jq's stringified form (raw for scalars, JSON for objects/arrays)." },
      { sig: "jsonEscape(s: string): string", desc: "encode a string as a JSON string literal <em>with</em> surrounding quotes. Convenient when hand-building a request body." },
    ],
  },
  {
    id: "test",
    title: "Test framework",
    intro:
      "Tests live next to the implementation, Rust-style: a <code>test \"...\" { ... }</code> block can sit anywhere in any <code>.tt</code> file and is stripped from non-test builds. Run a single file with <code>tartalo test foo.tt</code>; pass a directory and the runner walks it, executing every <code>.tt</code> that contains at least one test declaration. Hidden directories and <code>node_modules</code> are skipped.",
    items: [
      { sig: "assertEq(a, b): void", desc: "abort with a diagnostic if <code>a != b</code> (polymorphic over scalar primitives)" },
      { sig: "assertNe(a, b): void", desc: "abort with a diagnostic if <code>a == b</code>" },
      { sig: "check(cond: bool): void", desc: "abort with a diagnostic if <code>cond</code> is false" },
      { sig: "fail(msg: string): void", desc: "unconditionally abort the test with <code>msg</code>" },
      { sig: "skip(msg: string): void", desc: "mark the test as skipped and exit cleanly" },
    ],
  },
  {
    id: "mocks",
    title: "Mocks",
    intro:
      "Mocks intercept calls to side-effecting builtins so tests can run hermetically. Each setter is test-only and per-test (state resets between tests automatically). Strict-mode setters (<code>mockExec</code>, <code>mockFetch</code>, <code>mockReadFile</code>) fail the test on an unmatched real call once any rule has been registered. Native supports the full set; the sh backend supports the four name/value-style mocks (env / now / args / readStdin).",
    items: [
      { sig: "mockExec(pat: string, resp: Process): void", desc: "regex over the cmd; matched call returns <code>resp</code>; <em>strict</em>" },
      { sig: "mockExecCalls(): string[]", desc: "every cmd the SUT passed to <code>exec</code>/<code>execTimeout</code> during this test" },
      { sig: "mockFetch(pat: string, resp: Response): void", desc: "regex over the URL; matched call returns <code>resp</code>; <em>strict</em>" },
      { sig: "mockFetchCalls(): string[]", desc: "every URL the SUT passed to <code>fetch</code> during this test" },
      { sig: "mockReadFile(pat: string, content: string): void", desc: "regex over the path; matched call returns <code>content</code>; <em>strict</em>" },
      { sig: "mockReadFileCalls(): string[]", desc: "every path the SUT passed to <code>readFile</code> during this test" },
      { sig: "mockEnv(name: string, value: string?): void", desc: "override a single env name; <code>null</code> simulates &quot;unset&quot;; other names fall through" },
      { sig: "mockNow(secs: number): void", desc: "freeze the clock so <code>now()</code> returns <code>secs</code>" },
      { sig: "mockArgs(xs: string[]): void", desc: "replace the result of <code>args()</code> for this test" },
      { sig: "mockReadStdin(s: string): void", desc: "replace the result of <code>readStdin()</code> for this test" },
    ],
  },
  {
    id: "evals",
    title: "Evals",
    intro:
      "<code>eval \"...\" { ... }</code> is the LLM-accuracy sibling of <code>test</code>. Bodies record numeric metrics with <code>score(label, value)</code> and gate the eval on the mean with <code>expect(label, threshold)</code>. The runner prints a per-eval scorecard — gated metrics first with ✓/✗, ungated metrics with ·, plus sample count and duration — and exits non-zero on any failed gate. Eval bodies inherit the test-builtin context so <code>check</code>, <code>fail</code>, and every mock setter (notably <code>mockLlm</code>) work the same way they do in <code>test</code> blocks. Native target only — invoke with <code>tartalo eval &lt;file-or-dir&gt;</code>; sh builds skip eval declarations silently.",
    items: [
      { sig: "score(label: string, value: float): void", desc: "append <code>value</code> to a labeled bucket; the runner reports the mean across all calls with the same label. Accepts a <code>number</code> for <code>value</code> and widens." },
      { sig: "expect(label: string, threshold: float): void", desc: "at end-of-eval, assert <code>mean(label) ≥ threshold</code>. Fails the eval (and the binary's exit code) if the mean is lower or no samples were recorded." },
    ],
  },
  {
    id: "scoring",
    title: "Scoring metrics",
    intro:
      "Callable anywhere; especially useful inside <code>eval</code> bodies. The float-returning ones land in <code>[0.0, 1.0]</code> so they compose with <code>score(...)</code> directly. Pick the metric that matches the task: classification or short-form QA → <code>exactMatch</code> / <code>f1Tokens</code>; fuzzy text → <code>jaccard</code> / <code>levenshteinRatio</code>; generation / translation → <code>bleu</code>; summarisation → <code>rougeL</code>; embeddings → <code>cosineSimilarity</code>.",
    items: [
      { sig: "jaccard(a: string, b: string): float", desc: "word-set Jaccard similarity. Splits both strings on whitespace; comparison is byte-for-byte. Lowercase the inputs (<code>jaccard(lower(a), lower(b))</code>) for case-folded matching." },
      { sig: "exactMatch(a: string, b: string): float", desc: "<code>1.0</code> if <code>a == b</code>, else <code>0.0</code>." },
      { sig: "containsScore(text: string, terms: string[]): float", desc: "fraction of <code>terms</code> that occur as substrings in <code>text</code>. Empty <code>terms</code> returns <code>1.0</code>." },
      { sig: "f1Tokens(predicted: string, expected: string): float", desc: "single-pair token-level F1 (the SQuAD metric). Tokenises both strings on whitespace; F1 over the resulting word sets." },
      { sig: "f1Score(predicted: string[], expected: string[]): float", desc: "element-wise token F1 averaged across the two arrays. Mismatched lengths scale by the longer side. Use when you've collected many <code>(pred, ref)</code> pairs and want one number out." },
      { sig: "levenshtein(a: string, b: string): number", desc: "raw edit distance, counted in unicode codepoints (not bytes). Returns <code>0</code> for equal strings, <code>len(a)</code> against the empty string." },
      { sig: "levenshteinRatio(a: string, b: string): float", desc: "Levenshtein normalised to <code>[0.0, 1.0]</code> via <code>1 - dist / max(len)</code>. Equal strings score <code>1.0</code>." },
      { sig: "bleu(hypothesis: string, reference: string): float", desc: "sentence-level BLEU-4 with the standard brevity penalty and add-1 smoothing on each n-gram precision. Useful for translation / open-ended generation." },
      { sig: "rougeL(hypothesis: string, reference: string): float", desc: "F1 derived from the longest common subsequence between the two token streams. Standard for summarisation; insensitive to word order beyond the LCS." },
      { sig: "cosineSimilarity(a: float[], b: float[]): float", desc: "cosine of the angle between two embedding vectors. Returns <code>0.0</code> against an all-zero vector rather than <code>NaN</code>. Lengths needn't match — extras on the longer vector contribute to its norm only." },
    ],
  },
];

// ---- code samples ----
const codeModuleA = `// lib/math.tt
export type Pair = { a: number, b: number }

export func sumPair(p: Pair): number {
  return p.a + p.b
}

// (no \`export\`) — private to this module
func helper(): string { return "shh" }`;

const codeModuleB = `// main.tt
import { Pair, sumPair } from "./lib/math.tt"

func main(): void {
  let p: Pair = Pair{a: 7, b: 35}
  echo(str(sumPair(p)))
}`;

const codeDecls = `let name: string = "world"
const PI: number = 3        // const → readonly in sh
let active: bool = true

// Type annotations on \`let\`/\`const\` are optional;
// inferred from the initializer.
let inferred = "hello"      // string
let n = 42                  // number
let big = n > 10            // bool`;

const codeEmptyArr = `let xs: string[] = []`;

const codeGenerics = `func id<T>(x: T): T {
  return x
}

func first<T>(xs: T[]): T {
  return xs[0]
}

func or<T>(x: T?, fallback: T): T {
  return x ?? fallback
}

func main(): void {
  echo(id("hello"))
  echo(str(id(42)))

  let nums: number[] = [10, 20, 30]
  echo(str(first(nums)))
}`;

const codeOptionals = `let x: string? = "hi"        // non-null
let y: string? = null        // null
let z: string  = x ?? "fallback"   // unwrap with default
let w: string  = x!                // forced unwrap (aborts if null)`;

const codeOptFields = `type User = {
  name: string,
  nickname: string?,
}

let u = User{name: "alice", nickname: null}
echo(u.nickname ?? u.name)`;

const codeRecords = `type Person = {
  name: string,
  age: number,
}

func main(): void {
  let p: Person = { name: "Alice", age: 30 }
  echo(p.name + " is " + str(p.age))
  p.age = p.age + 1
  echo(str(p.age))
}`;

const codeNestedRecords = `type Addr   = { city: string, zip: number }
type Person = { name: string, addr: Addr, tags: string[] }

func main(): void {
  let p: Person = Person{
    name: "Alice",
    addr: Addr{city: "Madrid", zip: 28001},
    tags: ["admin", "ops"],
  }
  echo(p.addr.city + " #" + str(len(p.tags)))
}`;

const codeFuncs = `func greet(name: string): string {
  return "Hello, " + name
}

func main(): void {
  echo(greet("world"))
}`;

const codeControl = `if count > 10 {
  echo("big")
} else if count > 0 {
  echo("small")
} else {
  echo("zero or less")
}

for i in 0..10 {
  echo(str(i))
}

for line in \`ls -1\` {
  echo(line)
}

for x in [10, 20, 30] {
  echo(str(x))
}`;

const codeMatch = `match action {
  "build" | "compile" => echo("compiling")
  "run"               => echo("running")
  ""                  => echo("usage: ACTION=...")
  _                   => echo("unknown: " + action)
}`;

const codeSum = `type Shape =
  Circle{r: number}
  | Rectangle{w: number, h: number}
  | Empty

func area(s: Shape): number {
  match s {
    Circle{r}        => return r * r * 3
    Rectangle{w, h}  => return w * h
    Empty            => return 0
  }
  return -1
}`;

const codeDefer = `func work(): void {
  defer { echo("a") }
  defer { echo("b") }
  echo("body")     // body, then b, then a
}`;

const codeParallel = `func main(): void {
  parallel {
    task { echo("a") }
    task { echo("b") }
    task { echo("c") }
  }
  echo("done")     // runs only after every task completes
}`;

const codeResult = `type IntResult = Ok{value: number} | Err{error: string}

func parseInt(s: string): IntResult {
  if s == "bad" { return Err{error: "bad input"} }
  return Ok{value: 1}
}

func double(s: string): IntResult {
  let n: number = parseInt(s)?  // on Err, propagates
  return Ok{value: n + n}
}`;

const codePipeline = `let n: number = 5 |> double()           // double(5)
echo(str(7 |> add(3)))                  // add(7, 3)
echo("HELLO" |> lower)                  // lower("HELLO")
echo(str(3 |> double() |> plus(1)))     // plus(double(3), 1)`;

const codeArrayOfRecords = `type Person = { name: string, age: number }

func main(): void {
  let people: Person[] = [
    Person{name: "Alice", age: 30},
    Person{name: "Bob",   age: 25},
  ]
  echo(str(len(people)))
  for p in people { echo(p.name + "/" + str(p.age)) }
}`;

const codeRecordSpread = `type Person = { name: string, age: number }

let alice: Person = Person{name: "Alice", age: 30}
let older: Person = Person{...alice, age: 31}`;

const codeRecordCast = `type RawUser   = { name: string, age: number, email: string }
type ShortUser = { name: string, age: number }

let raw:   RawUser   = RawUser{name: "Alice", age: 30, email: "a@x"}
let short: ShortUser = raw as ShortUser`;

const codeFuncValue = `func square(n: number): number { return n * n }

let f: func(number): number = square
echo(str(f(7)))`;

const codeFuncLit = `let dbl: func(number): number = func(x: number): number { return x + x }

let xs: number[] = [1, 2, 3, 4]
let squares: number[] = map(xs, func(x: number): number { return x * x })`;

const codeFuncCapture = `func main(): void {
  let n: number = 10
  let xs: number[] = [1, 2, 3]
  let added: number[] = map(xs, func(x: number): number { return x + n })
}`;

const codeInterp = `let who: string = "world"
echo("Hello, \${who}!")`;

const codeCmd1 = `let files: string = \`ls -1\``;
const codeCmd2 = `\`mkdir -p build\``;

const codePredeclared = `type Response = {
  status: number,    // HTTP status code; 0 on network failure
  ok: bool,          // true iff 200 ≤ status < 300
  body: string,      // response body
  headers: string,   // raw response headers, one per line
}

type Process = {
  code: number,      // exit code
  ok: bool,          // true iff code == 0
  stdout: string,    // captured stdout
  stderr: string,    // captured stderr
}

type FileInfo = {
  exists: bool,      // false if the path doesn't exist
  isFile: bool,
  isDir: bool,
  size: number,      // bytes; 0 if missing
  mtime: number,     // Unix seconds; 0 if missing
  mode: string,      // octal permission bits, e.g. "644"; "" if missing
}

type PathParts = {
  dir: string,       // dirname(path)
  base: string,      // basename(path) — final component, with extension
  name: string,      // basename minus the last \`.ext\` (same rule as extname)
  ext: string,       // extension including leading dot, or ""
}`;

const codeModel = `                                          ┌─→  sh emitter   →  source.sh
source.tt  →  lexer  →  parser  →  checker┤
                                          └─→  Go emitter   →  go build  →  binary`;

const codeNativeUsage = `tartalo build foo.tt --target=native -o foo
tartalo build foo.tt --target=native --goos=linux --goarch=arm64 -o foo
tartalo run   --target=native foo.tt -- args...
tartalo test  --target=native foo.tt`;

// ---- TOC active section tracking ----
const active = ref(toc[0]!.id);
const secRefs = ref<HTMLElement[] | null>(null);
let observer: IntersectionObserver | null = null;

const goTo = (id: string) => {
  const el = document.getElementById(id);
  if (el) {
    const top = el.getBoundingClientRect().top + window.scrollY - 80;
    window.scrollTo({ top, behavior: "smooth" });
    history.replaceState(null, "", `#${id}`);
  }
};

onMounted(() => {
  const sections = document.querySelectorAll(".ref-content section[id]");
  observer = new IntersectionObserver(
    (entries) => {
      const visible = entries
        .filter((e) => e.isIntersecting)
        .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
      if (visible[0]) {
        const id = (visible[0].target as HTMLElement).id;
        if (id) active.value = id;
      }
    },
    { rootMargin: "-20% 0px -60% 0px", threshold: [0, 0.2, 0.6] }
  );
  sections.forEach((s) => observer?.observe(s));

  // honour incoming hash
  const hash = window.location.hash.replace("#", "");
  if (hash) {
    setTimeout(() => goTo(hash), 50);
  }
});

onUnmounted(() => {
  observer?.disconnect();
  observer = null;
});
</script>

<style scoped>
.reference {
  padding-top: 6rem;
}

.ref-hero {
  padding: 3rem 0 4rem;
  border-bottom: 1px solid var(--border);
  background:
    radial-gradient(at 10% 0%, rgba(255, 122, 61, 0.07), transparent 50%);
}

.ref-hero h1 {
  font-size: clamp(2.4rem, 4.8vw, 3.6rem);
  letter-spacing: -0.025em;
  margin: 0.4rem 0 1.2rem;
  font-weight: 700;
}

.ref-hero .lead {
  color: var(--text-muted);
  font-size: 1.05rem;
  max-width: 700px;
  line-height: 1.65;
}

.ref-hero .lead code {
  font-size: 0.88em;
}

.eyebrow {
  display: inline-block;
  color: var(--accent);
  font-size: 0.78rem;
}

.ref-meta {
  display: flex;
  gap: 0.7rem;
  align-items: center;
  font-size: 0.8rem;
  color: var(--text-muted);
  margin-top: 1.4rem;
  flex-wrap: wrap;
}

.ref-meta strong {
  color: var(--text);
  font-weight: 500;
}

.ref-layout {
  display: grid;
  grid-template-columns: 220px 1fr;
  gap: 4rem;
  padding-top: 4rem;
  padding-bottom: 6rem;
}

/* TOC */
.toc {
  position: sticky;
  top: 90px;
  align-self: start;
  max-height: calc(100vh - 100px);
  overflow-y: auto;
  padding-right: 0.5rem;
}

.toc-title {
  color: var(--text-muted);
  font-size: 0.75rem;
  margin: 0 0 0.8rem;
  letter-spacing: 0.04em;
}

.toc ul {
  list-style: none;
  margin: 0;
  padding: 0;
  border-left: 1px solid var(--border);
}

.toc li {
  position: relative;
}

.toc li.sub a {
  padding-left: 1.6rem;
  font-size: 0.78rem;
  color: var(--text-subtle);
}

.toc a {
  display: block;
  padding: 0.35rem 0.9rem;
  font-size: 0.85rem;
  color: var(--text-muted);
  text-decoration: none;
  transition: color 0.15s ease;
  border-left: 2px solid transparent;
  margin-left: -1px;
  cursor: pointer;
}

.toc a:hover {
  color: var(--text);
}

.toc a.active {
  color: var(--accent);
  border-left-color: var(--accent);
}

/* Content */
.ref-content {
  min-width: 0;
}

.ref-content section {
  padding-top: 1.5rem;
  scroll-margin-top: 80px;
  margin-bottom: 3rem;
}

.ref-content section + section {
  border-top: 1px solid var(--border);
  padding-top: 3rem;
}

.ref-content h2 {
  font-size: 1.85rem;
  margin: 0 0 1rem;
  letter-spacing: -0.02em;
  font-weight: 600;
}

.ref-content h3 {
  font-size: 1.15rem;
  margin: 2rem 0 0.7rem;
  letter-spacing: -0.01em;
  font-weight: 600;
  color: var(--text);
}

.ref-content p {
  font-size: 1rem;
  line-height: 1.7;
  color: var(--text-muted);
  max-width: 720px;
}

.ref-content code {
  font-size: 0.88em;
}

.bullets {
  margin: 0 0 1.2rem;
  padding-left: 1.4rem;
  color: var(--text-muted);
  line-height: 1.75;
  max-width: 720px;
}

.bullets li {
  margin-bottom: 0.4rem;
}

.bullets strong {
  color: var(--text);
  font-weight: 600;
}

.callout {
  background: rgba(255, 181, 71, 0.04);
  border: 1px solid rgba(255, 181, 71, 0.2);
  border-left: 3px solid var(--accent-secondary);
  padding: 1rem 1.2rem;
  border-radius: 6px;
  color: var(--text-muted);
  font-size: 0.95rem;
  line-height: 1.65;
  margin: 1.5rem 0;
}

.callout strong {
  color: var(--text);
}

/* Builtins */
.builtins {
  margin-bottom: 1.6rem;
}

.builtins h3 {
  scroll-margin-top: 80px;
}

.group-intro {
  font-size: 0.95rem;
  color: var(--text-muted);
  margin-bottom: 1rem;
  max-width: 720px;
}

.builtin-list {
  list-style: none;
  padding: 0;
  margin: 0;
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
}

.builtin-list li {
  display: flex;
  flex-wrap: wrap;
  gap: 0.6rem;
  padding: 0.7rem 1rem;
  font-size: 0.93rem;
  border-bottom: 1px solid var(--border);
  background: var(--bg);
  transition: background 0.15s ease;
}

.builtin-list li:last-child {
  border-bottom: none;
}

.builtin-list li:hover {
  background: var(--surface);
}

.builtin-list .sig {
  font-family: "JetBrains Mono", monospace;
  background: transparent;
  border: none;
  padding: 0;
  color: var(--code-fn);
  font-size: 0.88rem;
  white-space: pre-wrap;
}

.builtin-list .desc {
  color: var(--text-muted);
  font-size: 0.9rem;
}

.ref-end {
  margin-top: 4rem;
  padding-top: 2rem;
  border-top: 1px solid var(--border);
}

.ref-end p {
  margin-bottom: 1.4rem;
  max-width: 600px;
}

@media (max-width: 920px) {
  .ref-layout {
    grid-template-columns: 1fr;
    gap: 2rem;
  }
  .toc {
    position: static;
    max-height: none;
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 1rem 1rem 0.6rem;
  }
  .toc ul {
    border-left: none;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  }
  .toc li.sub {
    display: none;
  }
  .toc a {
    border-left: none;
    padding: 0.35rem 0.5rem;
  }
  .toc a.active {
    border-left: none;
    background: var(--surface);
    border-radius: 4px;
  }
}
</style>
