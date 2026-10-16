import { cssStyle } from "$lib/react-utils";

export default function XIcon({
  size = 14,
  strokeWidth = 2.2,
  className: klass = "",
  style = "",
}: {
  size?: number;
  strokeWidth?: number;
  className?: string;
  style?: string;
}) {
  return (
    <>
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeLinecap="round"
        width={size}
        height={size}
        strokeWidth={strokeWidth}
        style={cssStyle(style)}
        className={klass}
      >
        <path d="m7 7 10 10M17 7 7 17" />
      </svg>
    </>
  );
}
