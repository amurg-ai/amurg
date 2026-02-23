import { useState } from "react";
import { useSessionStore } from "@/stores/sessionStore";

export function PermissionBanner() {
  const activeSessionId = useSessionStore((s) => s.activeSessionId);
  const pendingPermissions = useSessionStore((s) => s.pendingPermissions);
  const respondToPermission = useSessionStore((s) => s.respondToPermission);

  const requests = activeSessionId ? pendingPermissions.get(activeSessionId) || [] : [];

  if (requests.length === 0) return null;

  return (
    <div className="border-t border-amber-700/50 bg-amber-950/30">
      {requests.map((req) => (
        <PermissionRequestCard
          key={req.request_id}
          sessionId={req.session_id}
          requestId={req.request_id}
          tool={req.tool}
          description={req.description}
          resource={req.resource}
          onRespond={respondToPermission}
        />
      ))}
    </div>
  );
}

function PermissionRequestCard({
  sessionId,
  requestId,
  tool,
  description,
  resource,
  onRespond,
}: {
  sessionId: string;
  requestId: string;
  tool: string;
  description: string;
  resource?: string;
  onRespond: (sessionId: string, requestId: string, approved: boolean, alwaysAllow?: boolean) => void;
}) {
  const [alwaysAllow, setAlwaysAllow] = useState(false);

  return (
    <div className="px-4 py-3 flex items-start gap-3 border-b border-amber-700/30 last:border-b-0">
      {/* Warning icon */}
      <div className="flex-shrink-0 mt-0.5">
        <svg className="w-5 h-5 text-amber-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
            d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
        </svg>
      </div>

      {/* Content */}
      <div className="flex-1 min-w-0">
        <div className="text-sm text-amber-200 font-medium">
          Permission requested: <span className="font-mono text-amber-100">{tool}</span>
        </div>
        <p className="text-xs text-amber-300/80 mt-0.5">{description}</p>
        {resource && (
          <code className="block text-xs text-amber-400/70 mt-1 bg-amber-950/50 px-2 py-1 rounded font-mono truncate">
            {resource}
          </code>
        )}

        {/* Actions */}
        <div className="flex items-center gap-3 mt-2">
          <button
            onClick={() => onRespond(sessionId, requestId, true, alwaysAllow)}
            className="px-3 py-1 bg-green-700 hover:bg-green-600 text-white text-xs font-medium rounded transition-colors"
          >
            Approve
          </button>
          <button
            onClick={() => onRespond(sessionId, requestId, false)}
            className="px-3 py-1 bg-red-700 hover:bg-red-600 text-white text-xs font-medium rounded transition-colors"
          >
            Deny
          </button>
          <label className="flex items-center gap-1.5 text-xs text-amber-300/70 cursor-pointer">
            <input
              type="checkbox"
              checked={alwaysAllow}
              onChange={(e) => setAlwaysAllow(e.target.checked)}
              className="rounded border-amber-600 bg-amber-950 text-teal-500 focus:ring-teal-500 focus:ring-offset-0 w-3.5 h-3.5"
            />
            Always allow <span className="font-mono">{tool}</span> this session
          </label>
        </div>
      </div>
    </div>
  );
}
