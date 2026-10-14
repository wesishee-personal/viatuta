import { useEffect, useRef, useState } from 'react';
import { ApiError } from '../api/client';
import { PASSWORD_MIN_LENGTH } from '../api/types';

interface Props {
  /** Why the rider was asked, e.g. "report a hazard". Null when they opened it themselves. */
  reason: string | null;
  onSignIn: (email: string, password: string) => Promise<void>;
  onRegister: (email: string, password: string, displayName: string) => Promise<void>;
  onClose: () => void;
}

/** Field-level detail beats the generic message ("password is too weak"). */
function describe(err: unknown): string {
  if (err instanceof ApiError) {
    const detail = Object.values(err.details)[0];
    return detail ?? err.message;
  }
  return err instanceof Error ? err.message : 'something went wrong';
}

export function AuthDialog({ reason, onSignIn, onRegister, onClose }: Props) {
  const ref = useRef<HTMLDialogElement>(null);
  const [mode, setMode] = useState<'signin' | 'register'>('signin');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // A native <dialog> gives us focus trapping, Escape to close, and a
  // backdrop for free, which hand-rolled modals usually get subtly wrong.
  useEffect(() => {
    const dialog = ref.current;
    if (dialog && !dialog.open) dialog.showModal();
  }, []);

  const registering = mode === 'register';

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      if (registering) await onRegister(email.trim(), password, displayName.trim());
      else await onSignIn(email.trim(), password);
      // On success the provider closes the dialog by unmounting it.
    } catch (err) {
      setError(describe(err));
      setBusy(false);
    }
  }

  return (
    <dialog ref={ref} className="auth-dialog" onClose={onClose} aria-labelledby="auth-title">
      <form onSubmit={(e) => void submit(e)}>
        <h2 id="auth-title">{registering ? 'Create an account' : 'Sign in'}</h2>
        {reason && <p className="hint">You need an account to {reason}.</p>}

        <label className="field">
          <span>Email</span>
          <input
            type="email"
            autoComplete="email"
            required
            autoFocus
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>

        <label className="field">
          <span>Password</span>
          <input
            type="password"
            autoComplete={registering ? 'new-password' : 'current-password'}
            required
            minLength={registering ? PASSWORD_MIN_LENGTH : undefined}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          {registering && <em className="hint">At least {PASSWORD_MIN_LENGTH} characters.</em>}
        </label>

        {registering && (
          <label className="field">
            <span>
              Display name <em>(optional)</em>
            </span>
            <input
              type="text"
              autoComplete="nickname"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          </label>
        )}

        {error && (
          <p className="auth-dialog__error" role="alert">
            {error}
          </p>
        )}

        <div className="row">
          <button type="submit" className="primary" disabled={busy || !email || !password}>
            {busy ? 'Please wait…' : registering ? 'Create account' : 'Sign in'}
          </button>
          <button type="button" onClick={() => ref.current?.close()}>
            Cancel
          </button>
        </div>

        <p className="hint">
          {registering ? 'Already have an account? ' : 'New here? '}
          <button
            type="button"
            className="link"
            onClick={() => {
              setMode(registering ? 'signin' : 'register');
              setError(null);
            }}
          >
            {registering ? 'Sign in' : 'Create an account'}
          </button>
        </p>
      </form>
    </dialog>
  );
}
