export function AnvilMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <rect width="32" height="32" rx="4" fill="#160f0b" />
      <rect
        x="0.5"
        y="0.5"
        width="31"
        height="31"
        rx="3.5"
        fill="none"
        stroke="#e8692f"
        strokeOpacity="0.55"
      />
      <path fill="#e8692f" d="M4 16l7-5h2v-1h10v2h3v4h-3v3h-2l3 7H8l3-7h-2v-3H7z" />
      <rect x="18" y="11" width="2" height="2" fill="#160f0b" />
      <path fill="#ff9a55" d="M8 26h16v1H8z" opacity="0.6" />
    </svg>
  );
}
