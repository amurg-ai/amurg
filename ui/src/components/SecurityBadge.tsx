import type { SecurityProfile } from "@/types";
import { getPermissionModeOption } from "@/lib/permissionModes";

export function SecurityBadge({ security }: { security?: string | SecurityProfile }) {
  if (!security) return null;

  let parsed: SecurityProfile;
  if (typeof security === "string") {
    try {
      parsed = JSON.parse(security);
    } catch {
      return null;
    }
  } else {
    parsed = security;
  }

  if (!parsed.permission_mode && !parsed.allowed_tools?.length) return null;

  const mode = parsed.permission_mode || "";
  const option = getPermissionModeOption(parsed.permission_mode);

  let icon: string;
  let color: string;

  switch (mode) {
    case "strict":
      icon = "\uD83D\uDD12";
      color = "text-amber-400";
      break;
    case "skip":
    case "bypassPermissions":
    case "dontAsk":
      icon = "\u26A0\uFE0F";
      color = "text-red-400";
      break;
    default:
      icon = "\uD83D\uDEE1\uFE0F";
      color = "text-teal-400";
  }

  const details: string[] = [];
  if (parsed.allowed_tools?.length) details.push(`Tools: ${parsed.allowed_tools.join(", ")}`);
  if (parsed.allowed_paths?.length) details.push(`Paths: ${parsed.allowed_paths.join(", ")}`);
  if (parsed.cwd) details.push(`CWD: ${parsed.cwd}`);

  const tooltip = `${option.label} - ${option.summary}${details.length ? "\n" + details.join("\n") : ""}`;

  return (
    <span className={`${color} text-xs`} title={tooltip}>
      {icon}
    </span>
  );
}
