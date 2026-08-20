import { defineConfig } from "vite";
import { fileURLToPath, URL } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const CONTROL = process.env.CONTROL_URL || "http://127.0.0.1:8088";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: CONTROL, changeOrigin: true },
    },
  },
  preview: {
    host: "0.0.0.0",
    port: 5174,
    proxy: {
      "/api": { target: CONTROL, changeOrigin: true },
    },
  },
});
