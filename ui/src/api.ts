// Thin API client for the Promtact console. The UI is served by the Go binary, so
// every call is same-origin and the session cookie is sent automatically.

export type Principal = {
  name: string;
  tenant?: string;
  roles?: string[];
};

export type Session = {
  authenticated: boolean;
  mode?: string;
  principal?: Principal;
  sso?: { oidc?: boolean; saml?: boolean };
};

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });

  const text = await response.text();
  let payload: any = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = text;
    }
  }
  if (!response.ok) {
    const message = (payload && payload.error) || `${response.status} ${response.statusText}`;
    throw new ApiError(message, response.status);
  }
  return payload as T;
}

export const api = {
  session: () => request<Session>("/api/session"),
  login: (username: string, token: string) =>
    request<Session>("/api/session", {
      method: "POST",
      body: JSON.stringify({ username, token }),
    }),
  logout: () => request<unknown>("/api/session", { method: "DELETE" }),

  status: () => request<any>("/api/status"),
  alerts: () => request<any[]>("/api/alerts"),
  assets: () => request<any[]>("/api/assets"),
  events: () => request<any[]>("/api/events"),
  actions: () => request<any[]>("/api/responses"),
  auditChain: () => request<any>("/api/audit/chain"),
  auditWitness: () => request<any>("/api/audit/witness"),
  demoDocuments: () => request<any[]>("/api/demo/documents"),
  demoOutbox: () => request<any[]>("/api/demo/outbox"),
  audit: () => request<any[]>("/api/audit"),
  runDemoAgent: (via: "direct" | "gateway") =>
    request<any>("/api/demo/agent-run", { method: "POST", body: JSON.stringify({ via }) }),
  approveAction: (actionID: string, approvedBy: string) =>
    request<any>("/api/responses/approve", {
      method: "POST",
      body: JSON.stringify({ action_id: actionID, approved_by: approvedBy }),
    }),
  validation: () => request<any>("/api/gateway/validation"),

  // Platform provisioning (admin on the default tenant only).
  adminTenants: () => request<any[]>("/api/admin/tenants"),
  createTenant: (body: { tenant: string; display_name?: string; plan?: string; admin_name?: string }) =>
    request<any>("/api/admin/tenants", { method: "POST", body: JSON.stringify(body) }),
  setTenantStatus: (tenant: string, status: string) =>
    request<any>(`/api/admin/tenants/${encodeURIComponent(tenant)}/status`, {
      method: "POST",
      body: JSON.stringify({ status }),
    }),
  tenantUsers: (tenant: string) => request<any[]>(`/api/admin/tenants/${encodeURIComponent(tenant)}/users`),
  tenantKeys: (tenant: string) => request<any[]>(`/api/admin/tenants/${encodeURIComponent(tenant)}/keys`),
  createTenantUser: (tenant: string, body: { name: string; roles: string[] }) =>
    request<any>(`/api/admin/tenants/${encodeURIComponent(tenant)}/users`, {
      method: "POST",
      body: JSON.stringify(body),
    }),
  revokeKey: (tenant: string, keyID: string) =>
    request<any>(`/api/admin/tenants/${encodeURIComponent(tenant)}/keys/${encodeURIComponent(keyID)}`, {
      method: "DELETE",
    }),
};

export function isPlatformAdmin(session: Session | null): boolean {
  const principal = session?.principal;
  if (!principal) return false;
  const roles = principal.roles || [];
  const tenant = (principal.tenant || "default").toLowerCase();
  return roles.includes("admin") && tenant === "default";
}
