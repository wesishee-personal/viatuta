import { useAuth } from './AuthContext';

/** Sign-in button, or the signed-in account with a way out. */
export function AccountMenu() {
  const { status, user, openSignIn, signOut } = useAuth();

  if (status === 'restoring') return null;

  if (status === 'signedIn' && user) {
    return (
      <div className="account">
        <span className="account__who" title={user.email}>
          {user.display_name || user.email}
        </span>
        <button type="button" className="link" onClick={signOut}>
          Sign out
        </button>
      </div>
    );
  }

  return (
    <div className="account">
      <button type="button" onClick={openSignIn}>
        Sign in
      </button>
    </div>
  );
}
