import { useState } from "react";
import { useSessionStore } from "@/stores/sessionStore";

interface QuestionOption {
  label: string;
  description?: string;
}

interface AskUserQuestionInput {
  questions?: {
    question: string;
    header?: string;
    options: QuestionOption[];
    multiSelect?: boolean;
  }[];
}

/**
 * Renders an AskUserQuestion tool call as interactive cards.
 * When the user selects an option, the answer is sent as a regular
 * user message. Claude Code picks it up on the next --resume turn.
 */
export function QuestionRenderer({
  content,
  sessionId: _sessionId,
}: {
  content: string;
  sessionId: string;
}) {
  void _sessionId;
  const sendMessage = useSessionStore((s) => s.sendMessage);
  const [answered, setAnswered] = useState<string | null>(null);
  const [customText, setCustomText] = useState("");

  let toolCall: { name: string; input: AskUserQuestionInput };
  try {
    toolCall = JSON.parse(content);
  } catch {
    return <pre className="text-xs text-slate-400">{content}</pre>;
  }

  const questions = toolCall.input?.questions;
  if (!questions?.length) {
    return (
      <div className="text-xs text-slate-400 italic">
        Agent requested input (no questions provided)
      </div>
    );
  }

  const handleAnswer = (answer: string) => {
    setAnswered(answer);
    sendMessage(answer);
  };

  if (answered) {
    return (
      <div className="my-2 rounded-lg border border-teal-700/40 bg-teal-950/20 px-4 py-3">
        <div className="flex items-center gap-2 text-xs text-teal-400">
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
          </svg>
          Answered: {answered}
        </div>
      </div>
    );
  }

  return (
    <div className="my-2 space-y-3">
      {questions.map((q, qi) => (
        <div
          key={qi}
          className="rounded-lg border border-amber-700/40 bg-amber-950/20 px-4 py-3"
        >
          {q.header && (
            <span className="inline-block text-[10px] font-medium uppercase tracking-wider text-amber-500 bg-amber-950/60 px-2 py-0.5 rounded mb-2">
              {q.header}
            </span>
          )}
          <p className="text-sm text-amber-200 mb-3">{q.question}</p>

          <div className="flex flex-wrap gap-2">
            {q.options.map((opt, oi) => (
              <button
                key={oi}
                onClick={() => handleAnswer(opt.label)}
                className="px-3 py-1.5 text-sm bg-slate-800 hover:bg-slate-700 text-slate-200 rounded-lg border border-slate-600/50 transition-colors"
                title={opt.description}
              >
                {opt.label}
              </button>
            ))}
          </div>

          {/* Custom input */}
          <div className="flex items-center gap-2 mt-3">
            <input
              type="text"
              value={customText}
              onChange={(e) => setCustomText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && customText.trim()) {
                  handleAnswer(customText.trim());
                }
              }}
              placeholder="Or type a custom answer..."
              className="flex-1 bg-slate-900 border border-slate-700 rounded-lg px-3 py-1.5 text-sm text-slate-200 placeholder-slate-500 focus:outline-none focus:border-amber-600"
            />
            <button
              onClick={() => {
                if (customText.trim()) handleAnswer(customText.trim());
              }}
              disabled={!customText.trim()}
              className="px-3 py-1.5 text-sm bg-amber-700 hover:bg-amber-600 disabled:bg-slate-700 disabled:text-slate-500 text-white rounded-lg transition-colors"
            >
              Send
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}
