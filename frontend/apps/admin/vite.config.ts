import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const rootDir = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "./",
  resolve: { alias: { "@": path.resolve(rootDir, "./src") } },
  build: {
    outDir: "../../../internal/controlplane/admin/ui/assets",
    emptyOutDir: true,
    target: "es2022",
    minify: "terser",
    rollupOptions: {
      output: {
        entryFileNames: "app.js",
        chunkFileNames: "[name].js",
        assetFileNames: ({ names }) => names.some((name) => name.endsWith(".css")) ? "app.css" : "[name][extname]",
      },
    },
  },
});
