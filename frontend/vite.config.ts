import { fileURLToPath, URL } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// 开发时把接口代理到本机的 agentuse-server(默认 :8080,AGENTUSE_API 可改)。
const target = process.env.AGENTUSE_API || "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: { proxy: Object.fromEntries(["/api", "/login", "/setup.sh", "/download"].map(p => [p, { target }])) },
});
