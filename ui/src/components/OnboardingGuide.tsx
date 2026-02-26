import { useState, useEffect, useMemo } from "react";
import { useSessionStore } from "@/stores/sessionStore";

function CopyButton({ text, id, copied, onCopy }: {
  text: string;
  id: number;
  copied: number | null;
  onCopy: (text: string, id: number) => void;
}) {
  return (
    <button
      onClick={() => onCopy(text, id)}
      className="absolute top-2 right-2 px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600
                 text-slate-300 opacity-0 group-hover:opacity-100 transition-opacity"
    >
      {copied === id ? "Copied!" : "Copy"}
    </button>
  );
}

function CodeBlock({ command, id, copied, onCopy }: {
  command: string;
  id: number;
  copied: number | null;
  onCopy: (text: string, id: number) => void;
}) {
  return (
    <div className="relative group mt-2">
      <pre className="bg-slate-900 rounded-lg px-4 py-3 text-sm font-mono text-teal-300 overflow-x-auto">
        <code>{command}</code>
      </pre>
      <CopyButton text={command} id={id} copied={copied} onCopy={onCopy} />
    </div>
  );
}

function Step({ number, title, children }: {
  number: number;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-2xl bg-slate-800/60 border border-slate-700/50 p-4">
      <div className="flex items-start gap-3">
        <span className="flex-shrink-0 w-7 h-7 rounded-full bg-teal-600 text-white text-sm font-medium
                         flex items-center justify-center">
          {number}
        </span>
        <div className="flex-1 min-w-0">
          <h3 className="text-sm font-medium text-slate-200">{title}</h3>
          {children}
        </div>
      </div>
    </div>
  );
}

export function OnboardingGuide() {
  const loadAgents = useSessionStore((s) => s.loadAgents);
  const [copied, setCopied] = useState<number | null>(null);

  const hubUrl = useMemo(
    () => `${window.location.protocol}//${window.location.host}`,
    [],
  );

  // Poll for agents every 5 seconds
  useEffect(() => {
    const interval = setInterval(() => { loadAgents(); }, 5000);
    return () => clearInterval(interval);
  }, [loadAgents]);

  const handleCopy = (text: string, id: number) => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(id);
      setTimeout(() => setCopied(null), 2000);
    });
  };

  return (
    <div className="flex items-center justify-center h-full px-4 sm:px-6">
      <div className="w-full max-w-lg py-8 animate-fade-in">
        {/* Header */}
        <div className="text-center mb-8">
          <svg className="w-12 h-12 text-slate-600 mx-auto mb-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5}
              d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
          </svg>
          <h2 className="text-xl font-semibold text-slate-200 mb-1">Get your first agent online</h2>
          <p className="text-sm text-slate-500">
            Install and connect a runtime to start using agents.
          </p>
        </div>

        {/* Steps */}
        <div className="space-y-4">
          <Step number={1} title="Install the runtime">
            <p className="text-xs text-slate-400 mt-1">
              Download the Amurg runtime binary on the machine that will host your agents.
            </p>
            <CodeBlock
              command="curl -fsSL https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.sh | sh"
              id={1}
              copied={copied}
              onCopy={handleCopy}
            />
          </Step>

          <Step number={2} title="Configure the runtime">
            <p className="text-xs text-slate-400 mt-1">
              Run the setup wizard. When prompted for the hub URL, use:
            </p>
            <div className="relative group mt-2">
              <code className="block bg-slate-900 rounded-lg px-4 py-2 text-sm font-mono text-amber-300 overflow-x-auto">
                {hubUrl}
              </code>
              <CopyButton text={hubUrl} id={20} copied={copied} onCopy={handleCopy} />
            </div>
            <CodeBlock
              command="amurg-runtime init"
              id={2}
              copied={copied}
              onCopy={handleCopy}
            />
            <p className="text-xs text-slate-500 mt-2">
              If the runtime is on a different network, use the hub's externally reachable address.
            </p>
          </Step>

          <Step number={3} title="Start the runtime">
            <p className="text-xs text-slate-400 mt-1">
              Launch the runtime. Agents will appear here automatically once connected.
            </p>
            <CodeBlock
              command="amurg-runtime run"
              id={3}
              copied={copied}
              onCopy={handleCopy}
            />
          </Step>
        </div>

        {/* Waiting indicator */}
        <div className="flex items-center justify-center gap-2 mt-8 text-sm text-slate-500">
          <span className="w-2 h-2 bg-teal-500 rounded-full animate-pulse" />
          Waiting for a runtime to connect...
        </div>

        {/* Refresh + docs link */}
        <div className="flex items-center justify-center gap-4 mt-3">
          <button
            onClick={() => loadAgents()}
            className="text-sm text-teal-400 hover:text-teal-300 transition-colors"
          >
            Refresh
          </button>
          <span className="text-slate-700">|</span>
          <a
            href="https://amurg.ai/docs/installation"
            target="_blank"
            rel="noopener noreferrer"
            className="text-sm text-slate-500 hover:text-slate-300 transition-colors"
          >
            Full docs
          </a>
        </div>
      </div>
    </div>
  );
}
