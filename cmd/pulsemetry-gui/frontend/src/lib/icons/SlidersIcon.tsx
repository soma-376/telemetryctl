export default function SlidersIcon({
  size = 19,
  strokeWidth = 1.7,
  knobFill = "var(--color-bg)",
  className: klass = "",
}: {
  size?: number;
  strokeWidth?: number;
  knobFill?: string;
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
        <path d="M4 8h16M4 16h16" />
        <circle cx="10" cy="8" r="2.4" fill={knobFill} />
        <circle cx="15" cy="16" r="2.4" fill={knobFill} />
      </svg>
    </>
  );
}
