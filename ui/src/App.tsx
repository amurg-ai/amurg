import { useEffect, lazy, Suspense } from "react";
import { Routes, Route, Navigate } from "react-router-dom";
import { useSessionStore } from "@/stores/sessionStore";
import { Login } from "@/components/Login";
import { Chat } from "@/components/Chat";

const ConnectRuntime = lazy(() =>
  import("@/components/ConnectRuntime").then((m) => ({ default: m.ConnectRuntime }))
);

export function App() {
  const { isAuthenticated, init } = useSessionStore();
  useEffect(() => { init(); }, [init]);
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      {isAuthenticated ? (
        <>
          <Route
            path="/connect"
            element={
              <Suspense fallback={null}>
                <ConnectRuntime />
              </Suspense>
            }
          />
          <Route path="/*" element={<Chat />} />
        </>
      ) : (
        <Route path="/*" element={<Navigate to="/login" replace />} />
      )}
    </Routes>
  );
}
