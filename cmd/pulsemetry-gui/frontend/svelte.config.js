import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

// 빌드는 vite.config.ts 의 svelte() 플러그인이 담당하고, 이 파일은 에디터의
// svelte-language-server 가 읽는다. 없으면 언어 서버가 vite config 를 직접
// 해석하려다 실패해 "No Svelte configuration found in vite config" 경고를 낸다.
export default {
  preprocess: vitePreprocess(),
};
