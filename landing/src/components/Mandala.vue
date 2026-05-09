<template>
  <section
    class="mandala"
    @mousemove="onMandalaMouseMove"
    @mouseleave="onMandalaMouseLeave"
  >
    <div class="mandala-visual" ref="visualRef">
      <div class="swarm-attract-wrapper" :style="swarmStyle">
        <div class="swarm-container" :class="`${phase}-phase`">
          <div
            v-for="p in particles"
            :key="p.id"
            class="particle-wrapper"
            :style="{
              transform: `translate3d(${p.x}px, ${p.y}px, ${p.z}px) rotateX(${p.rotX}deg) rotateY(${p.rotY}deg) rotateZ(${p.rotZ}deg)`,
              opacity: p.opacity,
              transitionDelay: `${p.delay}ms`,
              transitionDuration: phase === 'chaos' ? '11s' : '6.5s',
            }"
          >
            <div
              class="particle-inner mono"
              :style="{
                color: p.highlight ? 'var(--accent)' : 'var(--text-muted)',
                textShadow: p.highlight ? '0 0 12px var(--accent-glow)' : 'none',
                filter: p.blur ? 'blur(1.5px)' : 'blur(0px)',
                transform: `translate(-50%, -50%) scale(${p.highlight ? 1.18 : 1})`,
              }"
            >
              {{ phase === 'pipeline' ? '+' : p.char }}
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="mandala-content">
      <span class="eyebrow mono" ref="eyebrowRef">// what tartalo does</span>
      <h2 ref="titleRef">
        From chaos.<br />
        To <span class="gradient-text">/bin/sh</span>.
      </h2>
      <p class="subtitle" ref="subtitleRef">
        A scatter of shell tokens — <code>$</code>, <code>|</code>,
        <code>&gt;</code>, <code>{}</code> — resolves into a parseable grammar,
        then a checked module graph, then a single, boring, portable
        <code>.sh</code>. The compiler does the hard part. The output is
        readable on purpose.
      </p>
      <div class="phase-indicator mono" ref="indicatorRef">
        <span
          v-for="ph in phases"
          :key="ph.id"
          class="phase-pill"
          :class="{ active: phase === ph.id }"
        >
          <span class="phase-dot"></span>
          {{ ph.label }}
        </span>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from "vue";
import { animate, inView, stagger } from "motion";

const visualRef = ref<HTMLElement | null>(null);
const eyebrowRef = ref<HTMLElement | null>(null);
const titleRef = ref<HTMLElement | null>(null);
const subtitleRef = ref<HTMLElement | null>(null);
const indicatorRef = ref<HTMLElement | null>(null);

type Phase = "chaos" | "orbit" | "pipeline";

const phases: { id: Phase; label: string }[] = [
  { id: "chaos", label: "tokens" },
  { id: "orbit", label: "modules" },
  { id: "pipeline", label: "sh" },
];

const phase = ref<Phase>("chaos");

interface Particle {
  id: number;
  x: number;
  y: number;
  z: number;
  rotX: number;
  rotY: number;
  rotZ: number;
  char: string;
  opacity: number;
  delay: number;
  highlight: boolean;
  blur: boolean;

  chX: number;
  chY: number;
  chZ: number;
  chRot: number;

  orX: number;
  orY: number;
  orZ: number;
  orRot: number;

  pgX: number;
  pgY: number;
  pgZ: number;
}

const particles = ref<Particle[]>([]);
const NUM_PARTICLES = 192;
const TOKENS = "${}[]()|&><;=+-*/?!@#~^.,:'\"";

const initParticles = () => {
  const p: Particle[] = [];

  for (let i = 0; i < NUM_PARTICLES; i++) {
    // CHAOS: random sphere
    const r = 200 + Math.random() * 160;
    const theta = Math.random() * Math.PI * 2;
    const phi = Math.acos(Math.random() * 2 - 1);
    const chX = r * Math.sin(phi) * Math.cos(theta);
    const chY = r * Math.sin(phi) * Math.sin(theta);
    const chZ = r * Math.cos(phi);
    const chRot = Math.random() * 360;

    // ORBIT: 3 concentric rings (NUM/3 each), subtle 3D wave
    const ringCount = 3;
    const perRing = Math.ceil(NUM_PARTICLES / ringCount);
    const ring = Math.min(Math.floor(i / perRing), ringCount - 1);
    const ringRadius = 70 + ring * 60;
    const orAngle = ((i % perRing) / perRing) * Math.PI * 2;
    const orX = Math.cos(orAngle) * ringRadius;
    const orY = Math.sin(orAngle) * ringRadius;
    const orZ = (ring % 2 === 0 ? 1 : -1) * 22 * Math.sin(orAngle * 3);
    const orRot = (orAngle * 180) / Math.PI + 90;

    // PIPELINE: tight 8x8x3 grid (192) — represents compiled, structured output
    const gx = 8;
    const gy = 8;
    const spacing = 42;
    const cellX = i % gx;
    const cellY = Math.floor(i / gx) % gy;
    const cellZ = Math.floor(i / (gx * gy));
    const pgX = (cellX - (gx - 1) / 2) * spacing;
    const pgY = (cellY - (gy - 1) / 2) * spacing;
    const pgZ = (cellZ - 1) * spacing;

    p.push({
      id: i,
      x: chX,
      y: chY,
      z: chZ,
      rotX: Math.random() * 360,
      rotY: Math.random() * 360,
      rotZ: chRot,
      char: TOKENS.charAt(Math.floor(Math.random() * TOKENS.length)),
      opacity: Math.random() * 0.4 + 0.1,
      delay: Math.random() * 2500,
      highlight: Math.random() > 0.82,
      blur: Math.random() > 0.6,

      chX,
      chY,
      chZ,
      chRot,
      orX,
      orY,
      orZ,
      orRot,
      pgX,
      pgY,
      pgZ,
    });
  }
  particles.value = p;
};

const applyPhase = (next: Phase) => {
  phase.value = next;
  particles.value.forEach((p) => {
    p.delay = Math.random() * 2000;

    if (next === "chaos") {
      p.chX += (Math.random() - 0.5) * 80;
      p.chY += (Math.random() - 0.5) * 80;
      p.chZ += (Math.random() - 0.5) * 80;
      p.x = p.chX;
      p.y = p.chY;
      p.z = p.chZ;
      p.rotX = Math.random() * 360;
      p.rotY = Math.random() * 360;
      p.rotZ = p.chRot;
      p.opacity = Math.random() * 0.35 + 0.1;
      p.blur = Math.random() > 0.5;
    } else if (next === "orbit") {
      p.x = p.orX;
      p.y = p.orY;
      p.z = p.orZ;
      p.rotX = 0;
      p.rotY = 0;
      p.rotZ = p.orRot;
      p.opacity = p.highlight ? 1 : 0.7;
      p.blur = false;
    } else if (next === "pipeline") {
      p.x = p.pgX;
      p.y = p.pgY;
      p.z = p.pgZ;
      p.rotX = 0;
      p.rotY = 0;
      p.rotZ = 0;
      p.opacity = p.highlight ? 1 : 0.55;
      p.blur = false;
    }
  });
};

let cycleActive = true;

const runCycle = async () => {
  while (cycleActive) {
    applyPhase("chaos");
    await new Promise((r) => setTimeout(r, 9000));
    if (!cycleActive) break;

    applyPhase("orbit");
    await new Promise((r) => setTimeout(r, 8000));
    if (!cycleActive) break;

    applyPhase("pipeline");
    await new Promise((r) => setTimeout(r, 8000));
  }
};

let scrambleInterval: ReturnType<typeof setInterval>;

// Mouse attraction (parallax follow) + breathing scale, both via rAF
const mouseTargetX = ref(0);
const mouseTargetY = ref(0);
const attrOffsetX = ref(0);
const attrOffsetY = ref(0);
const breatheScale = ref(1);
let attrRafId: number;
let breathePhase = 0;

const onMandalaMouseMove = (e: MouseEvent) => {
  const el = e.currentTarget as HTMLElement;
  const rect = el.getBoundingClientRect();
  mouseTargetX.value = (e.clientX - rect.left - rect.width / 2) * 0.08;
  mouseTargetY.value = (e.clientY - rect.top - rect.height / 2) * 0.08;
};

const onMandalaMouseLeave = () => {
  mouseTargetX.value = 0;
  mouseTargetY.value = 0;
};

const tick = () => {
  attrOffsetX.value += (mouseTargetX.value - attrOffsetX.value) * 0.04;
  attrOffsetY.value += (mouseTargetY.value - attrOffsetY.value) * 0.04;
  breathePhase += 0.012;
  breatheScale.value = 1 + 0.045 * Math.sin(breathePhase);
  attrRafId = requestAnimationFrame(tick);
};

const swarmStyle = computed(() => ({
  transform: `translate(${attrOffsetX.value}px, ${attrOffsetY.value}px) scale(${breatheScale.value})`,
  transformOrigin: "center center",
}));

onMounted(() => {
  initParticles();

  // Slow character churn — only meaningful in chaos & orbit (pipeline shows '+')
  scrambleInterval = setInterval(() => {
    if (phase.value === "pipeline") return;
    particles.value.forEach((p) => {
      if (Math.random() < 0.025) {
        p.char = TOKENS.charAt(Math.floor(Math.random() * TOKENS.length));
      }
    });
  }, 180);

  setTimeout(runCycle, 1200);
  tick();

  // Reveal: stagger blur+opacity+translate on text, fade visual
  const textEls = [
    eyebrowRef.value,
    titleRef.value,
    subtitleRef.value,
    indicatorRef.value,
  ].filter(Boolean) as HTMLElement[];

  if (titleRef.value) {
    inView(
      titleRef.value,
      () => {
        if (textEls.length > 0) {
          // @ts-ignore
          animate(
            textEls,
            {
              filter: ["blur(12px)", "blur(0px)"],
              opacity: [0, 1],
              transform: ["translateY(20px)", "translateY(0px)"],
            },
            { delay: stagger(0.18), duration: 1.4, ease: "easeOut" }
          );
        }
        if (visualRef.value) {
          // @ts-ignore
          animate(
            visualRef.value,
            { opacity: [0, 1] },
            { delay: 0.6, duration: 2.2 }
          );
        }
      },
      { amount: 0.3 }
    );
  }
});

onUnmounted(() => {
  cycleActive = false;
  clearInterval(scrambleInterval);
  cancelAnimationFrame(attrRafId);
});
</script>

<style scoped>
.mandala {
  position: relative;
  min-height: 95vh;
  display: flex;
  flex-direction: row;
  align-items: center;
  justify-content: space-evenly;
  padding: 6rem 2rem;
  overflow: hidden;
  border-top: 1px solid var(--border);
  gap: 4rem;
}

.mandala::before {
  content: "";
  position: absolute;
  inset: 0;
  background:
    radial-gradient(circle at 30% 50%, rgba(255, 122, 61, 0.05), transparent 55%),
    radial-gradient(circle at 70% 50%, rgba(255, 181, 71, 0.03), transparent 55%);
  pointer-events: none;
  z-index: 0;
}

.mandala-content {
  text-align: left;
  max-width: 460px;
  z-index: 10;
  position: relative;
}

.eyebrow {
  display: inline-block;
  color: var(--accent);
  font-size: 0.8rem;
  margin-bottom: 0.9rem;
  font-weight: 500;
  letter-spacing: 0.02em;
  opacity: 0;
}

h2 {
  font-size: clamp(2.4rem, 4.4vw, 3.6rem);
  font-weight: 700;
  line-height: 1.05;
  letter-spacing: -0.025em;
  margin-bottom: 1.4rem;
  opacity: 0;
  color: var(--text);
}

.subtitle {
  font-size: 1.05rem;
  color: var(--text-muted);
  line-height: 1.65;
  margin: 0 0 2rem;
  opacity: 0;
}

.subtitle code {
  font-size: 0.88em;
}

.phase-indicator {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  opacity: 0;
}

.phase-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0.4rem 0.75rem;
  border: 1px solid var(--border);
  border-radius: 100px;
  font-size: 0.75rem;
  color: var(--text-subtle);
  background: rgba(255, 255, 255, 0.015);
  transition: all 0.4s ease;
}

.phase-pill.active {
  color: var(--accent);
  border-color: var(--accent);
  background: rgba(255, 122, 61, 0.08);
}

.phase-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--text-subtle);
  transition: background 0.4s ease;
}

.phase-pill.active .phase-dot {
  background: var(--accent);
  box-shadow: 0 0 8px var(--accent);
}

/* SWARM */
.swarm-attract-wrapper {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  will-change: transform;
}

.mandala-visual {
  position: relative;
  width: 100%;
  max-width: 580px;
  height: 580px;
  opacity: 0;
  z-index: 5;
  perspective: 1200px;
}

.swarm-container {
  position: absolute;
  top: 50%;
  left: 50%;
  width: 100%;
  height: 100%;
  transform-style: preserve-3d;
  animation: mandala-spin 60s linear infinite;
}

@keyframes mandala-spin {
  0% {
    transform: translate(-50%, -50%) rotateY(0deg) rotateZ(0deg);
  }
  50% {
    transform: translate(-50%, -50%) rotateY(180deg) rotateZ(180deg);
  }
  100% {
    transform: translate(-50%, -50%) rotateY(360deg) rotateZ(360deg);
  }
}

.particle-wrapper {
  position: absolute;
  top: 50%;
  left: 50%;
  will-change: transform, opacity;
  transform-style: preserve-3d;
}

.particle-inner {
  font-size: 1rem;
  line-height: 1;
  font-weight: 500;
  transition:
    color 2.2s ease,
    text-shadow 2.2s ease,
    filter 2.2s ease,
    transform 2.2s ease;
}

/* Easing per phase */
.chaos-phase .particle-wrapper {
  transition-timing-function: cubic-bezier(0.25, 0.1, 0.25, 1);
}

.orbit-phase .particle-wrapper,
.pipeline-phase .particle-wrapper {
  transition-timing-function: cubic-bezier(0.65, 0, 0.35, 1);
}

@media (max-width: 900px) {
  .mandala {
    flex-direction: column;
    text-align: center;
    padding: 5rem 1.5rem;
  }
  .mandala-content {
    text-align: center;
  }
  .mandala-visual {
    height: 420px;
    transform: scale(0.85);
  }
  .phase-indicator {
    justify-content: center;
  }
}

@media (max-width: 768px) {
  /* Limit particle count for perf */
  .particle-wrapper:nth-child(n + 110) {
    display: none !important;
  }
}
</style>
