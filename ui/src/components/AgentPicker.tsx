import { useEffect, useState } from "react";
import { useSessionStore } from "@/stores/sessionStore";
import { api } from "@/api/client";
import { PROFILE_DISPLAY, PROMPT_PROFILE_DISPLAY } from "@/types";
import type { PromptProfileInfo } from "@/types";
import { SecurityBadge } from "@/components/SecurityBadge";

interface AgentPickerProps {
  onClose: () => void;
}

export function AgentPicker({ onClose }: AgentPickerProps) {
  const { agents, createSession, loadAgents } = useSessionStore();
  const [creating, setCreating] = useState<string | null>(null);
  const [profiles, setProfiles] = useState<PromptProfileInfo[]>(() =>
    Object.entries(PROMPT_PROFILE_DISPLAY).map(([id, profile]) => ({
      id,
      display_name: profile.label,
      description: profile.description,
    })),
  );
  const [selectedProfile, setSelectedProfile] = useState("standard");

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  useEffect(() => {
    let cancelled = false;

    api.listPromptProfiles()
      .then((nextProfiles) => {
        if (!cancelled && nextProfiles.length > 0) {
          setProfiles(nextProfiles);
        }
      })
      .catch((err) => {
        console.error("Failed to load prompt profiles:", err);
      });

    return () => {
      cancelled = true;
    };
  }, []);

  const handleSelect = async (agentId: string) => {
    if (creating) return;
    setCreating(agentId);
    try {
      await createSession(agentId, selectedProfile);
      onClose();
    } catch (err) {
      console.error("Failed to create session:", err);
    } finally {
      setCreating(null);
    }
  };

  const handleRefresh = () => {
    loadAgents();
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4"
      onClick={onClose}
    >
      <div
        className="w-full max-w-md bg-slate-800 rounded-2xl border border-slate-700 shadow-xl animate-fade-in"
        role="dialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-slate-700">
          <h2 className="text-lg font-semibold text-slate-100">
            New Session
          </h2>
          <div className="flex items-center gap-2">
            <button
              onClick={handleRefresh}
              className="text-slate-400 hover:text-slate-200 p-2 rounded-lg"
              title="Refresh"
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                  d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
              </svg>
            </button>
            <button
              onClick={onClose}
              className="text-slate-400 hover:text-slate-200 p-2 rounded-lg"
            >
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>

        <div className="px-4 pt-4 pb-2 border-b border-slate-700/70">
          <div className="mb-2">
            <h3 className="text-sm font-medium text-slate-100">Prompt Profile</h3>
            <p className="text-xs text-slate-400 mt-1">
              Choose how the agent should frame the session before it starts.
            </p>
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {profiles.map((profile) => {
              const selected = selectedProfile === profile.id;
              return (
                <button
                  key={profile.id}
                  type="button"
                  onClick={() => setSelectedProfile(profile.id)}
                  className={`rounded-xl border px-3 py-3 text-left transition-colors ${
                    selected
                      ? "border-teal-500 bg-teal-500/10 text-slate-100"
                      : "border-slate-700 bg-slate-800/50 text-slate-300 hover:border-slate-600 hover:bg-slate-700/60"
                  }`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-sm font-medium">{profile.display_name}</span>
                    {selected && (
                      <span className="rounded-full bg-teal-500/20 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-teal-300">
                        selected
                      </span>
                    )}
                  </div>
                  <p className="mt-1 text-xs leading-relaxed text-slate-400">
                    {profile.description}
                  </p>
                </button>
              );
            })}
          </div>
        </div>

        {/* Agent list */}
        <div className="p-3 max-h-80 overflow-y-auto">
          {(!agents || agents.length === 0) ? (
            <div className="text-center py-8 text-slate-500">
              <p className="mb-2">No agents online</p>
              <p className="text-xs mb-4">Make sure a runtime is connected to the hub.</p>
              <button
                onClick={handleRefresh}
                className="text-xs text-teal-400 hover:text-teal-300 transition-colors"
              >
                Refresh
              </button>
            </div>
          ) : (
            <div className="space-y-2">
              {agents.map((ep) => {
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
                    className="w-full text-left px-4 py-4 rounded-xl bg-slate-700/50 hover:bg-slate-700
                               border border-slate-600/50 hover:border-teal-500/50
                               hover:shadow-md hover:shadow-teal-900/10
                               active:scale-[0.99]
                               transition-all duration-150
                               disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <div className="flex items-center gap-3">
                      <span
                        className={`
                          inline-flex items-center justify-center w-12 h-12 rounded-xl text-xl
                          ${profile.color} text-white
                        `}
                      >
                        {profile.icon}
                      </span>
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2 font-medium text-slate-100">
                          {ep.name || profile.label}
                          <span
                            className={`inline-block w-2 h-2 rounded-full ${
                              ep.online ? "bg-green-400" : "bg-red-400"
                            }`}
                            title={ep.online ? "Online" : "Offline"}
                          />
                          <SecurityBadge security={ep.security} />
                        </div>
                        <div className="text-xs text-slate-400 mt-0.5">
                          {profile.label} &middot; {ep.id.slice(0, 12)}
                        </div>
                      </div>
                      {creating === ep.id && (
                        <svg className="w-5 h-5 animate-spin text-teal-400 flex-shrink-0" fill="none" viewBox="0 0 24 24">
                          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                        </svg>
                      )}
                    </div>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
