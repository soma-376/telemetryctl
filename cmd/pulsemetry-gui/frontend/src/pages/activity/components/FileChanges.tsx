import ChevronDownIcon from "$lib/icons/ChevronDownIcon";
import { Fragment } from "react";
import { FILE_CAP } from "../model";
import type { FileChange } from "../types";

export default function FileChanges({
  files,
  open,
  onToggle,
}: {
  files: FileChange[];
  open: boolean;
  onToggle?: () => void;
}) {
  const visible = open ? files : files.slice(0, FILE_CAP);

  return (
    <>
      <div className="bg-surface border-border flex flex-col border rounded-[12px] p-[14px_16px]">
        <div className="text-text-secondary font-semibold text-[12.5px] pb-[8px] mb-[6px]">
          파일 변경
        </div>
        {visible.map((file) => (
          <Fragment key={file.dir + file.name}>
            <div className="grid items-center grid-cols-[minmax(0,1fr)_auto_32px] gap-[9px] p-[7px_0]">
              <span className="flex min-w-0 items-baseline [font-family:var(--font-mono)] text-[11.5px]">
                <span className="text-text-muted min-w-0 overflow-hidden text-ellipsis whitespace-nowrap [flex:0_1_auto] [direction:rtl] [text-align:left]">
                  <bdi>{file.dir}</bdi>
                </span>
                <span className="text-text flex-none">{file.name}</span>
              </span>
              <span
                style={{
                  color:
                    file.add === "-"
                      ? "var(--color-text-muted)"
                      : "var(--color-success)",
                }}
                className="text-[12px] tabular-nums"
              >
                {file.add}
              </span>
              <span
                style={{
                  color:
                    file.del === "-"
                      ? "var(--color-text-muted)"
                      : "var(--color-danger)",
                }}
                className="text-[12px] tabular-nums [text-align:right]"
              >
                {file.del}
              </span>
            </div>
          </Fragment>
        ))}
        {files.length > FILE_CAP ? (
          <>
            <button
              type="button"
              onClick={() => onToggle?.()}
              className="bg-surface border-border text-text hover:border-border-strong flex w-full cursor-pointer items-center justify-center border gap-[7px] text-[12px] font-medium p-[9px] rounded-[9px] mt-[auto]"
            >
              {open ? "접기" : `파일 ${files.length}개 모두 보기`}
              <ChevronDownIcon
                size={12}
                strokeWidth={2.4}
                rotated={open}
                className="text-text-muted"
              />
            </button>
          </>
        ) : null}
      </div>
    </>
  );
}
