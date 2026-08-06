import { useEffect, useState } from "react";
import { api, Session } from "../api";
import { Badge, Panel } from "../components";

export default function Settings({ session }: { session: Session | null }) {
  const [status, setStatus] = useState<any>(null);

  useEffect(() => {
    api.status().then(setStatus).catch(() => setStatus(null));
  }, []);

  const principal = session?.principal;

  return (
    <>
      <Panel title="Your access">
        <div className="stack">
          <div className="row">
            <span className="panel-note" style={{ minWidth: 110 }}>
              Signed in as
            </span>
            <strong>{principal?.name || "—"}</strong>
          </div>
          <div className="row">
            <span className="panel-note" style={{ minWidth: 110 }}>
              Tenant
            </span>
            <span className="mono">{principal?.tenant || "default"}</span>
          </div>
          <div className="row">
            <span className="panel-note" style={{ minWidth: 110 }}>
              Roles
            </span>
            <div className="row" style={{ gap: 5 }}>
              {(principal?.roles || []).map((role) => (
                <Badge key={role} tone="neutral">
                  {role}
                </Badge>
              ))}
            </div>
          </div>
          <div className="row">
            <span className="panel-note" style={{ minWidth: 110 }}>
              Session mode
            </span>
            <span className="mono">{session?.mode || "—"}</span>
          </div>
        </div>
      </Panel>

      <Panel title="Deployment">
        <div className="stack">
          <div className="row">
            <span className="panel-note" style={{ minWidth: 110 }}>
              Version
            </span>
            <span className="mono">{status?.version || "—"}</span>
          </div>
          <div className="row">
            <span className="panel-note" style={{ minWidth: 110 }}>
              Instance
            </span>
            <span className="mono">{status?.instance_name || "—"}</span>
          </div>
          <div className="row">
            <span className="panel-note" style={{ minWidth: 110 }}>
              Tenant isolation
            </span>
            <span className="mono">{status?.tenant_isolation || "—"}</span>
          </div>
          <div className="row">
            <span className="panel-note" style={{ minWidth: 110 }}>
              Tenants
            </span>
            <span className="num">{status?.tenant_count ?? "—"}</span>
          </div>
        </div>
      </Panel>
    </>
  );
}
