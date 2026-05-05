import { createRouter, createWebHistory } from "vue-router";
import Home from "./pages/Home.vue";
import Reference from "./pages/Reference.vue";

const routes = [
  { path: "/", component: Home },
  { path: "/reference", component: Reference },
  { path: "/:pathMatch(.*)*", component: Home },
];

const router = createRouter({
  history: createWebHistory("/tartalo/"),
  routes,
  scrollBehavior(to, _from, savedPosition) {
    if (savedPosition) return savedPosition;
    if (to.hash) {
      return { el: to.hash, behavior: "smooth", top: 80 };
    }
    return { top: 0 };
  },
});

export default router;
