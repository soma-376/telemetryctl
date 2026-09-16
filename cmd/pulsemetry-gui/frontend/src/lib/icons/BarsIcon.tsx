export default function BarsIcon({
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
        <rect x="3.5" y="4" width="17" height="16" rx="3" />
        <path d="M8.5 15.5v-3M12 15.5v-6M15.5 15.5v-4" />
      </svg>
    </>
  );
}
