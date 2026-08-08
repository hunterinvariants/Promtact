import { useCallback, useEffect, useState } from "react";
import { api, ApiError, isPlatformAdmin, Session } from "./api";
import Alerts from "./pages/Alerts";
import Approvals from "./pages/Approvals";
import Assets from "./pages/Assets";
import Demonstration from "./pages/Demonstration";
import Detections from "./pages/Detections";
import Events from "./pages/Events";
import Health from "./pages/Health";
import Overview from "./pages/Overview";
import Settings from "./pages/Settings";
import Tenants from "./pages/Tenants";

type PageKey =
  | "overview"
  | "health"
  | "approvals"
  | "alerts"
  | "events"
  | "assets"
  | "detections"
  | "tenants"
  | "demonstration"
  | "settings";

const PAGES: { key: PageKey; label: string; icon: string; adminOnly?: boolean; demoOnly?: boolean; title: string; subtitle: string }[] = [
  { key: "overview", label: "Overview", icon: "◎", title: "Overview", subtitle: "Current posture across your estate" },
  {
    key: "health",
    label: "System health",
    icon: "❍",
    title: "System health",
    subtitle: "Whether every part of the deployment is doing its job",
  },
  {
    // Second in the list, ahead of alerts, because this is the only page where
    // something is waiting on a person. An alert can be read tomorrow; a held
    // tool call has an agent stopped mid-task behind it.
    key: "approvals",
    label: "Approvals",
    icon: "⧗",
    title: "Approvals",
    subtitle: "Tool calls the gateway is holding for a person",
  },
  { key: "alerts", label: "Alerts", icon: "⚑", title: "Alerts", subtitle: "Correlated detections awaiting triage" },
  {
    key: "events",
    label: "Events",
    icon: "≡",
    title: "Events",
    subtitle: "Everything collected, searchable",
  },
  { key: "assets", label: "Assets", icon: "▤", title: "Assets", subtitle: "Hosts and agent surfaces under watch" },
  {
    key: "detections",
    label: "Detection coverage",
    icon: "◈",
    title: "Detection coverage",
    subtitle: "ATT&CK techniques the gateway enforces",
  },
  {
    key: "tenants",
    label: "Customers",
    icon: "⚭",
    adminOnly: true,
    title: "Customers",
    subtitle: "Provision and manage tenant accounts",
  },
  {
    // Only on a deployment started with --demo-tools. A button in a production
    // console that runs a scripted attack is not a feature.
    key: "demonstration",
    label: "Demonstration",
    icon: "▶",
    demoOnly: true,
    title: "Demonstration",
    subtitle: "The same agent, with and without the gateway",
  },
  { key: "settings", label: "Settings", icon: "⚙", title: "Settings", subtitle: "Access and deployment details" },
];

function useTheme(): [string, () => void] {
  const [theme, setTheme] = useState(() => localStorage.getItem("promtact-theme") || "system");

  useEffect(() => {
    const root = document.documentElement;
    if (theme === "system") root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", theme);
    localStorage.setItem("promtact-theme", theme);
  }, [theme]);

  const cycle = useCallback(() => {
    setTheme((current) => (current === "system" ? "light" : current === "light" ? "dark" : "system"));
  }, []);

  return [theme, cycle];
}

function Login({ onSignedIn, sso }: { onSignedIn: (session: Session) => void; sso?: Session["sso"] }) {
  const [username, setUsername] = useState("");
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const session = await api.login(username, token);
      onSignedIn(session);
    } catch (err: any) {
      setError(err.status === 401 ? "Invalid user name or API key." : err.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <div className="brand" style={{ padding: 0, marginBottom: 16 }}>
          <div className="brand-mark">P</div>
          <div>
            <div className="brand-name" style={{ color: "var(--text-primary)" }}>
              Promtact
            </div>
            <div className="brand-sub">Sign in to your console</div>
          </div>
        </div>

        <div className="field">
          <label htmlFor="login-user">User name</label>
          <input
            id="login-user"
            className="input"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            spellCheck={false}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="login-token">API key</label>
          <input
            id="login-token"
            className="input"
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoComplete="current-password"
            required
          />
        </div>

        <div className="form-error" role="status" aria-live="polite">
          {error}
        </div>

        <button className="btn btn-primary" type="submit" disabled={busy} style={{ width: "100%", justifyContent: "center" }}>
          {busy ? "Signing in…" : "Sign in"}
        </button>

        {sso?.oidc || sso?.saml ? (
          <div className="row" style={{ marginTop: 10, gap: 8 }}>
            {sso.oidc ? (
              <a className="btn" href="/api/sso/oidc/login" style={{ flex: 1, justifyContent: "center" }}>
                Sign in with OIDC
              </a>
            ) : null}
            {sso.saml ? (
              <a className="btn" href="/api/sso/saml/login" style={{ flex: 1, justifyContent: "center" }}>
                Sign in with SAML
              </a>
            ) : null}
          </div>
        ) : null}
      </form>
    </div>
  );
}

export default function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [ready, setReady] = useState(false);
  const [page, setPage] = useState<PageKey>("overview");
  const [theme, cycleTheme] = useTheme();
  const [demoMode, setDemoMode] = useState(false);

  const refreshSession = useCallback(async () => {
    try {
      const current = await api.session();
      setSession(current);
    } catch {
      setSession({ authenticated: false });
    } finally {
      setReady(true);
    }
  }, []);

  useEffect(() => {
    refreshSession();
  }, [refreshSession]);

  useEffect(() => {
    if (!session?.authenticated) return;
    api
      .status()
      .then((status) => setDemoMode(Boolean(status?.demo_tools)))
      .catch(() => setDemoMode(false));
  }, [session?.authenticated]);

  const signOut = async () => {
    try {
      await api.logout();
    } catch {
      /* fall through: clearing local state is what matters */
    }
    setSession({ authenticated: false });
    setPage("overview");
  };

  if (!ready) return null;

  if (!session?.authenticated) {
    return <Login sso={session?.sso} onSignedIn={(s) => setSession(s)} />;
  }

  const admin = isPlatformAdmin(session);
  const visiblePages = PAGES.filter(
    (item) => (!item.adminOnly || admin) && (!item.demoOnly || demoMode),
  );
  const active = visiblePages.find((item) => item.key === page) || visiblePages[0];

  const renderPage = () => {
    switch (active.key) {
      case "health":
        return <Health onNavigate={setPage} />;
      case "approvals":
        return <Approvals />;
      case "demonstration":
        return <Demonstration onNavigate={setPage} />;
      case "alerts":
        return <Alerts />;
      case "events":
        return <Events />;
      case "assets":
        return <Assets />;
      case "detections":
        return <Detections />;
      case "tenants":
        return <Tenants />;
      case "settings":
        return <Settings session={session} />;
      default:
        return <Overview onNavigate={setPage} />;
    }
  };

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">P</div>
          <div>
            <div className="brand-name">Promtact</div>
            <div className="brand-sub">{session.principal?.tenant || "default"}</div>
          </div>
        </div>

        <nav className="nav" aria-label="Sections">
          <div className="nav-label">Monitor</div>
          {visiblePages
            .filter((item) => !["tenants", "settings"].includes(item.key))
            .map((item) => (
              <button
                key={item.key}
                className="nav-item"
                aria-current={item.key === active.key ? "page" : undefined}
                onClick={() => setPage(item.key)}
              >
                <span className="nav-icon" aria-hidden="true">
                  {item.icon}
                </span>
                {item.label}
              </button>
            ))}

          <div className="nav-label">Manage</div>
          {visiblePages
            .filter((item) => ["tenants", "settings"].includes(item.key))
            .map((item) => (
              <button
                key={item.key}
                className="nav-item"
                aria-current={item.key === active.key ? "page" : undefined}
                onClick={() => setPage(item.key)}
              >
                <span className="nav-icon" aria-hidden="true">
                  {item.icon}
                </span>
                {item.label}
              </button>
            ))}
        </nav>

        <div className="sidebar-footer">
          {session.principal?.name}
          <br />
          {(session.principal?.roles || []).join(" · ")}
        </div>
      </aside>

      <div className="main">
        <header className="topbar">
          <div>
            <h1>{active.title}</h1>
            <div className="topbar-sub">{active.subtitle}</div>
          </div>
          <div className="spacer" />
          <button className="btn" onClick={cycleTheme} title={`Theme: ${theme}`}>
            {theme === "dark" ? "☾" : theme === "light" ? "☀" : "◐"} Theme
          </button>
          <button className="btn" onClick={signOut}>
            Sign out
          </button>
        </header>

        <main className="content">{renderPage()}</main>
      </div>
    </div>
  );
}
