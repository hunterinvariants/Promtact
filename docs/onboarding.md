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

### A1. Point the CLI at the deployment

```bash
export PROMTACT_ADMIN_TOKEN="$(cat /path/to/admin-token)"
export PROMTACT_PUBLIC_URL="https://app.promtact.example"
```

The public URL is what the customer will type. Without it the CLI prints the
address it was called on, which is usually loopback and useless to hand over.

### A2. Create the tenant

```bash
promtactl tenant create --name acme --display "Acme GmbH"
```

This creates the tenant and its first administrator in one step, and prints the
console address, the account name and the key. **The key is shown once and
cannot be recovered** — the command says so, and means it.

The tenant slug becomes part of every record and cannot be changed afterwards.
Use something short and permanent: a company slug, not a project name.

### A3. Create the agent account

```bash
promtactl tenant add-agent --tenant acme
```

Prints the endpoint credential. It may submit telemetry and nothing else: it
cannot read alerts, approve actions, or provision. That matters because this key
sits unattended on a customer machine — if that machine is compromised, the
blast radius is "someone can submit telemetry", not "someone owns the tenant".

### A4. If something needs checking or a key is lost

```bash
promtactl tenant list
promtactl tenant new-key --tenant acme --user acme-agent
```

Issuing a new key does not revoke the old ones. Revoke them separately if the
old key may be in the wrong hands.

### A5. Hand over

Send the customer:

- the console address,
- the person's account name and key,
- the agent's key, by a **different channel** than the person's key,
- the checklist below.

Sending both keys in one message means one intercepted message is the whole
tenant.

### A6. Confirm it arrived

After the customer has finished, check that the tenant is actually reporting:

```bash
promtactl tenant list
curl -s "$PROMTACT_URL/api/admin/tenants/acme/usage"   -H "Authorization: Bearer $PROMTACT_ADMIN_TOKEN"
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

Restart the machine, wait two minutes, then run
`Get-ScheduledTask -TaskName PromtactAgent` again and refresh the console.

This step exists because a service that only works until the next reboot is the
most common way monitoring quietly stops. It takes three minutes now, and it is
the difference between believing it works and knowing.

### ☐ 8. Note how to stop it

```powershell
Stop-ScheduledTask -TaskName PromtactAgent                          # pause
Unregister-ScheduledTask -TaskName PromtactAgent -Confirm:$false    # remove entirely
```

Removing the task stops all collection immediately. Records already sent stay
until the retention window expires.

---

### If something is wrong

| What you see | What it usually means |
| --- | --- |
| `unauthorized` or 401 | The key is wrong, expired, or has a trailing space |
| Connection timed out | Outbound HTTPS to the Promtact address is blocked |
| Task shows Ready, never Running | Run the same arguments by hand; the installer prints them |
| Console shows nothing after 10 minutes | The service is running but the log is empty — try `-LogName Security` |

Security problems, including anything about this agent: see
[SECURITY.md](../SECURITY.md).
