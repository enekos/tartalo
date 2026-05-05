import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

export default defineConfig({
  plugins: [vue()],
  base: "/tartalo/",
  server: {
    host: true,
    port: 5176,
  },
  build: {
    target: "esnext",
    minify: "esbuild",
  },
});
