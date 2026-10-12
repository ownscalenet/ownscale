// Logo is the ownscale mark (see public/favicon.svg), inlined so its dots
// follow the current text color and stay visible in both themes.
export default function Logo({ className }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 64 64"
      className={className}
      aria-hidden="true"
    >
      <g fill="currentColor">
        <circle cx="12" cy="12" r="8" />
        <circle cx="32" cy="12" r="8" />
        <circle cx="52" cy="12" r="8" />
        <circle cx="12" cy="32" r="8" />
        <circle cx="52" cy="32" r="8" />
        <circle cx="12" cy="52" r="8" />
        <circle cx="32" cy="52" r="8" />
        <circle cx="52" cy="52" r="8" />
      </g>
      <circle cx="32" cy="32" r="8" fill="currentColor" opacity="0.4" />
    </svg>
  )
}
