import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

export default defineConfig({
  plugins: [tailwindcss(), svelte(), wails("./bindings")],
  resolve: {
    alias: { "$lib": new URL("./src/lib", import.meta.url).pathname },
  },
  // Wails 자산 프록시가 tcp4(127.0.0.1)로만 다이얼한다. Node 17+ 에서 localhost 가
  // ::1 로 먼저 바인딩되면 프록시가 502 를 내므로 IPv4 로 고정한다.
  server: { host: "127.0.0.1" },
});
