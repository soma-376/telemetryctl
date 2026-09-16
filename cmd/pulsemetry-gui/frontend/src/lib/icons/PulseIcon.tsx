import { cssStyle } from "$lib/react-utils";

export default function PulseIcon({
  size = 19,
  strokeWidth = 1.9,
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
        strokeLinejoin="round"
        width={size}
        height={size}
        strokeWidth={strokeWidth}
        style={cssStyle(style)}
        className={klass}
      >
        <path d="M3 13h3l2.5-7 4 14 2.5-7H21" />
      </svg>
    </>
  );
}
