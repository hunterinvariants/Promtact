import { useEffect, useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel } from "../components";

export default function Tenants() {
  const [tenants, setTenants] = useState<any[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({ tenant: "", display_name: "", admin_name: "" });
  const [issued, setIssued] = useState<any>(null);
  const [selected, setSelected] = useState<string>("");
  const [detail, setDetail] = useState<{ users: any[]; keys: any[] } | null>(null);

  const load = async () => {
    try {
      setTenants((await api.adminTenants()) || []);
      setError("");
    } catch (err: any) {
      setError(err.message);
    }
  };

  useEffect(() => {
    load();
  }, []);

  useEffect(() => {
    if (!selected) {
      setDetail(null);
      return;
    }
    Promise.all([api.tenantUsers(selected), api.tenantKeys(selected)])
      .then(([users, keys]) => setDetail({ users: users || [], keys: keys || [] }))
      .catch((err) => setError(err.message));
  }, [selected, issued]);

  const createTenant = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!form.tenant.trim()) return;
    setBusy(true);
    try {
      const result = await api.createTenant({
        tenant: form.tenant.trim(),
        display_name: form.display_name.trim(),
        admin_name: form.admin_name.trim() || undefined,
      });
      setIssued(result);
      setForm({ tenant: "", display_name: "", admin_name: "" });
      await load();
      setError("");
    } catch (err: any) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const toggleStatus = async (tenant: string, status: string) => {
    try {
      await api.setTenantStatus(tenant, status === "active" ? "suspended" : "active");
      await load();
    } catch (err: any) {
      setError(err.message);
    }
  };

  const revoke = async (tenant: string, keyID: string) => {
    try {
      await api.revokeKey(tenant, keyID);
      setDetail(null);
      setSelected("");
      setSelected(tenant);
    } catch (err: any) {
      setError(err.message);
    }
  };

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}

      {issued ? (
        <Panel
          title="New customer provisioned"
          actions={
            <button className="btn" onClick={() => setIssued(null)}>
              Dismiss
            </button>
          }
        >
          <div className="stack">
            <div className="callout">
              <span aria-hidden="true">!</span>
              <span>
                Copy this API key now — it is shown once and cannot be recovered. Hand it to the customer over a secure
                channel.
              </span>
            </div>
            <div className="mono" style={{ wordBreak: "break-all", fontSize: 13 }}>
              {issued.api_key}
            </div>
            <div className="row">
              <button className="btn" onClick={() => navigator.clipboard?.writeText(issued.api_key)}>
                Copy key
              </button>
              <span className="panel-note">
                tenant <strong>{issued.tenant?.tenant}</strong> · admin <strong>{issued.user?.name}</strong>
              </span>
            </div>
          </div>
        </Panel>
      ) : null}

      <Panel title="Provision a customer" note="creates the tenant, its first admin and an API key">
        <form onSubmit={createTenant} className="row" style={{ alignItems: "flex-end" }}>
          <div className="field" style={{ marginTop: 0 }}>
            <label htmlFor="tenant-id">Tenant id</label>
            <input
              id="tenant-id"
              className="input"
              value={form.tenant}
              onChange={(e) => setForm({ ...form, tenant: e.target.value })}
              placeholder="acme"
              spellCheck={false}
              required
            />
          </div>
          <div className="field" style={{ marginTop: 0 }}>
            <label htmlFor="tenant-name">Display name</label>
            <input
              id="tenant-name"
              className="input"
              value={form.display_name}
              onChange={(e) => setForm({ ...form, display_name: e.target.value })}
              placeholder="Acme Corp"
            />
          </div>
          <div className="field" style={{ marginTop: 0 }}>
            <label htmlFor="tenant-admin">Admin user</label>
            <input
              id="tenant-admin"
              className="input"
              value={form.admin_name}
              onChange={(e) => setForm({ ...form, admin_name: e.target.value })}
              placeholder="acme-admin"
              spellCheck={false}
            />
          </div>
          <button className="btn btn-primary" type="submit" disabled={busy}>
            {busy ? "Creating…" : "Create customer"}
          </button>
        </form>
      </Panel>

      <Panel title="Customers" note={`${tenants.length} tenants`} bodyless>
        {tenants.length === 0 ? (
          <Empty>No customers yet. Provision the first one above.</Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Status</th>
                <th>Tenant</th>
                <th>Plan</th>
                <th>Created</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {tenants.map((tenant) => (
                <tr key={tenant.tenant}>
                  <td>
                    <Badge tone={tenant.status === "active" ? "good" : "warning"}>{tenant.status}</Badge>
                  </td>
                  <td>
                    <div style={{ fontWeight: 500 }}>{tenant.display_name || tenant.tenant}</div>
                    <div className="panel-note mono">{tenant.tenant}</div>
                  </td>
                  <td>{tenant.plan}</td>
                  <td className="num panel-note">
                    {tenant.created_at ? new Date(tenant.created_at).toLocaleDateString() : "—"}
                  </td>
                  <td>
                    <div className="row" style={{ justifyContent: "flex-end", gap: 6 }}>
                      <button
                        className="btn"
                        onClick={() => setSelected(selected === tenant.tenant ? "" : tenant.tenant)}
                      >
                        {selected === tenant.tenant ? "Hide" : "Manage"}
                      </button>
                      <button className="btn" onClick={() => toggleStatus(tenant.tenant, tenant.status)}>
                        {tenant.status === "active" ? "Suspend" : "Activate"}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      {selected && detail ? (
        <Panel title={`Users and keys · ${selected}`} bodyless>
          <table>
            <thead>
              <tr>
                <th>User</th>
                <th>Roles</th>
                <th>Keys</th>
              </tr>
            </thead>
            <tbody>
              {detail.users.map((user) => (
                <tr key={user.id}>
                  <td>
                    <div style={{ fontWeight: 500 }}>{user.name}</div>
                    <div className="panel-note mono">{user.id}</div>
                  </td>
                  <td>
                    <div className="row" style={{ gap: 5 }}>
                      {(user.roles || []).map((role: string) => (
                        <Badge key={role} tone="neutral">
                          {role}
                        </Badge>
                      ))}
                    </div>
                  </td>
                  <td>
                    <div className="stack" style={{ gap: 6 }}>
                      {detail.keys
                        .filter((key) => key.user_id === user.id)
                        .map((key) => (
                          <div key={key.id} className="row" style={{ gap: 8 }}>
                            <Badge tone={key.revoked_at ? "critical" : "good"}>
                              {key.revoked_at ? "revoked" : "active"}
                            </Badge>
                            <span className="mono panel-note">{key.name || key.id}</span>
                            {key.revoked_at ? null : (
                              <button className="btn" onClick={() => revoke(selected, key.id)}>
                                Revoke
                              </button>
                            )}
                          </div>
                        ))}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      ) : null}
    </>
  );
}
