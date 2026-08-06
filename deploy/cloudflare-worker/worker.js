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

    // Fan out to whatever is configured. Failures are logged, never propagated:
    // a dead downstream must not make the sender think the alert was rejected
    // and retry it forever.
    const deliveries = [];
    if (env.NTFY_TOPIC) {
      deliveries.push(
        post(`https://ntfy.sh/${env.NTFY_TOPIC}`, text, {
          Title: "Promtact detection regression",
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

    return new Response(null, { status: 204 });
  },
};

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
