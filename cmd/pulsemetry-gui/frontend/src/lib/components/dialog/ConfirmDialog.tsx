import { useWindowEvent } from "$lib/react-utils";
import { useEffect, useState } from "react";

export default function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = "확인",
  cancelLabel = "취소",
  danger = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const [cancelButton, setCancelButton] = useState<HTMLButtonElement | null>(
    null,
  );

  useEffect(() => {
    if (open) cancelButton?.focus();
  }, [open, cancelButton]);

  useWindowEvent("keydown", (e) => {
    if (open && e.key === "Escape") {
      e.preventDefault();
      onCancel();
    }
  });

  return (
    <>
      {open ? (
        <>
          <div className="fixed inset-0 flex items-center justify-center z-[70]">
            <button
              type="button"
              aria-label={cancelLabel}
              onClick={onCancel}
              className="absolute inset-0 cursor-default border-none [background:rgba(27,26,24,0.22)] animate-[fadeIn_160ms_ease-out]"
            />
            <div
              role="alertdialog"
              aria-modal="true"
              aria-label={title}
              className="bg-surface border-border relative flex flex-col border w-[340px] max-w-[calc(100vw_-_48px)] rounded-[16px] [box-shadow:0_18px_48px_rgba(27,26,24,0.16)] animate-[popIn_200ms_cubic-bezier(0.32,0.72,0,1)] p-[20px_22px_18px]"
            >
              <div className="text-text font-bold text-[15px] tracking-[-0.01em] mb-[8px]">
                {title}
              </div>
              <div className="text-text-secondary whitespace-pre-line text-[12.5px] leading-[1.6] mb-[18px]">
                {message}
              </div>
              <div className="flex justify-end gap-[8px]">
                <button
                  ref={setCancelButton}
                  type="button"
                  onClick={onCancel}
                  className="border-border text-text hover:border-border-strong cursor-pointer border bg-transparent font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out rounded-[9px] p-[8px_14px] text-[12.5px]"
                >
                  {cancelLabel}
                </button>
                <button
                  type="button"
                  onClick={onConfirm}
                  className={[
                    String(
                      danger
                        ? "bg-danger hover:bg-danger-strong"
                        : "bg-accent hover:bg-accent-hover",
                    ) +
                      " cursor-pointer border-none font-semibold whitespace-nowrap transition-colors duration-[120ms] ease-in-out",
                    "text-[#ffffff] rounded-[9px] p-[8px_14px] text-[12.5px]",
                  ]
                    .filter(Boolean)
                    .join(" ")}
                >
                  {confirmLabel}
                </button>
              </div>
            </div>
          </div>
        </>
      ) : null}
    </>
  );
}
