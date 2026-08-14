import path from "path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../../dist",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("node_modules")) return;
          if (id.includes("/@xterm/")) return "terminal-vendor";
          if (id.includes("/recharts/") || id.includes("/d3-") || id.includes("/victory-vendor/")) return "charts-vendor";
          if (id.includes("/lucide-react/")) return "icons-vendor";
          if (id.includes("/radix-ui/") || id.includes("/@radix-ui/")) return "radix-vendor";
          return "ui-vendor";
        },
      },
    },
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});
