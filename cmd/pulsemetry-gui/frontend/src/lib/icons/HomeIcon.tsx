export default function HomeIcon({
  size = 19,
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
        <path d="M4 10.5 12 4l8 6.5V20H4z" />
      </svg>
    </>
  );
}
