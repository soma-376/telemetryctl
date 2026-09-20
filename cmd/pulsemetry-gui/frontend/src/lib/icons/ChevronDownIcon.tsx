import { cssStyle } from "$lib/react-utils";

export default function ChevronDownIcon({
  size = 14,
  strokeWidth = 1.8,
  className: klass = "",
  style = "",
  rotated,
}: {
  size?: number;
  strokeWidth?: number;
  className?: string;
  style?: string;
  rotated?: boolean;
}) {
  const composed =
    rotated === undefined
      ? style
      : `transform:rotate(${rotated ? 180 : 0}deg);` +
        `transition:transform 200ms cubic-bezier(0.32,0.72,0,1);${style}`;

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
        style={cssStyle(composed)}
        className={klass}
      >
        <path d="M6 9l6 6 6-6" />
      </svg>
    </>
  );
}
