// Plain-language explanations for the rules a customer actually sees.
//
// A rule identifier like `process.discovery.chain` belongs in a log, not in
// front of someone deciding whether to worry. Nor does restating the detection
// logic help: "matched discovery, credential-access or lateral-movement
// telemetry patterns" tells a reader what the code did, not what happened on
// their machine or what they should do about it.
//
// Each entry answers three questions in order — what happened, why it matters,
// what to do — and every one of them names the ordinary, innocent explanation
// too. An alert that only describes the bad case trains people to panic first
// and dismiss second, which is how real ones end up ignored.

export type Explanation = {
  summary: string;
  what: string;
  why: string;
  doThis: string;
};

export const RISK_SCALE =
  "Risk score runs 0–100 and is the sum of a machine's open alerts: roughly 40 for one " +
  "high finding, 60 or more for a critical one, and higher again when several stack up. " +
  "It is a sorting aid for deciding what to look at first, not a verdict.";

const RULES: Record<string, Explanation> = {
  "process.discovery.chain": {
    summary: "A command ran that is typically used to map out a machine or network.",
    what:
      "A process on this machine ran a command of the kind used to survey what else is " +
      "reachable — listing accounts, shares, sessions, network configuration or domain " +
      "details. Evidence below shows what was seen: `matched` is the exact term that " +
      "triggered this, and `command`, `process` and `account` are the record's own fields " +
      "where Windows supplied them. Where it did not, `observed` quotes the record text.",
    why:
      "On its own this is unremarkable: administrators and installers do it constantly. " +
      "It matters because it is also the first thing an intruder does after gaining a " +
      "foothold, and it usually happens minutes before anything worse. Seen together with " +
      "credential access or an unfamiliar outbound connection, it stops being routine.",
    doThis:
      "Read `command` in Evidence and decide one thing: was that command started by " +
      "software you run, or by a person? Backup agents, inventory tools, login scripts and " +
      "installers produce this constantly, and they run under a service or machine account " +
      "at regular times — that is the ordinary case, and you can close it. " +
      "It is worth a second look when `account` is a named user who was not working then, " +
      "when the same command has never run on this machine before, or when it ran outside " +
      "working hours. In that case copy the host name into the Events page and read the " +
      "ten minutes either side of the timestamp: a single survey command means little, " +
      "and a run of them followed by an outbound connection means a great deal. " +
      "Change nothing until you have looked — pulling the machine off the network destroys " +
      "the very record that would tell you whether it mattered.",
  },
  "agent.tool.unapproved": {
    summary: "An AI agent tried to call a tool that is not on the approved list.",
    what:
      "An agent asked the gateway to run a tool that no policy permits. The call was " +
      "refused before it reached the tool.",
    why:
      "This is the control working rather than a breach. It matters because it is either " +
      "a legitimate tool nobody has approved yet, or an agent being steered somewhere it " +
      "should not go — and those look identical from here.",
    doThis:
      "Check which tool was requested. If it belongs in the workflow, add it to the " +
      "approved list. If it does not, look at what the agent was asked to do just before.",
  },
  "agent.secret.exposure": {
    summary: "Something that looks like a credential appeared in an agent's context.",
    what:
      "Content passing through an agent matched the shape of a secret — an API key, token " +
      "or password.",
    why:
      "Anything in an agent's context can end up in a model prompt, a log, or a tool call " +
      "to a third party. A credential that goes in usually cannot be taken back out.",
    doThis:
      "Treat the credential as exposed and rotate it. Then find where it entered the " +
      "context, because it will happen again from the same place.",
  },
  "deception.canary.hit": {
    summary: "Something touched a file or token that exists only as a trap.",
    what:
      "A deception asset was accessed. Nothing legitimate reads it, because it exists for " +
      "no other purpose.",
    why:
      "This has almost no false positives. Unlike most detections it does not rest on a " +
      "pattern being suspicious — the object simply has no innocent reason to be touched.",
    doThis:
      "Treat as an intrusion until shown otherwise. Find the account and process that " +
      "reached it, and check what else they did.",
  },
  "network.egress.unknown": {
    summary: "Something connected out to a destination that is not on the approved list.",
    what: "An outbound connection was made to a destination no policy permits.",
    why:
      "Most outbound traffic is ordinary, which is why only unapproved destinations are " +
      "flagged. It matters because data leaving is the step that cannot be undone.",
    doThis:
      "Check the destination under Evidence. If it belongs to a service you use, add it to " +
      "the approved list. If you do not recognise it, look at what sent the traffic.",
  },
  "model.runtime.suspicious": {
    summary: "A model runtime behaved in a way that does not match normal inference.",
    what: "Activity around a local model process departed from what serving a model looks like.",
    why:
      "Model runtimes are attractive targets: they hold credentials, reach internal " +
      "services, and are rarely watched as closely as ordinary applications.",
    doThis: "Check what the process did under Evidence, and whether the runtime was updated recently.",
  },
};

const FALLBACK: Explanation = {
  summary: "",
  what: "",
  why: "",
  doThis:
    "Check the evidence below and the surrounding activity on the Events page. If this " +
    "rule keeps firing on something ordinary, it is a candidate for tuning.",
};

export function explainRule(ruleID: string): Explanation {
  return RULES[ruleID] || FALLBACK;
}
