/**
 * Detection-regression alert receiver.
 *
 * Runs on Cloudflare's edge rather than on the monitored host: the alert reports
 * that something on that host is broken, so a receiver living there would die
 * with it.
 *
 * It authenticates the sender, formats the alert for humans, and fans it out to
 * whichever destinations are configured. Everything is optional except the
 * shared secret, so an unconfigured Worker still returns 204 and logs — visible
 * with `wrangler tail`.
 */

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);

    // The witness lives here rather than on the monitored host for the same
    // reason the alert receiver does, only more so: its whole purpose is to hold
    // a record the host's operator cannot rewrite.
    if (url.pathname === "/anchor" || url.pathname === "/anchor/pubkey") {
      return handleAnchor(request, env, url);
    }

    if (request.method !== "POST") {
      return new Response("POST only", { status: 405 });
    }

    // A public endpoint that pages a human is a denial-of-service target, so the
    // shared secret is mandatory rather than optional.
    if (!env.ALERT_SHARED_SECRET) {
      console.error("ALERT_SHARED_SECRET is not configured; refusing to accept alerts");
      return new Response("receiver not configured", { status: 503 });
    }
    const auth = request.headers.get("Authorization") || "";
    const presented = auth.startsWith("Bearer ") ? auth.slice(7) : "";
    if (!timingSafeEqual(presented, env.ALERT_SHARED_SECRET)) {
      return new Response("unauthorized", { status: 401 });
    }

    let alert;
    try {
      alert = await request.json();
    } catch {
      return new Response("invalid json", { status: 400 });
    }

    const text = formatAlert(alert);
    console.log(text);
    notify(env, ctx, text, "Promtact detection regression");

    return new Response(null, { status: 204 });
  },

  /**
   * Availability check, run on a schedule.
   *
   * It exists because everything else about this deployment is checked from
   * inside it. The tunnel was once down for fifteen hours while every local
   * probe reported health, because a service bound to loopback answers happily
   * whether or not the world can reach it. This runs at Cloudflare's edge, so
   * it sees what a customer sees.
   */
  async scheduled(event, env, ctx) {
    const target = env.MONITOR_URL;
    if (!target) {
      console.log("MONITOR_URL is not set; nothing to check");
      return;
    }
    if (!env.ANCHORS) {
      console.error("witness storage not bound; cannot track outage state");
      return;
    }
    ctx.waitUntil(runMonitor(env, ctx, target));
  },
};

/**
 * Fan out to whatever is configured. Failures are logged, never propagated: a
 * dead downstream must not make the caller believe the alert was rejected.
 */
function notify(env, ctx, text, title) {
  const deliveries = [];
  if (env.NTFY_TOPIC) {
    deliveries.push(
      post(`https://ntfy.sh/${env.NTFY_TOPIC}`, text, {
        Title: title,
        Priority: "high",
        Tags: "rotating_light",
      })
    );
  }
  if (env.DISCORD_WEBHOOK_URL) {
    deliveries.push(
      postJSON(env.DISCORD_WEBHOOK_URL, { content: text.slice(0, 1900) })
    );
  }
  if (env.SLACK_WEBHOOK_URL) {
    deliveries.push(postJSON(env.SLACK_WEBHOOK_URL, { text }));
  }
  ctx.waitUntil(Promise.allSettled(deliveries));
}

const MONITOR_KEY = "monitor:state";
const MONITOR_FAILURES_BEFORE_ALERT = 2;

async function runMonitor(env, ctx, target) {
  const previous = (await env.ANCHORS.get(MONITOR_KEY, { type: "json" })) || {
    failures: 0,
    alerted: false,
  };

  let healthy = false;
  let detail = "";
  try {
    const response = await fetch(target, {
      method: "GET",
      redirect: "manual",
      cf: { cacheTtl: 0 },
    });
    // 5xx includes Cloudflare's own 530, which is what a dead tunnel returns.
    healthy = response.status >= 200 && response.status < 400;
    detail = `HTTP ${response.status}`;
  } catch (err) {
    detail = String(err);
  }

  if (healthy) {
    if (previous.alerted) {
      notify(env, ctx, `Promtact is reachable again: ${target} (${detail})`,
        "Promtact recovered");
    }
    await env.ANCHORS.put(MONITOR_KEY, JSON.stringify({
      failures: 0,
      alerted: false,
      last_ok: new Date().toISOString(),
    }));
    return;
  }

  const failures = previous.failures + 1;

  // Two consecutive failures before paging anyone. A single edge hiccup is not
  // an outage, and an alarm that cries wolf is one that gets muted — which
  // would leave the deployment less monitored than it is now.
  const shouldAlert = failures >= MONITOR_FAILURES_BEFORE_ALERT && !previous.alerted;
  if (shouldAlert) {
    notify(env, ctx,
      `Promtact is unreachable from the internet: ${target} (${detail}, ${failures} consecutive checks)`,
      "Promtact unreachable");
  }

  await env.ANCHORS.put(MONITOR_KEY, JSON.stringify({
    failures,
    alerted: previous.alerted || shouldAlert,
    last_failure: new Date().toISOString(),
    last_detail: detail,
  }));
}

function formatAlert(alert) {
  const host = alert.host || "unknown host";
  const when = alert.time || new Date().toISOString();

  if (alert.type === "detection_regression") {
    const failed = Array.isArray(alert.failed) ? alert.failed : [];
    const lines = failed.map(
      (f) => `  - ${f.technique || "?"} ${f.name || ""}: expected ${f.want}, observed ${f.got}`
    );
    return [
      `Detection regression on ${host}`,
      `${alert.passed}/${alert.total} held (${alert.missed} missed, ${alert.false_positives} false positives)`,
      ...lines,
      when,
    ].join("\n");
  }

  return [
    `Detection validation did not complete on ${host}`,
    alert.summary || "no summary provided",
    when,
  ].join("\n");
}

async function post(url, body, headers = {}) {
  return fetch(url, { method: "POST", body, headers }).catch((err) =>
    console.error(`delivery to ${new URL(url).host} failed: ${err}`)
  );
}

async function postJSON(url, payload) {
  return fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  }).catch((err) => console.error(`delivery to ${new URL(url).host} failed: ${err}`));
}

/** Constant-time compare so the secret cannot be recovered by timing. */
function timingSafeEqual(a, b) {
  if (typeof a !== "string" || typeof b !== "string" || a.length !== b.length) {
    return false;
  }
  let diff = 0;
  for (let i = 0; i < a.length; i++) {
    diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  }
  return diff === 0;
}

/**
 * Audit-chain witness.
 *
 * The service publishes the head of its tamper-evident audit chain here. The
 * value of doing so is entirely in the refusals: this endpoint will not accept a
 * chain that got shorter, and will not accept a different head for an index it
 * has already recorded. A host operator can rewrite local history and recompute
 * the local anchor over it — they cannot make this store agree.
 *
 * Storage is Cloudflare KV, which is eventually consistent. With a single
 * publisher on a multi-minute interval that is not a practical concern, but it
 * does mean this is a witness, not a distributed ledger: two publishers racing
 * could both be accepted.
 */
async function handleAnchor(request, env, url) {
  const secret = env.ANCHOR_SHARED_SECRET || env.ALERT_SHARED_SECRET;
  if (!secret) {
    return new Response("witness not configured", { status: 503 });
  }
  if (!env.ANCHORS) {
    return new Response("witness storage not bound", { status: 503 });
  }

  // The verification key is public by definition, and an auditor holding only a
  // database copy needs it without also being given the anchoring secret.
  // Gating it behind the shared secret would mean handing out a credential that
  // can also write anchors, just to let somebody read.
  if (url.pathname === "/anchor/pubkey") {
    if (request.method !== "GET") {
      return new Response("GET only", { status: 405 });
    }
    const publicKey = await witnessPublicKey(env);
    if (!publicKey) {
      return json({ error: "this witness has no signing key configured" }, 503);
    }
    return json({
      key_id: env.WITNESS_KEY_ID || "w1",
      public_key: publicKey,
      algorithm: "ECDSA-P256-SHA256",
    }, 200);
  }

  const auth = request.headers.get("Authorization") || "";
  const presented = auth.startsWith("Bearer ") ? auth.slice(7) : "";
  if (!timingSafeEqual(presented, secret)) {
    return new Response("unauthorized", { status: 401 });
  }

  if (request.method === "GET") {
    // An auditor can ask for the latest witnessed state, or for a specific
    // index, which is what makes an old claim checkable rather than trusted.
    const wanted = url.searchParams.get("index");
    const key = wanted === null ? "latest" : `idx:${Number(wanted)}`;
    const stored = await env.ANCHORS.get(key, { type: "json" });
    if (!stored) {
      return new Response(JSON.stringify({ error: "no anchor recorded" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      });
    }
    return json(stored, 200);
  }

  if (request.method !== "POST") {
    return new Response("GET or POST", { status: 405 });
  }

  let submitted;
  try {
    submitted = await request.json();
  } catch {
    return new Response("invalid json", { status: 400 });
  }

  const index = Number(submitted.chain_index);
  const head = String(submitted.head || "");
  if (!Number.isInteger(index) || index < 0) {
    return json({ error: "chain_index must be a non-negative integer" }, 400);
  }

  const latest = await env.ANCHORS.get("latest", { type: "json" });

  // A chain that got shorter is the case a local anchor cannot detect at all:
  // truncated history re-anchors against itself perfectly well.
  if (latest && index < latest.chain_index) {
    return json({
      error: "the submitted chain is shorter than the witnessed chain",
      witnessed_index: latest.chain_index,
      submitted_index: index,
    }, 409);
  }

  // The same index with a different head means records were rewritten in place.
  const existing = await env.ANCHORS.get(`idx:${index}`, { type: "json" });
  if (existing && existing.head !== head) {
    return json({
      error: "this index was already witnessed with a different head",
      chain_index: index,
      witnessed_head: existing.head,
      submitted_head: head,
    }, 409);
  }

  const record = {
    chain_index: index,
    head,
    valid: Boolean(submitted.valid),
    witnessed_at: new Date().toISOString(),
    reported_at: String(submitted.at || ""),
  };

  // Sign what was accepted, so the gateway can store proof of what a third
  // party saw and when.
  //
  // Without this, checking an old claim means asking this Worker and trusting
  // that it answers honestly and still exists. With it, anyone holding the
  // public key can verify the statement offline - including an auditor handed
  // nothing but a database copy, and including a customer who has stopped
  // trusting the vendor. A receipt that cannot be produced for a range is then
  // positive evidence that the range was never witnessed.
  const signature = await signRecord(env, record);
  if (signature) {
    record.signature = signature;
    record.key_id = env.WITNESS_KEY_ID || "w1";
  }

  // The per-index record is written first. If the second write fails, the
  // witness has the stricter of the two states rather than a forgotten index.
  if (!existing) {
    await env.ANCHORS.put(`idx:${index}`, JSON.stringify(record));
  }
  if (!latest || index >= latest.chain_index) {
    await env.ANCHORS.put("latest", JSON.stringify(record));
  }

  console.log(`anchor accepted index=${index} head=${head.slice(0, 12)}`);
  return json(record, 200);
}

function json(body, status) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/**
 * Receipt signing.
 *
 * ECDSA on P-256 rather than Ed25519: both are fine cryptographically, but
 * P-256 is guaranteed present in the Workers Web Crypto implementation, while
 * Ed25519 support has moved around between runtime versions. A signature scheme
 * that works until the runtime is updated is worse than a slightly more
 * awkward one that always works.
 *
 * The awkwardness is that Web Crypto emits the signature as raw r||s while Go
 * verifies ASN.1 by default; the Go side splits the 64 bytes, which is all the
 * conversion amounts to.
 */

const WITNESS_DOMAIN = "promtact-witness-v1";

/**
 * The exact string that gets signed. It must match SigningString() in
 * internal/witness/receipt.go byte for byte - so it is built from the fields,
 * not from serialised JSON, because JSON key order and whitespace are not
 * guaranteed to survive a round trip and a verifier that a reformatting proxy
 * can break is not worth having.
 */
function signingString(record) {
  return [
    WITNESS_DOMAIN,
    String(record.chain_index),
    String(record.head || "").trim().toLowerCase(),
    record.witnessed_at,
  ].join("|");
}

let cachedSigningKey = null;

async function witnessSigningKey(env) {
  if (cachedSigningKey) return cachedSigningKey;
  if (!env.WITNESS_SIGNING_JWK) return null;
  try {
    const jwk = JSON.parse(env.WITNESS_SIGNING_JWK);
    cachedSigningKey = await crypto.subtle.importKey(
      "jwk",
      jwk,
      { name: "ECDSA", namedCurve: "P-256" },
      false,
      ["sign"]
    );
    return cachedSigningKey;
  } catch (err) {
    console.error(`witness signing key could not be imported: ${err}`);
    return null;
  }
}

async function signRecord(env, record) {
  const key = await witnessSigningKey(env);
  if (!key) return null;
  try {
    const signature = await crypto.subtle.sign(
      { name: "ECDSA", hash: "SHA-256" },
      key,
      new TextEncoder().encode(signingString(record))
    );
    return base64(signature);
  } catch (err) {
    // A witness that cannot sign still witnesses. Refusing the anchor because
    // the signature failed would turn a signing misconfiguration into a loss of
    // the witnessing this endpoint existed for in the first place.
    console.error(`signing the anchor failed: ${err}`);
    return null;
  }
}

/**
 * The public half, derived from the private JWK rather than configured
 * separately - two settings that must agree is two settings that can disagree,
 * and a published key that does not match the signing key would make every
 * receipt look forged.
 */
async function witnessPublicKey(env) {
  if (!env.WITNESS_SIGNING_JWK) return null;
  try {
    const jwk = JSON.parse(env.WITNESS_SIGNING_JWK);
    const publicJWK = { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y, ext: true };
    const publicKey = await crypto.subtle.importKey(
      "jwk",
      publicJWK,
      { name: "ECDSA", namedCurve: "P-256" },
      true,
      ["verify"]
    );
    return base64(await crypto.subtle.exportKey("spki", publicKey));
  } catch (err) {
    console.error(`witness public key could not be derived: ${err}`);
    return null;
  }
}

function base64(buffer) {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
  return btoa(binary);
}
