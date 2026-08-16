import {
  ArrowRightLeft,
  Cable,
  CopyPlus,
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
export const sshIcon = SquareTerminal;
export const sftpIcon = FolderOpen;

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
