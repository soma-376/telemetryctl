import { cssStyle } from "$lib/react-utils";

export default function RefreshIcon({
  size = 14,
  strokeWidth = 2,
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
        <path d="M20 12a8 8 0 1 1-2.4-5.7" />
        <path d="M20 4.5V9h-4.5" />
      </svg>
    </>
  );
}
