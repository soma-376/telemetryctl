<script lang="ts">
  import ConfirmDialog from "./ConfirmDialog.svelte";
  import { quitApp } from "$lib/ipc/app";

  // 종료 문구와 quitApp 배선을 여기에 캡슐화한다.
  // 호스트(메인 셸, 트레이 퀵뷰)는 열림 상태만 관리하면 된다.
  let { open, onClose }: { open: boolean; onClose: () => void } = $props();
</script>

<ConfirmDialog
  {open}
  danger
  title="Pulsemetry 종료"
  message={"Pulsemetry를 종료할까요?\n트레이 아이콘이 사라지고 사용량 집계가 멈춰요."}
  confirmLabel="종료"
  cancelLabel="취소"
  onConfirm={quitApp}
  onCancel={onClose}
/>
