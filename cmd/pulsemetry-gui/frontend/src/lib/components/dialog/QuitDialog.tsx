import { quitApp } from "$lib/ipc/app";
import ConfirmDialog from "./ConfirmDialog";

export default function QuitDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  return (
    <>
      <ConfirmDialog
        open={open}
        danger={true}
        title="Pulsemetry 종료"
        message="Pulsemetry를 종료할까요?&#xA;트레이 아이콘이 사라지고 사용량 집계가 멈춰요."
        confirmLabel="종료"
        cancelLabel="취소"
        onConfirm={quitApp}
        onCancel={onClose}
      />
    </>
  );
}
