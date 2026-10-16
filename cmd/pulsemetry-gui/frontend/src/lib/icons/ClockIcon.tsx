import { cssStyle } from "$lib/react-utils";

export default function ClockIcon({
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
        <circle cx="12" cy="12" r="8.5" />
        <path d="M12 7.5V12l3 2" />
      </svg>
    </>
  );
}
