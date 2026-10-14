import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { fetchMe, login as apiLogin, register as apiRegister, setSessionExpiredHandler } from '../api/client';
import { clearToken, getToken, setToken } from '../api/session';
import type { AuthResponse, AuthUser } from '../api/types';
import { AuthDialog } from './AuthDialog';

export type AuthStatus = 'restoring' | 'signedOut' | 'signedIn';

interface AuthValue {
  status: AuthStatus;
  user: AuthUser | null;
  /** Open the sign-in dialog without a follow-up action. */
  openSignIn: () => void;
  /**
   * Run `action` now if signed in; otherwise ask the rider to sign in and run
   * it once they have. `reason` explains why, e.g. "report a hazard".
   */
  requireAuth: (action: () => void, reason: string) => void;
  signOut: () => void;
}

const AuthContext = createContext<AuthValue | null>(null);

export function useAuth(): AuthValue {
  const value = useContext(AuthContext);
  if (!value) throw new Error('useAuth must be used inside <AuthProvider>');
  return value;
}

interface Prompt {
  reason: string | null;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>(() => (getToken() ? 'restoring' : 'signedOut'));
  const [user, setUser] = useState<AuthUser | null>(null);
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  // Kept in a ref so finishing a sign-in can run the action that triggered it
  // without that action being a dependency of anything.
  const pendingAction = useRef<(() => void) | null>(null);

  // Validate any stored token once on load.
  useEffect(() => {
    if (status !== 'restoring') return;
    const ctrl = new AbortController();
    fetchMe(ctrl.signal)
      .then((me) => {
        setUser(me);
        setStatus('signedIn');
      })
      .catch(() => {
        if (ctrl.signal.aborted) return;
        // A token the server rejected was already cleared by the client.
        // Offline or a server error leaves the token in place for the next
        // load; either way the rider isn't signed in right now.
        setStatus('signedOut');
      });
    return () => ctrl.abort();
  }, []); // once, on mount

  const drop = useCallback(() => {
    clearToken();
    setUser(null);
    setStatus('signedOut');
  }, []);

  // The client reports a token the server no longer accepts.
  useEffect(() => {
    setSessionExpiredHandler(drop);
    return () => setSessionExpiredHandler(null);
  }, [drop]);

  const finish = useCallback((res: AuthResponse) => {
    setToken(res.token);
    setUser(res.user);
    setStatus('signedIn');
    setPrompt(null);
    const action = pendingAction.current;
    pendingAction.current = null;
    action?.();
  }, []);

  const value = useMemo<AuthValue>(
    () => ({
      status,
      user,
      openSignIn: () => {
        pendingAction.current = null;
        setPrompt({ reason: null });
      },
      requireAuth: (action, reason) => {
        if (status === 'signedIn') {
          action();
          return;
        }
        pendingAction.current = action;
        setPrompt({ reason });
      },
      signOut: drop,
    }),
    [status, user, drop],
  );

  return (
    <AuthContext.Provider value={value}>
      {children}
      {prompt && (
        <AuthDialog
          reason={prompt.reason}
          onSignIn={async (email, password) => finish(await apiLogin(email, password))}
          onRegister={async (email, password, name) => finish(await apiRegister(email, password, name))}
          onClose={() => {
            pendingAction.current = null;
            setPrompt(null);
          }}
        />
      )}
    </AuthContext.Provider>
  );
}
