import { useState, useEffect } from "react";
import { useSessionStore } from "@/stores/sessionStore";
import { PROFILE_DISPLAY } from "@/types";
import { SecurityBadge } from "@/components/SecurityBadge";
import { OnboardingGuide } from "@/components/OnboardingGuide";

export function AgentHomeScreen() {
  const { endpoints, createSession, loadEndpoints } = useSessionStore();
  const [creating, setCreating] = useState<string | null>(null);

  useEffect(() => { loadEndpoints(); }, [loadEndpoints]);

  const handleSelect = async (endpointId: string) => {
    if (creating) return;
    setCreating(endpointId);
    try {
      await createSession(endpointId);
    } catch (err) {
      console.error("Failed to create session:", err);
    } finally {
      setCreating(null);
    }
  };

  if (!endpoints || endpoints.length === 0) {
    return <OnboardingGuide />;
  }

  return (
    <div className="flex items-center justify-center h-full px-4 sm:px-6">
      <div className="w-full max-w-4xl py-8 animate-fade-in">
        <div className="text-center mb-8">
          <h2 className="text-xl font-semibold text-slate-200 mb-1">Choose an agent</h2>
          <p className="text-sm text-slate-500">Select an agent to start a new session</p>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {endpoints.map((ep) => {
            const profile = PROFILE_DISPLAY[ep.profile] || {
              label: ep.profile,
              color: "bg-slate-600",
              icon: "?",
            };

            return (
              <button
                key={ep.id}
                onClick={() => handleSelect(ep.id)}
                disabled={!!creating}
                className="group relative flex flex-col items-center gap-3 p-6 rounded-2xl
                           bg-slate-800/60 border border-slate-700/50
                           hover:bg-slate-700/80 hover:border-teal-500/30 hover:shadow-lg hover:shadow-teal-900/10
                           active:scale-[0.98]
                           transition-all duration-150
                           disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <span
                  className={`
                    inline-flex items-center justify-center w-14 h-14 rounded-xl text-2xl
                    ${profile.color} text-white
                    group-hover:scale-105 transition-transform duration-150
                  `}
                >
                  {profile.icon}
                </span>

                <span className="text-base font-medium text-slate-100 text-center">
                  {ep.name || profile.label}
                </span>

                <div className="flex items-center gap-2">
                  <span className={`text-xs px-2 py-0.5 rounded-full ${profile.color} text-white/90`}>
                    {profile.label}
                  </span>
                  <span
                    className={`w-2 h-2 rounded-full ${ep.online ? "bg-green-400" : "bg-red-400"}`}
                    title={ep.online ? "Online" : "Offline"}
                  />
                  <SecurityBadge security={ep.security} />
                </div>

                <span className="text-xs text-slate-500">{ep.id.slice(0, 12)}</span>

                {creating === ep.id && (
                  <div className="absolute inset-0 flex items-center justify-center bg-slate-800/80 rounded-2xl">
                    <svg className="w-6 h-6 animate-spin text-teal-400" fill="none" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                    </svg>
                  </div>
                )}
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
