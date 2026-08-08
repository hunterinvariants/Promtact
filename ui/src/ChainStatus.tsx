import { useEffect, useState } from "react";
import { api } from "./api";

// The integrity of the record, in the header, on every page.
//
// This is what distinguishes the product from a log viewer, and it was reachable
// only by navigating to a page and reading a paragraph. A reader who has to go
// looking for the guarantee does not believe it is a guarantee.
//
// It reports four states rather than two. "Verified" and "broken" are the
// obvious pair; the two in between matter more in practice, because a chain
// with no witness proves far less than one with a witness agreeing, and saying
// so here is the difference between a claim and a boast.

type State = {
  label: string;
  tone: "verified" | "internal" | "broken" | "unknown";
  title: string;
};

export default function ChainStatus() {
  const [state, setState] = useState<State>({
    label: "Chain …",
    tone: "unknown",
    title: "Checking the audit chain.",
  });

  useEffect(() => {
    let cancelled = false;

    const check = async () => {
      try {
        // The witness is allowed to be unreadable - some accounts cannot see
        // it - without that turning the whole badge into "unknown", which
        // would say the chain is unverifiable when it is merely unwitnessed
        // to this reader.
        const [chainResult, witnessResult] = await Promise.allSettled([
          api.auditChain(),
          api.auditWitness(),
        ]);
        if (cancelled) return;
        if (chainResult.status !== "fulfilled") throw chainResult.reason;
        const chain = chainResult.value;
        const witness = witnessResult.status === "fulfilled" ? witnessResult.value : null;

        if (!chain?.valid) {
          setState({
            label: "Chain BROKEN",
            tone: "broken",
            title:
              "A record has been changed or removed, or records were deleted by a retention policy. Nothing else on this console should be relied on until that is explained.",
          });
          return;
        }
        if (witness?.diverged) {
          setState({
            label: "Witness DIVERGED",
            tone: "broken",
            title:
              "An external witness holds a version of this record that this server can no longer produce. Something here was removed or rewritten.",
          });
          return;
        }
        if (witness?.configured && witness?.agrees) {
          setState({
            label: `Chain intact · witness at ${witness.witnessed_index}`,
            tone: "verified",
            title:
              "Every record still hashes to the one before it, and an external witness outside this server agrees. An operator with full access here cannot remove a decision without the witness refusing the result.",
          });
          return;
        }
        setState({
          label: `Chain intact · ${chain.linked} records`,
          tone: "internal",
          title:
            "Every record still hashes to the one before it. No external witness is agreeing yet, so this detects accidental corruption only: anyone able to write to the database could recompute every hash.",
        });
      } catch {
        if (!cancelled) {
          setState({
            label: "Chain unknown",
            tone: "unknown",
            title: "The chain could not be read. This is not the same as the chain being wrong.",
          });
        }
      }
    };

    check();
    const handle = window.setInterval(check, 30000);
    return () => {
      cancelled = true;
      window.clearInterval(handle);
    };
  }, []);

  return (
    <div className={`chain-status chain-${state.tone}`} title={state.title}>
      <span className="chain-dot" />
      <span className="chain-label">{state.label}</span>
    </div>
  );
}
