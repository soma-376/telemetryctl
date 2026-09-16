export default function PowerIcon({
  size = 19,
  strokeWidth = 1.9,
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
        width={size}
        height={size}
        strokeWidth={strokeWidth}
        className={klass}
      >
        <path d="M12 4v8" />
        <path d="M7.5 7A7 7 0 1 0 16.5 7" />
      </svg>
    </>
  );
}
