import { useEffect, useState, type FormEvent } from 'react';
import { DroneRegistry } from './DroneRegistry';
import { AUTH_ACTIVITY_EVENT, AUTH_EXPIRED_EVENT, ApiError, request, type Role, type User } from './api';

const roles: Record<Role, { label: string; description: string; areas: string[] }> = {
  system_administrator: {
    label: 'System administrator', description: 'Manage platform access across organisations.',
    areas: ['Organisation administration'],
  },
  fleet_manager: {
    label: 'Fleet manager', description: 'Coordinate your organisation’s fleet and team.',
    areas: ['Fleet overview', 'Mission scheduling', 'Maintenance history', 'Team management'],
  },
  pilot: {
    label: 'Pilot', description: 'Review your fleet and prepare for assigned missions.',
    areas: ['Fleet overview', 'Mission assignments', 'Technical issue reporting'],
  },
  maintenance_technician: {
    label: 'Maintenance technician', description: 'Keep your organisation’s aircraft ready for service.',
    areas: ['Maintenance alerts', 'Service history', 'Technical issues'],
  },
};

function message(error: unknown) { return error instanceof Error ? error.message : 'Please try again.'; }

export function App() {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [initialError, setInitialError] = useState('');
  const [notice, setNotice] = useState('');
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setInitialError('');
    request<{ user: User }>('me')
      .then(data => { if (active) setUser(data.user); })
      .catch(error => {
        if (!active) return;
        setUser(null);
        if (!(error instanceof ApiError && error.status === 401)) setInitialError(message(error));
      })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [retry]);

  // No background polling: it would keep an idle server session alive.
  useEffect(() => {
    if (!user) return;
    let timer: number;
    function resetTimer() {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        setUser(null);
        setNotice('Your session has expired. Sign in again.');
      }, 30 * 60 * 1000);
    }
    resetTimer();
    window.addEventListener(AUTH_ACTIVITY_EVENT, resetTimer);
    return () => { window.clearTimeout(timer); window.removeEventListener(AUTH_ACTIVITY_EVENT, resetTimer); };
  }, [user]);

  useEffect(() => {
    const expired = () => { setUser(null); setNotice('Your session has expired. Sign in again.'); };
    window.addEventListener(AUTH_EXPIRED_EVENT, expired);
    return () => window.removeEventListener(AUTH_EXPIRED_EVENT, expired);
  }, []);

  function signedOut(text: string) { setUser(null); setNotice(text); }

  return (
    <main className={user ? 'app-main' : undefined}>
      <header>
        <p className="eyebrow">Drone Fleet Management</p>
        <h1>AeroSync</h1>
        <p className="intro">One workspace for your mixed-vendor drone fleet.</p>
      </header>
      {loading ? <p role="status">Checking your session…</p> : initialError ? (
        <section className="panel" aria-label="Connection problem">
          <p role="alert" className="error">{initialError}</p>
          <button onClick={() => setRetry(value => value + 1)}>Retry connection</button>
        </section>
      ) : user ? <Workspace user={user} signedOut={signedOut} /> : (
        <Login notice={notice} signedIn={value => { setUser(value); setNotice(''); }} />
      )}
    </main>
  );
}

function Login({ notice, signedIn }: { notice: string; signedIn: (user: User) => void }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    setBusy(true); setError('');
    try {
      const data = await request<{ user: User }>('login', { email, password });
      setPassword(''); signedIn(data.user);
    } catch (error) { setError(message(error)); setPassword(''); }
    finally { setBusy(false); }
  }

  return (
    <section className="panel auth-panel" aria-labelledby="signin-title">
      <h2 id="signin-title">Sign in to your workspace</h2>
      <p>Use the account provided by your administrator or fleet manager.</p>
      {notice && <p role="status">{notice}</p>}
      <form onSubmit={submit} aria-busy={busy}>
        <label htmlFor="email">Email address</label>
        <input id="email" type="email" autoComplete="username" required maxLength={254}
          value={email} onChange={event => setEmail(event.target.value)} disabled={busy} />
        <label htmlFor="password">Password</label>
        <input id="password" type="password" autoComplete="current-password" required
          value={password} onChange={event => setPassword(event.target.value)} disabled={busy} />
        {error && <p role="alert" className="error">{error}</p>}
        <button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
      </form>
    </section>
  );
}

function Workspace({ user, signedOut }: { user: User; signedOut: (text: string) => void }) {
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const role = roles[user.role];

  async function logout() {
    setBusy(true); setError('');
    try { await request<void>('logout', {}); signedOut('You have signed out.'); }
    catch (error) { setError(message(error)); }
    finally { setBusy(false); }
  }

  return (
    <section className="panel" aria-labelledby="workspace-title">
      <div className="workspace-heading">
        <div>
          <p className="eyebrow">{role.label}</p>
          <h2 id="workspace-title">Welcome, {user.name}</h2>
          <p>{role.description}</p>
        </div>
        <button className="secondary" onClick={logout} disabled={busy}>{busy ? 'Signing out…' : 'Sign out'}</button>
      </div>
      <p className="account">{user.email}</p>
      {user.role === 'system_administrator' ? <>
        <h3>Your workspace</h3>
        <ul className="areas">{role.areas.map(area => <li key={area}>{area}<span>Coming soon</span></li>)}</ul>
      </> : <DroneRegistry canManage={user.role === 'fleet_manager'} />}
      <p className="muted">Sessions expire after 30 minutes of inactivity.</p>
      {error && <p role="alert" className="error">{error}</p>}
      <button className="secondary" aria-expanded={showPassword} aria-controls="password-panel"
        onClick={() => setShowPassword(value => !value)}>{showPassword ? 'Cancel password change' : 'Change password'}</button>
      {showPassword && <PasswordForm signedOut={signedOut} />}
    </section>
  );
}

function PasswordForm({ signedOut }: { signedOut: (text: string) => void }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy) return;
    if (next !== confirmation) { setError('New passwords do not match.'); return; }
    const length = new TextEncoder().encode(next).length;
    if (length < 12 || length > 72) { setError('Password must be between 12 and 72 bytes.'); return; }
    setBusy(true); setError('');
    try {
      await request<void>('password', { current_password: current, new_password: next });
      signedOut('Password changed. All sessions were signed out. Sign in with your new password.');
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        signedOut('Your session has expired. Sign in again.');
      } else { setError(message(error)); }
    } finally { setBusy(false); }
  }

  return (
    <form id="password-panel" className="password-form" onSubmit={submit} aria-busy={busy}>
      <h3>Change your password</h3>
      <p id="password-help">Use 12–72 bytes (at least 12 characters for an ASCII password). Changing your password signs you out on all devices.</p>
      <label htmlFor="current-password">Current password</label>
      <input id="current-password" type="password" autoComplete="current-password" required disabled={busy}
        value={current} onChange={event => setCurrent(event.target.value)} />
      <label htmlFor="new-password">New password</label>
      <input id="new-password" type="password" autoComplete="new-password" required aria-describedby="password-help" disabled={busy}
        value={next} onChange={event => setNext(event.target.value)} />
      <label htmlFor="confirm-password">Confirm new password</label>
      <input id="confirm-password" type="password" autoComplete="new-password" required disabled={busy}
        value={confirmation} onChange={event => setConfirmation(event.target.value)} />
      {error && <p role="alert" className="error">{error}</p>}
      <button type="submit" disabled={busy}>{busy ? 'Updating password…' : 'Update password'}</button>
    </form>
  );
}
