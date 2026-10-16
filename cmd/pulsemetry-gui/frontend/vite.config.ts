import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

export default defineConfig({
  plugins: [tailwindcss(), react(), wails("./bindings")],
  resolve: {
    alias: { $lib: fileURLToPath(new URL("./src/lib", import.meta.url)) },
  },
  // Wails 자산 프록시가 tcp4(127.0.0.1)로만 다이얼한다. Node 17+ 에서 localhost 가
  // ::1 로 먼저 바인딩되면 프록시가 502 를 내므로 IPv4 로 고정한다.
  server: { host: "127.0.0.1" },
});
