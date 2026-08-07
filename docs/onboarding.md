# Onboarding a new customer

From nothing to a Windows endpoint reporting into its own tenant, in about
twenty minutes.

The document is in two halves. The first is what the provider does and never
leaves the provider. The second is the checklist the customer receives — it can
be sent as-is, and it says plainly what leaves their machine, because that is the
first thing a competent customer asks.

---

## Part A — Provider

Everything here runs against the Promtact deployment with an admin credential.

### A1. Create the tenant

**Read the response.** Creating a tenant also creates its first administrator —
named `<tenant>-admin` unless you pass `admin_name` — and returns that account's
API key. That key is the customer's console login, and it is shown exactly once.

```bash
curl -s -X POST "$URL/api/admin/tenants"   -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json"   -d '{"tenant":"acme","display_name":"Acme GmbH"}'
```

Discarding this output leaves an account whose key nobody knows, and creating it
again fails on the unique name. Recovering means issuing a fresh key for the
existing user — extra work for no reason.

The tenant name becomes part of every record and cannot be changed afterwards.
Use something short and stable: a company slug, not a project name.

### A2. Create the agent account

The tenant already has its person. It still needs a machine identity, and the
two must not be the same one.

```bash
curl -s -X POST "$URL/api/admin/tenants/acme/users"   -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json"   -d '{"name":"acme-agent","roles":["ingestor"]}'
```

**This response also carries an API key in clear text, once.** Keys are stored
only as hashes and cannot be recovered.

Why a second account: the agent's key sits on an endpoint, unattended, where a
credential should reach exactly as far as its job. An `ingestor` cannot read
another machine's alerts, cannot approve anything, and cannot provision. If that
endpoint is compromised, the blast radius is "someone can submit telemetry", not
"someone owns the tenant".

Account names are unique across the whole deployment rather than per tenant:
login resolves a name to exactly one account, and two `admin` users in different
tenants would be ambiguous. Prefix them with the tenant, as `acme-admin` does.

### A2b. If a key is lost

Issue a replacement for the existing user rather than recreating it:

```bash
curl -s "$URL/api/admin/tenants/acme/users" -H "Authorization: Bearer $ADMIN_TOKEN"

curl -s -X POST "$URL/api/admin/tenants/acme/keys"   -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json"   -d '{"user_id":"usr-...","name":"replacement"}'
```

### A3. Hand over

Send the customer:

- the console address,
- the person's account name and key,
- the agent's key, by a **different channel** than the person's key,
- the checklist below.

Sending both keys in one message means one intercepted message is the whole
tenant.

### A4. Confirm it arrived

After the customer has finished, check that the tenant is actually reporting:

```bash
curl -s "$URL/api/admin/tenants/acme/usage" -H "Authorization: Bearer $ADMIN_TOKEN"
```

An empty result an hour after installation means the agent is not sending, and
the customer will not necessarily notice — silence looks the same as quiet.

---

## Part B — Customer checklist

*Everything below can be sent to the customer unchanged.*

### What this does, before you install anything

The Promtact agent reads **one Windows event log** on this machine and forwards
its records to your Promtact tenant. It reads nothing else: no files, no
keystrokes, no screen, no network capture, no browsing history.

By default it reads the **System** log, which records service starts, driver
loads and shutdowns. If you were asked to use `Security` instead, that log also
records sign-ins and privilege use — more useful, and more sensitive. You choose
which.

What is transmitted: the event's timestamp, the machine name, the account name
in the record, the process or service it concerns, and the event's own text.
That text is written by Windows, not by us, and we do not filter it — assume it
can contain a user name or a file path.

Data is kept for **30 days** and then deleted. Details in
[data-protection.md](data-protection.md).

Nothing is installed that accepts inbound connections. The agent makes an
outbound HTTPS connection and nothing listens on your machine.

---

### ☐ 1. Check you have what you need

- Windows 10 or 11, with local administrator rights
- Outbound HTTPS to your Promtact address (no inbound rules, no VPN needed)
- The two items your provider sent: the **console key** and the **agent key**

### ☐ 2. Sign in to the console

Open your Promtact address in a browser. Sign in with the user name and console
key you were given.

You should see your own tenant name in the top left, and an empty overview. It
is empty because nothing has reported yet — that is the next step.

**If you see another company's name, stop and tell your provider.**

### ☐ 3. Put the agent in place

Copy `promtactl.exe` to:

```
C:\Program Files\Promtact\promtactl.exe
```

Create the folder if it is not there.

### ☐ 4. Try it once, in the foreground

Before installing anything permanent, check it works. Open **PowerShell as
Administrator** and run:

```powershell
& "C:\Program Files\Promtact\promtactl.exe" agent `
  --source windows-eventlog --log-name System `
  --url https://YOUR-PROMTACT-ADDRESS `
  --token YOUR-AGENT-KEY --once
```

`--once` reads what is there, sends it, and exits. It should report how many
records it sent.

**If it reports an authentication error**, the key is wrong or has a stray
space. **If it reports a connection error**, outbound HTTPS is being blocked.

### ☐ 5. Confirm it arrived

Refresh the console. Under **Assets** you should now see this machine by name,
and the **Events** count on the overview should no longer be zero.

**Do not continue until you have seen this.** Installing a service that silently
sends nothing is worse than not installing one, because it looks finished.

### ☐ 6. Install it as a service

So it keeps running after a reboot and after you log out:

```powershell
cd "C:\Program Files\Promtact"
.\install-agent-service.ps1 -Url https://YOUR-PROMTACT-ADDRESS -LogName System
```

It asks for the agent key and does not echo it. The key is written to
`C:\ProgramData\Promtact\agent.token`, readable only by SYSTEM and
Administrators — deliberately not passed on the command line, where any local
administrator could read it out of the service configuration.

Check it is running:

```powershell
Get-Service PromtactAgent
```

### ☐ 7. Prove it survives a restart

Restart the machine, wait two minutes, then run `Get-Service PromtactAgent`
again and refresh the console.

This step exists because a service that only works until the next reboot is the
most common way monitoring quietly stops. It takes three minutes now, and it is
the difference between believing it works and knowing.

### ☐ 8. Note how to stop it

```powershell
sc.exe stop PromtactAgent                     # pause
sc.exe stop PromtactAgent; sc.exe delete PromtactAgent   # remove entirely
```

Removing the service stops all collection immediately. Records already sent stay
until the retention window expires.

---

### If something is wrong

| What you see | What it usually means |
| --- | --- |
| `unauthorized` or 401 | The key is wrong, expired, or has a trailing space |
| Connection timed out | Outbound HTTPS to the Promtact address is blocked |
| Service starts, then stops | Check the token file exists and is readable by SYSTEM |
| Console shows nothing after 10 minutes | The service is running but the log is empty — try `-LogName Security` |

Security problems, including anything about this agent: see
[SECURITY.md](../SECURITY.md).
