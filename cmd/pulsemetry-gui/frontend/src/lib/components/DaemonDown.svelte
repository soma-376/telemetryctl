<script lang="ts">
  import EmptyState from "./ui/EmptyState.svelte";
  import RefreshIcon from "$lib/icons/RefreshIcon.svelte";
  import { reconnect, retryNow, retryNote } from "$lib/domain/reconnect.svelte";

  // 데몬에 닿지 못할 때의 화면이다. **트레이와 메인이 같은 것을 쓴다** — 데몬은 하나이므로
  // 그 사실을 화면마다 다르게 말할 이유가 없고, 문구가 갈라지면 같은 상황을 다르게 설명하게
  // 된다.
  //
  // 상태(backoff·카운트다운)는 여기 없다. lib/domain/reconnect.svelte.ts 가 앱당 하나로
  // 들고 있어서, 두 화면이 동시에 떠 있어도 카운트다운은 하나만 돌고 재시도도 한 번만 나간다.
  //
  // 레이아웃을 새로 짜지 않고 EmptyState 를 조립한다. 그쪽이 이미 "본문을 통째로 대체하는
  // 자리" 의 규격(마스코트·제목·설명·액션)을 들고 있다.
</script>

<EmptyState
  pose="confused"
  title="데몬에 연결할 수 없어요"
  description="Pulsemetry 데몬이 응답하지 않아 사용량을 확인할 수 없어요. 수집도 멈춘 상태예요."
>
  {#snippet action()}
    <div
      class="flex flex-col items-center"
      style="gap:11px"
    >
      <button
        type="button"
        disabled={reconnect.retrying}
        onclick={() => retryNow()}
        class="inline-flex items-center border font-semibold transition-[background,border-color] duration-[180ms] ease-in-out {reconnect.retrying
          ? 'cursor-default'
          : 'cursor-pointer hover:bg-[#fbf1e4]'}"
        style="gap:8px;border-radius:10px;padding:10px 20px;font-size:13px;white-space:nowrap;background:{reconnect.retrying
          ? '#f7e4c6'
          : 'var(--color-surface)'};border-color:{reconnect.retrying
          ? '#f0d2ae'
          : '#e0b071'};color:{reconnect.retrying ? '#8b6b36' : '#9a6a14'}"
      >
        <RefreshIcon
          size={14}
          strokeWidth={2.2}
          style="animation:{reconnect.retrying
            ? 'spin 900ms linear infinite'
            : 'none'};transform-origin:50% 50%"
        />
        {reconnect.retrying ? "연결 중" : "지금 재연결"}
      </button>
      <!-- 자동 재시도가 돈다는 사실을 말한다. 이것이 없으면 사용자는 버튼을 누르는 것 말고
           할 일이 없다고 느낀다. -->
      <div class="text-text-muted" style="font-size:11px;white-space:nowrap">
        {retryNote()}
      </div>
    </div>
  {/snippet}
</EmptyState>
