import { cn } from "@/lib/utils";

/** Bauhaus KL: blue stem+foot, green circle, amber triangle. */
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
      <rect width="1024" height="1024" rx="228" fill="#FAFAFA" />
      <rect x="300" y="210" width="100" height="600" fill="#326CE5" />
      <circle cx="620" cy="360" r="120" fill="#30A46C" />
      <polygon points="420,480 780,700 420,700" fill="#F5A524" />
      <rect x="300" y="740" width="380" height="90" fill="#326CE5" />
    </svg>
  );
}
