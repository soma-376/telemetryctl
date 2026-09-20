import type { ReactNode as Snippet } from "react";
import Mascot from "./Mascot";
import type { MascotPose } from "./mascot.types";

export default function EmptyState({
  pose = "no-data",
  title,
  description = "",
  size = "lg",
  action,
}: {
  pose?: MascotPose;
  title: string;
  description?: string;
  size?: "lg" | "sm";
  action?: Snippet;
}) {
  return (
    <>
      {size === "lg" ? (
        <>
          <div className="flex flex-col items-center justify-center text-center gap-[14px] p-[36px_24px]">
            <Mascot pose={pose} height={88} />
            <div>
              <div className="text-text font-bold text-[15px] mb-[6px]">
                {title}
              </div>
              {description ? (
                <>
                  <div className="text-text-secondary text-[12.5px] leading-[1.6]">
                    {description}
                  </div>
                </>
              ) : null}
            </div>
            {action}
          </div>
        </>
      ) : (
        <>
          <div className="flex items-center gap-[10px] p-[14px_2px]">
            <Mascot pose={pose} height={30} />
            <div className="min-w-0 flex-1">
              <div className="text-text truncate font-semibold text-[12.5px]">
                {title}
              </div>
              {description ? (
                <>
                  <div className="text-text-muted truncate text-[10.5px] mt-[2px]">
                    {description}
                  </div>
                </>
              ) : null}
            </div>
            {action}
          </div>
        </>
      )}
    </>
  );
}
