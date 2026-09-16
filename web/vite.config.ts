import { defineConfig } from "vite";

// The Go server renders the pages; Vite serves and builds the scripts and styles under /static/.
export default defineConfig({
  base: "/static/",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    manifest: true,
    rollupOptions: { input: "src/main.ts" },
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    origin: "http://127.0.0.1:5173",
    cors: true,
  },
});
