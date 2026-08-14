<script lang="ts">
  import BottomNav, {
    type Tab,
  } from "./pages/home/components/BottomNav.svelte";

  // 라우터 없이 로컬 탭 상태로 페이지 전환. 페이지 구현은 후속 티켓(PROJ-60~63)에서
  // 각 탭에 얹는다. 현재는 모든 탭이 플레이스홀더.
  let activeTab = $state<Tab>("overview");
  const go = (tab: Tab) => (activeTab = tab);

  const STUB_LABEL: Record<Tab, string> = {
    overview: "Overview",
    activity: "Activity",
    insights: "Insights",
    settings: "Settings",
  };
</script>

<!-- 앱 셸: 창 높이 고정(h-screen). 가운데 영역만 스크롤되고 하단 nav는 창 최하단에 고정. -->
<div class="flex h-screen min-w-[1024px] flex-col overflow-hidden bg-bg">
  <div class="flex flex-1 flex-col overflow-y-auto">
    <main class="flex flex-1 flex-col items-center justify-center gap-2">
      <div
        class="text-text font-bold"
        style="font-size:25px;letter-spacing:-0.02em"
      >
        {STUB_LABEL[activeTab]}
      </div>
      <div class="text-text-secondary" style="font-size:13.5px">
        아직 준비 중인 화면이야.
      </div>
    </main>
  </div>

  <BottomNav active={activeTab} onSelect={go} />
</div>
