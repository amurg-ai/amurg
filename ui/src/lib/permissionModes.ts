export interface PermissionModeOption {
  value: string;
  label: string;
  summary: string;
}

export const PERMISSION_MODE_OPTIONS: PermissionModeOption[] = [
  {
    value: "",
    label: "Default",
    summary: "Use the agent's built-in approval behavior.",
  },
  {
    value: "strict",
    label: "Ask Every Time",
    summary: "Most restrictive. Require approval before actions whenever the adapter supports it.",
  },
  {
    value: "auto",
    label: "Balanced",
    summary: "Good default for day-to-day work. Let the adapter auto-approve routine actions when supported.",
  },
  {
    value: "plan",
    label: "Plan First",
    summary: "Have the agent plan before acting when the adapter supports it.",
  },
  {
    value: "acceptEdits",
    label: "Approve Edits",
    summary: "Auto-approve edits while still gating higher-risk actions when supported.",
  },
  {
    value: "dontAsk",
    label: "Don't Ask",
    summary: "Minimize prompts when the adapter supports it.",
  },
  {
    value: "skip",
    label: "Full Access",
    summary: "No permission prompts. The agent can edit files and run commands freely.",
  },
  {
    value: "bypassPermissions",
    label: "Bypass Checks",
    summary: "Force bypass adapter permission checks. Advanced only.",
  },
];

export function getPermissionModeOption(
  value?: string | null,
): PermissionModeOption {
  return (
    PERMISSION_MODE_OPTIONS.find((option) => option.value === (value ?? "")) ?? {
      value: value ?? "",
      label: value || "Default",
      summary: "Custom permission mode.",
    }
  );
}

export function formatPermissionModeLabel(value?: string | null): string {
  return getPermissionModeOption(value).label;
}
