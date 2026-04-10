import { Badge } from "../ui/badge";
import type { StatusBranch } from "../../types/ezstack";

interface BranchStatusBadgeProps {
  branch: StatusBranch;
}

export function BranchStatusBadge({ branch }: BranchStatusBadgeProps) {
  if (branch.is_merged) {
    return <Badge variant="purple">Merged</Badge>;
  }
  if (!branch.pr_state) {
    return <Badge variant="muted">No PR</Badge>;
  }
  switch (branch.pr_state) {
    case "OPEN":
      return <Badge variant="success">Open</Badge>;
    case "DRAFT":
      return <Badge variant="muted">Draft</Badge>;
    case "MERGED":
      return <Badge variant="purple">Merged</Badge>;
    case "CLOSED":
      return <Badge variant="destructive">Closed</Badge>;
    default:
      return <Badge variant="outline">{branch.pr_state}</Badge>;
  }
}
