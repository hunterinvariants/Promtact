# promtact.com

Static pages: what the product is, how it is secured, what it does with data.
No build step, no JavaScript, nothing fetched from another host — the content
security policy in `_headers` forbids scripts entirely, which is easy to promise
when there are none.

The security and privacy pages are summaries. The authoritative versions live in
`docs/` beside the code, so they change when the code changes rather than when
someone remembers.

## Deploying

Cloudflare Pages, pointed at this directory:

```bash
npx wrangler pages deploy site --project-name promtact-site
```

Then attach `promtact.com` and `www.promtact.com` in the Pages project. The
`app.` subdomain stays on the tunnel and is unaffected.

## Keeping it honest

If a control named on `/security/` stops being true, the page is wrong and a
reader will find out. Both pages carry a review date; when the underlying
document in `docs/` changes, update the date here too.
