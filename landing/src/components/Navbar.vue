<template>
  <nav class="navbar" :class="{ scrolled: isScrolled }">
    <div class="nav-content">
      <router-link to="/" class="logo">
        <span class="logo-mark">tt</span>
        <span class="logo-text">tartalo</span>
        <span class="logo-version">v0</span>
      </router-link>

      <div class="nav-links">
        <router-link to="/reference">Reference</router-link>
        <router-link to="/#features">Features</router-link>
        <router-link to="/#install">Install</router-link>
        <a href="https://github.com/enekos/tartalo" target="_blank" rel="noopener">GitHub</a>
      </div>

      <div class="nav-actions">
        <router-link to="/reference" class="btn-secondary nav-cta">
          Read the spec
          <span class="arrow">→</span>
        </router-link>
      </div>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from "vue";

const isScrolled = ref(false);

const handleScroll = () => {
  isScrolled.value = window.scrollY > 12;
};

onMounted(() => {
  window.addEventListener("scroll", handleScroll, { passive: true });
  handleScroll();
});

onUnmounted(() => {
  window.removeEventListener("scroll", handleScroll);
});
</script>

<style scoped>
.navbar {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 100;
  padding: 1.1rem 2rem;
  background: transparent;
  transition: all 0.3s ease;
  border-bottom: 1px solid transparent;
}

.navbar.scrolled {
  background: rgba(10, 10, 10, 0.78);
  backdrop-filter: blur(14px);
  -webkit-backdrop-filter: blur(14px);
  border-bottom: 1px solid var(--border);
  padding: 0.8rem 2rem;
}

.nav-content {
  max-width: 1180px;
  margin: 0 auto;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 2rem;
}

.logo {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  text-decoration: none;
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
  letter-spacing: -0.04em;
}

.logo-text {
  font-family: "JetBrains Mono", monospace;
  font-weight: 700;
  font-size: 1.05rem;
  color: var(--text);
  letter-spacing: -0.02em;
}

.logo-version {
  font-family: "JetBrains Mono", monospace;
  font-size: 0.7rem;
  color: var(--text-subtle);
  padding: 0.15rem 0.4rem;
  border: 1px solid var(--border);
  border-radius: 4px;
}

.nav-links {
  display: flex;
  gap: 2rem;
  align-items: center;
}

.nav-links a {
  color: var(--text-muted);
  font-size: 0.9rem;
  font-weight: 500;
  transition: color 0.2s ease;
}

.nav-links a:hover,
.nav-links a.router-link-active {
  color: var(--text);
}

.nav-actions {
  display: flex;
  gap: 1rem;
  align-items: center;
}

.nav-cta {
  padding: 0.55rem 1rem;
  font-size: 0.85rem;
}

.arrow {
  transition: transform 0.2s ease;
}

.nav-cta:hover .arrow {
  transform: translateX(2px);
}

@media (max-width: 768px) {
  .nav-links {
    display: none;
  }
  .navbar {
    padding: 0.9rem 1.2rem;
  }
}
</style>
