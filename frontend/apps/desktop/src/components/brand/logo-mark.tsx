import { cn } from "@/lib/utils";

/** Aurora KL — indigo canvas, white stem, emerald orb, amber arc. */
export function LogoMark({
  className,
  title = "KubeLoop",
}: {
  className?: string;
  title?: string;
}) {
  return (
    <svg
      viewBox="0 0 1024 1024"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      role="img"
      aria-label={title}
      className={cn("size-9", className)}
    >
      <title>{title}</title>
      <defs>
        <linearGradient id="kl-stem" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#ffffff" />
          <stop offset="1" stopColor="#e0e7ff" />
        </linearGradient>
        <linearGradient id="kl-bg" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#6366f1" />
          <stop offset="1" stopColor="#4338ca" />
        </linearGradient>
      </defs>
      <rect width="1024" height="1024" rx="228" fill="url(#kl-bg)" />
      <rect x="300" y="210" width="100" height="600" rx="50" fill="url(#kl-stem)" />
      <circle cx="620" cy="360" r="120" fill="#a6e3a1" />
      <path d="M420 480 L780 700 L420 700 Z" fill="#fab387" />
      <rect x="300" y="740" width="380" height="90" rx="45" fill="url(#kl-stem)" />
    </svg>
  );
}
