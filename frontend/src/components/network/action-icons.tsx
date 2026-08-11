import {
  ArrowRightLeft,
  Cable,
  CopyPlus,
  Eye,
  FolderOpen,
  SquareTerminal,
  type LucideIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";

export const portForwardIcon = Cable;
export const exchangeIcon = ArrowRightLeft;
export const mirrorIcon = CopyPlus;
export const previewIcon = Eye;
export const sshIcon = SquareTerminal;
export const sftpIcon = FolderOpen;

export type NetworkAction = "portForward" | "exchange" | "mirror" | "preview";

export const networkActionIcons: Record<NetworkAction, LucideIcon> = {
  portForward: portForwardIcon,
  exchange: exchangeIcon,
  mirror: mirrorIcon,
  preview: previewIcon,
};

export function ActionIconButton({
  label,
  icon: Icon,
  text,
  disabled,
  onClick,
  variant = "outline",
}: {
  label: string;
  icon: LucideIcon;
  text?: string;
  disabled?: boolean;
  onClick(): void;
  variant?: "outline" | "ghost" | "default";
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex">
          <Button
            type="button"
            size={text ? "sm" : "icon-sm"}
            variant={variant}
            disabled={disabled}
            aria-label={label}
            onClick={onClick}
          >
            <Icon size={14} strokeWidth={1.9} />
            {text ? <span className="max-w-24 truncate">{text}</span> : null}
          </Button>
        </span>
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={6}>
        {label}
      </TooltipContent>
    </Tooltip>
  );
}

export function ActionTypeBadge({
  label,
  icon: Icon,
}: {
  label: string;
  icon: LucideIcon;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className="inline-flex size-7 items-center justify-center rounded-md border bg-muted/40 text-muted-foreground"
          aria-label={label}
        >
          <Icon size={14} strokeWidth={1.9} />
        </span>
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={6}>
        {label}
      </TooltipContent>
    </Tooltip>
  );
}
