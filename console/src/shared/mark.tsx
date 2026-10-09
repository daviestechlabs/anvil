export function AnvilMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <rect width="32" height="32" rx="6" fill="currentColor" className="text-foreground" />
      <path
        fill="currentColor"
        className="text-background"
        d="M4 16l7-5h2v-1h10v2h3v4h-3v3h-2l3 7H8l3-7h-2v-3H7z"
      />
      <rect x="18" y="11" width="2" height="2" fill="currentColor" className="text-foreground" />
    </svg>
  );
}
