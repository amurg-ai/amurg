import { useState, useEffect, useRef } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "@/api/client";

export function ConnectRuntime() {
  const [searchParams] = useSearchParams();
  const [userCode, setUserCode] = useState("");
  const [runtimeName, setRuntimeName] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<{ runtime_id: string } | null>(null);
  const autoSubmitted = useRef(false);

  // Pre-fill code from URL query param.
  useEffect(() => {
    const code = searchParams.get("code");
    if (code) {
      handleCodeChange(code);
    }
  }, [searchParams]);

  // Auto-submit when code is pre-filled from URL and valid.
  useEffect(() => {
    if (autoSubmitted.current) return;
    if (userCode.length === 9 && searchParams.get("code")) {
      autoSubmitted.current = true;
      doSubmit(userCode, runtimeName);
    }
  }, [userCode]);

  const handleCodeChange = (value: string) => {
    // Strip non-alphanumeric except hyphen, uppercase
    let cleaned = value.replace(/[^a-zA-Z0-9-]/g, "").toUpperCase();
    // Auto-insert hyphen after 4 characters
    if (cleaned.length > 4 && cleaned[4] !== "-") {
      cleaned = cleaned.slice(0, 4) + "-" + cleaned.slice(4);
    }
    // Limit to XXXX-XXXX (9 chars with hyphen)
    setUserCode(cleaned.slice(0, 9));
  };

  const doSubmit = async (code: string, name: string) => {
    setError("");
    setResult(null);
    setLoading(true);

    try {
      const res = await api.approveRuntimeRegistration(code, name);
      setResult({ runtime_id: res.runtime_id });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to approve registration");
    } finally {
      setLoading(false);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    await doSubmit(userCode, runtimeName);
  };

  if (result) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-slate-900 px-4">
        <div className="w-full max-w-sm text-center">
          <div className="mb-6">
            <div className="w-16 h-16 bg-teal-600/20 rounded-full flex items-center justify-center mx-auto mb-4">
              <svg className="w-8 h-8 text-teal-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
              </svg>
            </div>
            <h1 className="text-2xl font-bold text-slate-100">Runtime Connected</h1>
            <p className="text-slate-400 mt-2">
              Your runtime has been registered successfully.
            </p>
            <p className="text-xs text-slate-500 mt-1 font-mono">
              ID: {result.runtime_id}
            </p>
          </div>
          <p className="text-slate-400 text-sm mb-6">
            You can close this tab or return to your agents.
          </p>
          <Link
            to="/"
            className="inline-block px-6 py-2 bg-teal-600 hover:bg-teal-700 text-white rounded-lg font-medium transition-colors"
          >
            Go to Agents
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-slate-900 px-4">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-bold text-slate-100">Connect Runtime</h1>
          <p className="text-slate-400 mt-2">
            {searchParams.get("code")
              ? "Approving your runtime registration..."
              : "Enter the code displayed on the runtime"}
          </p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          {error && (
            <div className="bg-red-900/50 text-red-300 px-4 py-2 rounded-lg text-sm">
              {error}
            </div>
          )}

          <div>
            <label htmlFor="userCode" className="block text-sm text-slate-400 mb-1">
              Device Code
            </label>
            <input
              id="userCode"
              type="text"
              value={userCode}
              onChange={(e) => handleCodeChange(e.target.value)}
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg
                         text-slate-100 placeholder-slate-500 font-mono text-center text-lg tracking-widest
                         focus:outline-none focus:ring-2 focus:ring-teal-500 focus:border-transparent"
              placeholder="XXXX-XXXX"
              autoFocus
              required
              maxLength={9}
            />
          </div>

          <div>
            <label htmlFor="runtimeName" className="block text-sm text-slate-400 mb-1">
              Runtime Name <span className="text-slate-500">(optional)</span>
            </label>
            <input
              id="runtimeName"
              type="text"
              value={runtimeName}
              onChange={(e) => setRuntimeName(e.target.value)}
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg
                         text-slate-100 placeholder-slate-500
                         focus:outline-none focus:ring-2 focus:ring-teal-500 focus:border-transparent"
              placeholder="e.g. production-server"
            />
          </div>

          <button
            type="submit"
            disabled={loading || userCode.length < 9}
            className="w-full py-2 bg-teal-600 hover:bg-teal-700 disabled:bg-teal-800
                       disabled:cursor-not-allowed text-white rounded-lg font-medium transition-colors"
          >
            {loading ? "Approving..." : "Approve Registration"}
          </button>
        </form>

        <div className="text-center mt-6">
          <Link to="/" className="text-teal-400 hover:text-teal-300 text-sm">
            Back to home
          </Link>
        </div>
      </div>
    </div>
  );
}
