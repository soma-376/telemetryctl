import { cssStyle } from "$lib/react-utils";

export default function CheckIcon({
  size = 16,
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
        <path d="M20 7 10 17l-6-6" />
      </svg>
    </>
  );
}
