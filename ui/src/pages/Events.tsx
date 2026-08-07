import { Fragment, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import { Empty, Panel } from "../components";

// The raw record, because a count with no page behind it is not evidence.
//
// When an alert says a command looked like discovery, the next question is
// always "show me" — and until this page existed the answer was to query the
// API by hand.

export default function Events() {
  const [events, setEvents] = useState<any[]>([]);
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState("all");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .events()
        .then((data) => {
          if (!cancelled) {
            setEvents(data || []);
            setError("");
          }
        })
        .catch((err) => !cancelled && setError(err.message));
    load();
    const handle = window.setInterval(load, 15000);
    return () => {
      cancelled = true;
      window.clearInterval(handle);
    };
  }, []);

  const kinds = useMemo(() => {
    const seen = new Set<string>();
    events.forEach((e) => e.kind && seen.add(e.kind));
    return Array.from(seen).sort();
  }, [events]);

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return events.filter((e) => {
      if (kind !== "all" && e.kind !== kind) return false;
      if (!needle) return true;
      // Search the fields someone would actually search: the host, who did it,
      // what ran, and the command itself.
      return [e.hostname, e.asset_id, e.actor, e.process, e.command, e.destination]
        .filter(Boolean)
        .some((field: string) => String(field).toLowerCase().includes(needle));
    });
  }, [events, kind, query]);

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}
      <Panel
        title="Events"
        note={`${visible.length} of ${events.length}`}
        bodyless
        actions={
          <div className="event-controls">
            <input
              className="select"
              type="search"
              placeholder="Search host, process, command…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="Search events"
            />
            <select className="select" value={kind} onChange={(e) => setKind(e.target.value)} aria-label="Filter by kind">
              <option value="all">All kinds</option>
              {kinds.map((k) => (
                <option key={k} value={k}>{k}</option>
              ))}
            </select>
          </div>
        }
      >
        {visible.length === 0 ? (
          <Empty>
            {events.length === 0
              ? "Nothing has been collected yet. If an agent is installed, give it a minute."
              : "No events match this search."}
          </Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>When</th>
                <th>Host</th>
                <th>What</th>
                <th>Kind</th>
              </tr>
            </thead>
            <tbody>
              {visible.slice(0, 200).map((event) => {
                const open = expanded === event.id;
                return (
                  <Fragment key={event.id}>
                    <tr
                      className="is-clickable"
                      onClick={() => setExpanded(open ? null : event.id)}
                    >
                      <td className="num panel-note">
                        {event.timestamp ? new Date(event.timestamp).toLocaleString() : "—"}
                      </td>
                      <td className="mono">{event.hostname || event.asset_id || "—"}</td>
                      <td>
                        <div style={{ fontWeight: 500 }}>{event.process || event.tool_name || event.kind}</div>
                        {event.command ? (
                          <div className="panel-note mono event-command">{event.command}</div>
                        ) : null}
                      </td>
                      <td className="panel-note">{event.kind || "—"}</td>
                    </tr>
                    {open ? (
                      <tr>
                        <td colSpan={4} className="event-detail">
                          <dl>
                            {[
                              ["Time", event.timestamp ? new Date(event.timestamp).toLocaleString() : ""],
                              ["Host", event.hostname],
                              ["Asset", event.asset_id],
                              ["Account", event.actor],
                              ["Process", event.process],
                              ["Command", event.command],
                              ["Destination", event.destination],
                              ["Source address", event.source_ip],
                              ["Tool", event.tool_name],
                              ["Signal", event.signal],
                              ["Labels", (event.labels || []).join(", ")],
                            ]
                              .filter(([, value]) => value)
                              .map(([label, value]) => (
                                <div key={label as string}>
                                  <dt>{label}</dt>
                                  <dd className="mono">{value as string}</dd>
                                </div>
                              ))}
                          </dl>
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        )}
      </Panel>
      {visible.length > 200 ? (
        <p className="panel-note">
          Showing the first 200. Narrow the search to see the rest.
        </p>
      ) : null}
    </>
  );
}
