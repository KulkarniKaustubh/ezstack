import * as vscode from "vscode";

/** Returns the user-configured ticket regex, or undefined if not set / invalid. */
export function getTicketRegex(): RegExp | undefined {
  const pattern = vscode.workspace
    .getConfiguration("ezstack")
    .get<string>("ticketPattern", "");
  if (!pattern) {
    return undefined;
  }
  try {
    return new RegExp(pattern, "i");
  } catch {
    return undefined;
  }
}

/** Extract a ticket label from a branch name using the configured pattern. */
export function extractTicket(branchName: string): string | undefined {
  const re = getTicketRegex();
  if (!re) {
    return undefined;
  }
  const match = branchName.match(re);
  return match ? match[0] : undefined;
}

/** Extract a short display name from a dotted branch name (last segment). */
export function shortBranchName(branchName: string): string {
  const parts = branchName.split(".");
  return parts.length > 1 ? parts[parts.length - 1] : branchName;
}
