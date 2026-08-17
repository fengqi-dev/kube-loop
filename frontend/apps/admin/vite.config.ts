import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const rootDir = path.dirname(fileURLToPath(import.meta.url));
const developmentBackend =
  process.env.VITE_ADMIN_BACKEND || "http://127.0.0.1:8081";

export default defineConfig({
  plugins: [
    {
      name: "kubeloop-admin-development-path",
      apply: "serve",
      transformIndexHtml(html) {
        return html.replaceAll("{{MANAGEMENT_PATH}}", "/api/admin");
      },
    },
    react(),
    tailwindcss(),
  ],
  base: "./",
  resolve: { alias: { "@": path.resolve(rootDir, "./src") } },
  server: {
    proxy: {
      "/api": developmentBackend,
      "/oauth2": developmentBackend,
      "/.well-known": developmentBackend,
    },
  },
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
