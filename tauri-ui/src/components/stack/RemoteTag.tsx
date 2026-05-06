import { cn } from "../../lib/utils";

interface RemoteTagProps {
  // "branch" — pickup branch row; "stack" — pickup-rooted stack header/root.
  // Picks the right tooltip wording for the surface; both render the same
  // (remote) text so the user sees one consistent phrasing across the UI.
  kind?: "branch" | "stack";
  className?: string;
}

const TOOLTIPS = {
  branch:
    "Pickup branch — belongs to another contributor. Excluded from bulk sync (ezs sync -a) by default.",
  stack:
    "Stack rooted on a pickup branch (ezs new -r) — belongs to another contributor.",
} as const;

export function RemoteTag({ kind = "branch", className }: RemoteTagProps) {
  return (
    <span
      title={TOOLTIPS[kind]}
      className={cn("text-muted-foreground/70", className)}
    >
      (remote)
    </span>
  );
}
