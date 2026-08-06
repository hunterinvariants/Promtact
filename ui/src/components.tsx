import { ReactNode, useState } from "react";

export function Panel({
  title,
  note,
  actions,
  children,
  bodyless,
}: {
  title: string;
  note?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  bodyless?: boolean;
}) {
  return (
    <section className="panel">
      <div className="panel-head">
        <h2>{title}</h2>
        {note ? <span className="panel-note">{note}</span> : null}
        <div className="spacer" />
        {actions}
      </div>
      {bodyless ? children : <div className="panel-body">{children}</div>}
    </section>
  );
}

export function StatTile({ label, value, hint }: { label: string; value: ReactNode; hint?: string }) {
  return (
    <div className="stat">
      <div className="stat-label">{label}</div>
      <div className="stat-value num">{value}</div>
      {hint ? <div className="stat-hint">{hint}</div> : null}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

type Tone = "good" | "warning" | "serious" | "critical" | "neutral";

const TONE_ICON: Record<Tone, string> = {
  good: "✓",
  warning: "!",
  serious: "▲",
  critical: "✕",
  neutral: "•",
};

/** Status is always icon + label, never color alone. */
export function Badge({ tone, children }: { tone: Tone; children: ReactNode }) {
  return (
    <span className={`badge badge-${tone}`}>
      <span aria-hidden="true">{TONE_ICON[tone]}</span>
      {children}
    </span>
  );
}

export function severityTone(severity: string): Tone {
  switch ((severity || "").toLowerCase()) {
    case "critical":
      return "critical";
    case "high":
      return "serious";
    case "medium":
      return "warning";
    case "low":
      return "good";
    default:
      return "neutral";
  }
}

export function verdictTone(verdict: string): Tone {
  switch ((verdict || "").toLowerCase()) {
    case "deny":
      return "critical";
    case "require_approval":
      return "warning";
    case "allow":
      return "good";
    default:
      return "neutral";
  }
}

export type TrendPoint = { time: string; passed: number; total: number };

/**
 * Detection coverage over time — one series, so the title carries identity and
 * no legend box is needed. Hover exposes the exact run.
 */
export function CoverageTrend({ history }: { history: TrendPoint[] }) {
  const [hover, setHover] = useState<number | null>(null);
  if (!history || history.length < 2) {
    return <p className="panel-note">A trend appears once at least two validation runs have been recorded.</p>;
  }

  const width = 640;
  const height = 120;
  const pad = { top: 12, right: 14, bottom: 22, left: 34 };
  const plotW = width - pad.left - pad.right;
  const plotH = height - pad.top - pad.bottom;

  const ratio = (p: TrendPoint) => (p.total ? p.passed / p.total : 0);
  const x = (i: number) => pad.left + (history.length === 1 ? plotW / 2 : (i * plotW) / (history.length - 1));
  const y = (r: number) => pad.top + (1 - r) * plotH;

  const points = history.map((p, i) => `${x(i).toFixed(1)},${y(ratio(p)).toFixed(1)}`).join(" ");
  const last = history[history.length - 1];
  const allHeld = ratio(last) >= 1;
  const active = hover === null ? history.length - 1 : hover;
  const activePoint = history[active];

  return (
    <div className="trend">
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        style={{ maxWidth: width }}
        role="img"
        aria-label="Share of detections holding, per validation run"
        onMouseLeave={() => setHover(null)}
      >
        {[0, 0.5, 1].map((tick) => (
          <g key={tick}>
            <line
              x1={pad.left}
              x2={width - pad.right}
              y1={y(tick)}
              y2={y(tick)}
              stroke="var(--gridline)"
              strokeWidth="1"
            />
            <text x={pad.left - 7} y={y(tick) + 3.5} textAnchor="end" fontSize="9.5" fill="var(--text-muted)">
              {Math.round(tick * 100)}%
            </text>
          </g>
        ))}

        <polyline
          points={points}
          fill="none"
          stroke={allHeld ? "var(--status-good)" : "var(--status-critical)"}
          strokeWidth="2"
          strokeLinejoin="round"
          strokeLinecap="round"
        />

        {history.map((p, i) => (
          <circle
            key={i}
            cx={x(i)}
            cy={y(ratio(p))}
            r={i === active ? 4.5 : 3}
            fill={ratio(p) >= 1 ? "var(--status-good)" : "var(--status-critical)"}
            stroke="var(--surface-1)"
            strokeWidth="2"
          />
        ))}

        {history.map((p, i) => (
          <rect
            key={`hit-${i}`}
            x={x(i) - plotW / (history.length * 2) - 2}
            y={pad.top}
            width={plotW / history.length + 4}
            height={plotH}
            fill="transparent"
            onMouseEnter={() => setHover(i)}
          />
        ))}
      </svg>

      <div className="stack" style={{ gap: 4 }}>
        <div style={{ fontSize: 13, fontWeight: 600 }} className="num">
          {activePoint.passed}/{activePoint.total} held
        </div>
        <div className="panel-note num">{new Date(activePoint.time).toLocaleString()}</div>
        <div className="panel-note">
          {history.filter((p) => p.total && p.passed === p.total).length}/{history.length} runs fully green
        </div>
      </div>
    </div>
  );
}
