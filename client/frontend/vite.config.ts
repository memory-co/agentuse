import { fileURLToPath, URL } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// 开发时把接口代理到本机只读页面(默认 :7801,AGENTUSE_API 可改)。
const target = process.env.AGENTUSE_API || "http://127.0.0.1:7801";

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: { proxy: Object.fromEntries(["/api", "/login", "/setup.sh", "/download"].map(p => [p, { target }])) },
});
