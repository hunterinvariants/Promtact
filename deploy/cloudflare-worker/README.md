# Detection-regression alert receiver

A Cloudflare Worker that receives the alert `promtact-validate-alert` sends when a
scheduled detection validation regresses.

**Why not host it next to Promtact?** The alert says something on that host is
broken. A receiver living on the same machine dies with it, exactly when it is
needed. Running at the edge keeps the alert path independent of the thing it
watches.

## Deploy

```bash
cd deploy/cloudflare-worker
npx wrangler login
npx wrangler secret put ALERT_SHARED_SECRET   # paste a long random value
npx wrangler deploy
```

Optionally route it at your own hostname by uncommenting the `routes` block in
`wrangler.toml` (for example `alerts.example.com`) and deploying again.

Add any delivery channels you want — each is optional, and the Worker logs
regardless (`npx wrangler tail`):

```bash
npx wrangler secret put NTFY_TOPIC           # free phone push, no account
npx wrangler secret put DISCORD_WEBHOOK_URL
npx wrangler secret put SLACK_WEBHOOK_URL
```

## Point Promtact at it

On the monitored host, in `/etc/promtact/validate.env`:

```bash
PROMTACT_VALIDATE_ALERT_URL=https://alerts.example.com
PROMTACT_VALIDATE_ALERT_TOKEN=<the same ALERT_SHARED_SECRET>
```

Verify without touching the real gateway, using a synthetic failing result:

```bash
cat >/tmp/val-fail.json <<'JSON'
{"total":1,"passed":0,"missed":1,"false_positives":0,
 "results":[{"name":"canary-touch","technique":"T1530","tactic":"Collection",
             "want":">=deny","got":"allow","pass":false}]}
JSON
set -a; . /etc/promtact/validate.env; set +a
PROMTACT_VALIDATE_RESULT_FILE=/tmp/val-fail.json /usr/local/sbin/promtact-validate-alert
rm -f /tmp/val-fail.json
```

Expect `-> HTTP 204` and the alert in your channel.

## Notes

The shared secret is required: an unauthenticated endpoint that pages a human is
a denial-of-service target. It is compared in constant time. Delivery failures
are logged rather than returned, so a dead downstream cannot make the sender
believe the alert was rejected and retry it indefinitely.
