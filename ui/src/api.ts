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

// What to do when the session is gone.
//
// Four pages poll on a ten-second timer. When the session expired they kept
// polling anyway, forever: on the live deployment one forgotten browser tab
// produced 2432 rejected requests, and because the server recorded each one,
// more than two thirds of the tamper-evident audit chain became this console
// failing to log in.
//
// Stopping belongs here rather than in each page. A page that polls is not the
// place that knows the session ended, and adding the check to four call sites
// means the fifth one will not have it.
let onUnauthorized: (() => void) | null = null;

export function setUnauthorizedHandler(handler: (() => void) | null) {
  onUnauthorized = handler;
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });

  // A rejected sign-in is the user mistyping a key, not an expired session, so
  // it must not tear down the session it is trying to create.
  const isSignIn = path === "/api/session" && (options.method || "GET").toUpperCase() === "POST";
  if (response.status === 401 && !isSignIn && onUnauthorized) {
    onUnauthorized();
  }

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
  actions: () => request<any[]>("/api/responses"),
  auditChain: () => request<any>("/api/audit/chain"),
  auditWitness: () => request<any>("/api/audit/witness"),
  report: (from: string, to: string) =>
    request<any>(`/api/report?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`),
  policy: () => request<any>("/api/policy"),
  registerAgent: (agentID: string) =>
    request<any>("/api/policy/agents", { method: "POST", body: JSON.stringify({ agent_id: agentID }) }),
  removeAgent: (agentID: string) =>
    request<any>(`/api/policy/agents/${encodeURIComponent(agentID)}`, { method: "DELETE" }),
  updatePolicy: (body: { approved_tools: string[]; approved_egress_hosts: string[] }) =>
    request<any>("/api/policy", { method: "PUT", body: JSON.stringify(body) }),
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
  // The reason is required by the server, and it is the point: a refusal with
  // none recorded answers "was this allowed" but not "why not".
  declineAction: (actionID: string, reason: string, declinedBy?: string) =>
    request<any>("/api/responses/decline", {
      method: "POST",
      body: JSON.stringify({ action_id: actionID, reason, declined_by: declinedBy }),
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
