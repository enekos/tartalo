<template>
  <footer class="footer">
    <div class="container">
      <div class="footer-top">
        <div class="brand">
          <div class="brand-row">
            <span class="logo-mark">tt</span>
            <span class="logo-text">tartalo</span>
          </div>
          <p class="description">
            A small, statically-typed scripting language that compiles to POSIX sh.
            Pre-alpha, vibes-driven, type-safe.
          </p>
          <p class="status mono">// status: pre-alpha</p>
        </div>

        <div class="footer-links">
          <div class="link-column">
            <h4>Language</h4>
            <router-link to="/reference#types">Types</router-link>
            <router-link to="/reference#declarations">Declarations</router-link>
            <router-link to="/reference#functions">Functions</router-link>
            <router-link to="/reference#control-flow">Control flow</router-link>
            <router-link to="/reference#builtins">Builtins</router-link>
          </div>
          <div class="link-column">
            <h4>Project</h4>
            <a href="https://github.com/" target="_blank" rel="noopener">GitHub</a>
            <router-link to="/reference">Spec</router-link>
            <router-link to="/#install">Install</router-link>
            <router-link to="/#examples">Examples</router-link>
          </div>
          <div class="link-column">
            <h4>Compiler</h4>
            <span class="muted">lexer</span>
            <span class="muted">parser</span>
            <span class="muted">type checker</span>
            <span class="muted">sh emitter</span>
          </div>
        </div>
      </div>

      <div class="footer-bottom">
        <p class="mono">&copy; {{ year }} tartalo · MIT</p>
        <p class="mono tagline">written in Go · compiles to /bin/sh · set -eu</p>
      </div>
    </div>
  </footer>
</template>

<script setup lang="ts">
const year = new Date().getFullYear();
</script>

<style scoped>
.footer {
  border-top: 1px solid var(--border);
  background: var(--bg-secondary);
  padding: 5rem 2rem 3rem;
  position: relative;
  z-index: 5;
}

.container {
  max-width: 1180px;
  margin: 0 auto;
}

.footer-top {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 4rem;
  margin-bottom: 4rem;
}

.brand {
  max-width: 320px;
}

.brand-row {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  margin-bottom: 1rem;
}

.logo-mark {
  width: 28px;
  height: 28px;
  border-radius: 6px;
  background: var(--text);
  color: var(--bg);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-family: "JetBrains Mono", monospace;
  font-weight: 700;
  font-size: 0.85rem;
}

.logo-text {
  font-family: "JetBrains Mono", monospace;
  font-weight: 700;
  font-size: 1.1rem;
  color: var(--text);
}

.description {
  color: var(--text-muted);
  line-height: 1.6;
  font-size: 0.95rem;
  margin-bottom: 0.6rem;
}

.status {
  color: var(--accent);
  font-size: 0.8rem;
  margin: 0;
}

.footer-links {
  display: flex;
  gap: 4rem;
  flex-wrap: wrap;
}

.link-column {
  display: flex;
  flex-direction: column;
  gap: 0.7rem;
}

.link-column h4 {
  color: var(--text);
  font-family: "JetBrains Mono", monospace;
  font-weight: 600;
  font-size: 0.8rem;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  margin-bottom: 0.5rem;
}

.link-column a,
.link-column .muted {
  color: var(--text-muted);
  font-size: 0.9rem;
  text-decoration: none;
  transition: color 0.2s ease;
}

.link-column a:hover {
  color: var(--accent);
}

.link-column .muted {
  color: var(--text-subtle);
  font-family: "JetBrains Mono", monospace;
  font-size: 0.85rem;
}

.footer-bottom {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-top: 2rem;
  border-top: 1px solid var(--border);
  color: var(--text-subtle);
  font-size: 0.8rem;
  flex-wrap: wrap;
  gap: 1rem;
}

.tagline {
  color: var(--text-subtle);
}

@media (max-width: 768px) {
  .footer-top {
    flex-direction: column;
    gap: 3rem;
  }
  .footer-links {
    gap: 2.5rem;
  }
}
</style>
