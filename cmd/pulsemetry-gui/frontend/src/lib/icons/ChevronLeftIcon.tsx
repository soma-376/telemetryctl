export default function ChevronLeftIcon({
  size = 16,
  strokeWidth = 1.8,
  className: klass = "",
}: {
  size?: number;
  strokeWidth?: number;
  className?: string;
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
        className={klass}
      >
        <path d="m15 5-7 7 7 7" />
      </svg>
    </>
  );
}
