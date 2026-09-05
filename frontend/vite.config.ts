import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

// Only the development server sees these variables; never bundle service tokens.
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/api/platform": {
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
