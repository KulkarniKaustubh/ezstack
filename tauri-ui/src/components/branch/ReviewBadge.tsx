import { CheckCircle, AlertCircle, Clock } from "lucide-react";
import { Tooltip } from "../ui/tooltip";

interface ReviewBadgeProps {
  state?: string;
}

export function ReviewBadge({ state }: ReviewBadgeProps) {
  if (!state) return null;

  switch (state) {
    case "APPROVED":
      return (
        <Tooltip content="Approved">
          <CheckCircle className="h-3.5 w-3.5 text-success" />
        </Tooltip>
      );
    case "CHANGES_REQUESTED":
      return (
        <Tooltip content="Changes requested">
          <AlertCircle className="h-3.5 w-3.5 text-warning" />
        </Tooltip>
      );
    case "REVIEW_REQUIRED":
      return (
        <Tooltip content="Review required">
          <Clock className="h-3.5 w-3.5 text-muted-foreground" />
        </Tooltip>
      );
    default:
      return null;
  }
}
