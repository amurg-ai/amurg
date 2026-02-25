import { useSessionStore } from "@/stores/sessionStore";
import { PROFILE_DISPLAY } from "@/types";

interface SessionListProps {
  onSelect: () => void;
}

export function SessionList({ onSelect }: SessionListProps) {
  const { sessions, activeSessionId, selectSession, unreadCounts } = useSessionStore();

  if (sessions.length === 0) {
    return (
      <div className="p-4 text-sm text-slate-500 text-center">
        No sessions yet. Create a new session to get started.
      </div>
    );
  }

  return (
    <div className="space-y-0.5 p-2">
      {sessions.map((session) => {
        const isActive = session.id === activeSessionId;
        const profile = PROFILE_DISPLAY[session.profile] || {
          label: session.profile,
          color: "bg-slate-600",
          icon: "?",
        };
        const unread = unreadCounts.get(session.id) || 0;

        return (
          <button
            key={session.id}
            onClick={() => {
              selectSession(session.id);
              onSelect();
            }}
            className={`
              w-full text-left px-3 py-3 rounded-lg transition-all duration-150 text-sm
              ${
                isActive
                  ? "bg-teal-900/30 text-slate-100 border-l-2 border-teal-400"
                  : "text-slate-300 hover:bg-slate-700/50 border-l-2 border-transparent"
              }
            `}
          >
            <div className="flex items-center gap-2">
              <span
                className={`
                  inline-flex items-center justify-center w-8 h-8 rounded-lg text-sm
                  ${profile.color} text-white
                `}
              >
                {profile.icon}
              </span>
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium truncate">
                    {session.endpoint_name || profile.label}
                    {session.seq != null && ` #${session.seq}`}
                  </span>
                  <span
                    className={`
                      w-2 h-2 rounded-full flex-shrink-0
                      ${
                        session.state === "active"
                          ? "bg-green-400"
                          : session.state === "responding"
                            ? "bg-yellow-400 animate-pulse"
                            : "bg-slate-500"
                      }
                    `}
                  />
                </div>
                <div className="text-xs text-slate-500 truncate">
                  {formatTimeAgo(session.updated_at)}
                </div>
              </div>
              {unread > 0 && (
                <span className="bg-teal-500 text-white text-xs rounded-full w-5 h-5 flex items-center justify-center flex-shrink-0">
                  {unread > 9 ? "9+" : unread}
                </span>
              )}
            </div>
          </button>
        );
      })}
    </div>
  );
}

function formatTimeAgo(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMin = Math.floor(diffMs / 60000);

  if (diffMin < 1) return "just now";
  if (diffMin < 60) return `${diffMin}m ago`;

  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour}h ago`;

  const diffDay = Math.floor(diffHour / 24);
  return `${diffDay}d ago`;
}
