import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import { ledgerDevelopment } from "./dev/ledger-proxy";

// Only the development server sees these variables; never bundle service tokens.
const ledger = ledgerDevelopment(process.env);
export default defineConfig({
  plugins: [vue(), ledger.plugin],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      ...ledger.proxy,
      "/api/platform/liquor/": {
        target: process.env.BACKEND_PROXY_URL || "http://127.0.0.1:5051",
        changeOrigin: false,
        ...(process.env.BACKEND_API_TOKEN
          ? {
              headers: {
                Authorization: `Bearer ${process.env.BACKEND_API_TOKEN}`,
              },
            }
          : {}),
      },
    },
  },
});
