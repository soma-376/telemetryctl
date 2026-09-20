export default function ListIcon({
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
        <path d="M9 6h11M9 12h11M9 18h11" />
        <circle cx="4.5" cy="6" r="1.3" fill="currentColor" stroke="none" />
        <circle cx="4.5" cy="12" r="1.3" fill="currentColor" stroke="none" />
        <circle cx="4.5" cy="18" r="1.3" fill="currentColor" stroke="none" />
      </svg>
    </>
  );
}
