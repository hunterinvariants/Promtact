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

## Signed witness receipts

The witness records what it saw. Signing that record is what lets somebody check
it later without asking the witness anything - including an auditor holding a
database copy and no network access, and including a customer who has stopped
taking the vendor's word for it.

Generate a P-256 key and install it as a secret. The private half never leaves
Cloudflare; the public half is served at `/anchor/pubkey` and is meant to be
handed out.

```bash
node -e "(async()=>{const k=await crypto.subtle.generateKey({name:'ECDSA',namedCurve:'P-256'},true,['sign','verify']);console.log(JSON.stringify(await crypto.subtle.exportKey('jwk',k.privateKey)))})()"
```

```bash
wrangler secret put WITNESS_SIGNING_JWK
```

Then fetch the public key once and keep it somewhere that is not the gateway:

```bash
curl -s https://alerts.example.com/anchor/pubkey | tee witness.pub.json
```

Checking the receipts is then an offline operation:

```bash
promtactl audit receipts --all --public-key @witness.pub
```

Two things worth being precise about, because both get overstated:

- A receipt proves a third party saw this head at this time. It does not prove
  the contents of any record, and it does not stop an operator deleting one.
  What it changes is that deleting one leaves a range with no receipt, and that
  is evidence rather than an absence.
- While the vendor operates the only witness, the vendor is still trusted. The
  arrangement that removes that is a second witness the customer runs. The
  signing scheme here is the same either way, which is the point: an OEM buyer
  can hold their own key without any code changing.

**Version skew is normal and safe.** A witness without a signing key still
witnesses; receipts are then stored unsigned and reported as unsigned rather
than as valid or as failures.

## Availability monitoring

The Worker also checks that the deployment answers from the internet, on a
five-minute schedule. This is not redundant with the checks on the host: a
service bound to loopback answers happily whether or not anything can reach it,
and this deployment once spent fifteen hours unreachable while every local probe
reported health.

It pages after **two** consecutive failures, not one — a single edge hiccup is
not an outage, and an alarm that cries wolf gets muted. Recovery is announced
too, so an outage has a visible end rather than trailing off.

Configure the target in `wrangler.toml`:

```toml
[triggers]
crons = ["*/5 * * * *"]

[vars]
MONITOR_URL = "https://app.promtact.com/"
```

State lives in the same KV namespace as the audit witness, so the alert fires
once per outage rather than every five minutes.
