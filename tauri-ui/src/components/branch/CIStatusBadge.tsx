import { CheckCircle, XCircle, Loader, MinusCircle } from "lucide-react";
import { Tooltip } from "../ui/tooltip";

interface CIStatusBadgeProps {
  state?: string;
  summary?: string;
}

export function CIStatusBadge({ state, summary }: CIStatusBadgeProps) {
  if (!state) return null;

  const tooltipText = summary || state;

  switch (state) {
    case "success":
      return (
        <Tooltip content={tooltipText}>
          <CheckCircle className="h-3.5 w-3.5 text-success" />
        </Tooltip>
      );
    case "failure":
      return (
        <Tooltip content={tooltipText}>
          <XCircle className="h-3.5 w-3.5 text-destructive" />
        </Tooltip>
      );
    case "pending":
      return (
        <Tooltip content={tooltipText}>
          <Loader className="h-3.5 w-3.5 text-warning animate-spin" />
        </Tooltip>
      );
    default:
      return (
        <Tooltip content="No CI">
          <MinusCircle className="h-3.5 w-3.5 text-muted-foreground/50" />
        </Tooltip>
      );
  }
}
