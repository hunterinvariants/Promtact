package policy

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/hunterinvariants/promtact/internal/domain"
)

// Inspection of what a tool returns, which is where the attack actually lives.
//
// The gateway gates what an agent asks to do. That stops an agent from calling
// a tool it should not call, and it is worth having. It does nothing about the
// attack the category is named after.
//
// Indirect prompt injection does not touch the agent. It plants instructions in
// data the agent will later read — a web page, a ticket, a document, an MCP
// resource — and waits. The tool call that fetches it is unremarkable, because
// it *is* unremarkable: fetching a page is what the agent is for. The poison is
// in the answer, and until now the answer went back untouched.
//
// A word about what this can and cannot do, because the difference decides
// whether the product survives its first serious customer:
//
// Prompt injection cannot be reliably detected. Instructions are just text, the
// same text a legitimate document might contain, and any list of phrases is one
// rephrasing away from useless — the fifteen-phrase list this replaces caught
// "ignore previous" and sailed past "disregard the above". Anyone claiming
// reliable detection is selling a keyword list with better marketing.
//
// So detection is not the load-bearing part here. Provenance is. Content that
// came back from a tool is untrusted by origin, regardless of what it contains,
// and that fact is carried forward so the agent's next action is judged knowing
// it acted after reading untrusted material. Detection then adds confidence
// where a signal is genuinely hard to produce by accident — smuggled Unicode,
// hidden markup, an image URL with data pasted into it — and stays advisory
// where it is not.

// ToolResultDecision is the verdict on a tool's response.
type ToolResultDecision struct {
	Verdict domain.GatewayVerdict
	Reason  string
	Risk    domain.Severity
	// Findings name each signal that fired, so an operator sees why rather than
	// a score.
	Findings []string
	// Taint travels with the agent's session: the next call is assessed knowing
	// this content was read.
	Taint []string
	// Evidence quotes the offending span. A verdict on content nobody can see is
	// the same failure as an alert with an empty evidence block.
	Evidence map[string]string
}

// Withheld reports whether the body must not reach the agent.
func (d ToolResultDecision) Withheld() bool {
	return d.Verdict == domain.GatewayDeny
}

var (
	// Instruction-shaped overrides, matched as a shape rather than a phrase:
	// a cancelling verb, then optional filler, then a word meaning "what came
	// before". This is what catches "disregard the above", "forget all prior
	// instructions" and "ignore everything you were told earlier" with one rule.
	overridePattern = regexp.MustCompile(`(?i)\b(ignore|disregard|forget|override|discard|set aside)\b[^.!?\n]{0,40}\b(previous|prior|earlier|above|preceding|foregoing|all)\b`)

	// Text addressed to a model rather than to a reader. On its own this is
	// weak — documentation about AI says these things constantly — so it is
	// scored, not acted on alone.
	addressedPattern = regexp.MustCompile(`(?i)\b(you are|your (instructions|rules|system prompt|guidelines)|as an? (ai|assistant|language model)|new instructions?|system prompt)\b`)

	// A markdown image whose URL carries a query string is the standard
	// zero-click exfiltration: the agent renders it, the client fetches it, and
	// whatever was interpolated into the query arrives at the attacker. Almost
	// nothing legitimate needs this shape inside tool output.
	imageExfilPattern = regexp.MustCompile(`!\[[^\]]*\]\(\s*https?://[^)\s]+\?[^)\s]+\)`)

	// Markup that renders to nothing. Hidden text has no honest purpose in a
	// document a model is about to read.
	hiddenMarkupPattern = regexp.MustCompile(`(?i)(display\s*:\s*none|visibility\s*:\s*hidden|font-size\s*:\s*0|opacity\s*:\s*0|<!--)`)

	// A long unbroken base64-ish run, which is how instructions get past a
	// reader without getting past a decoder.
	encodedBlobPattern = regexp.MustCompile(`[A-Za-z0-9+/]{120,}={0,2}`)

	// A deception asset names itself, and the name is followed by the token
	// body. Requiring that body is what separates our own canary from a page
	// that merely uses the word "decoy" in a sentence.
	canaryPattern = regexp.MustCompile(`(?i)\b(canary|honeytoken|honey|decoy)[-_][A-Za-z0-9]{4,}\b`)

	// Credential shapes. Each is a value nobody writes by accident, which is
	// the property that makes it worth acting on. Vendor prefixes are listed
	// because they are unambiguous; the assignment form catches the rest.
	credentialPatterns = []struct {
		kind    string
		pattern *regexp.Regexp
	}{
		{"private key", regexp.MustCompile(`-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----`)},
		{"aws access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
		{"github token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)},
		{"openai key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
		{"slack token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
		{"json web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+`)},
		{"bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{20,}={0,2}`)},
		{"assigned secret", regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|token|password|passwd|pwd)\s*[:=]\s*["']?[A-Za-z0-9._~+/-]{12,}`)},
	}
)

// credentialShaped reports the kind of credential found and a redacted excerpt.
//
// The excerpt is redacted rather than quoted. An audit record is read by more
// people than the tool result was, and copying the secret into it in order to
// report the secret would be its own disclosure.
func credentialShaped(body string) (string, string) {
	for _, candidate := range credentialPatterns {
		if match := candidate.pattern.FindString(body); match != "" {
			return candidate.kind, redactSecret(match)
		}
	}
	return "", ""
}

func redactSecret(match string) string {
	trimmed := strings.TrimSpace(match)
	visible := 6
	if len(trimmed) <= visible {
		return strings.Repeat("•", len(trimmed))
	}
	return trimmed[:visible] + strings.Repeat("•", 8) + fmt.Sprintf(" (%d characters)", len(trimmed))
}

// InspectToolResult judges a tool's response before the agent sees it.
func (e *Engine) InspectToolResult(request domain.ToolCallRequest, body string) ToolResultDecision {
	decision := ToolResultDecision{
		Verdict:  domain.GatewayAllow,
		Risk:     domain.SeverityInfo,
		Evidence: map[string]string{},
	}
	if strings.TrimSpace(body) == "" {
		return decision
	}

	// Origin first, and unconditionally. This is the part that does not depend
	// on detecting anything: content arrived from outside, so the agent has now
	// read material it did not author and neither did its operator.
	origin := strings.TrimSpace(request.Destination)
	if origin == "" {
		origin = strings.TrimSpace(request.ToolName)
	}
	if origin != "" {
		decision.Taint = appendUniqueString(decision.Taint, "tool_result:"+normalizeToolOrigin(origin))
	}
	if request.Destination != "" && isExternalDestination(request.Destination) {
		decision.Taint = appendUniqueString(decision.Taint, "untrusted_origin:"+normalizeHost(request.Destination))
	}

	// Signals that are hard to produce by accident come first, because they are
	// the ones worth acting on rather than merely recording.
	if spans := smuggledRunes(body); len(spans) > 0 {
		decision.Findings = append(decision.Findings, "hidden_unicode")
		decision.Evidence["hidden_unicode"] = spans
		// The characters counted, and then what they say. A count proves
		// something was hidden; the decoded text is what makes an audience
		// understand why it mattered, and an analyst reading this later has the
		// same question. It is attacker-controlled text and is recorded as
		// evidence, never rendered as anything but text.
		if decoded := decodeSmuggledText(body); decoded != "" {
			decision.Evidence["hidden_text"] = truncate(decoded, 400)
		}
		decision.Verdict = domain.GatewayDeny
		decision.Reason = "tool result contains characters that are invisible to a reader but not to a model"
		decision.Risk = domain.SeverityCritical
	}

	if match := imageExfilPattern.FindString(body); match != "" {
		decision.Findings = append(decision.Findings, "image_exfiltration")
		decision.Evidence["image_exfiltration"] = excerptAround(body, match)
		decision.Verdict = domain.GatewayDeny
		decision.Reason = "tool result embeds an image URL carrying a query string, the standard shape of zero-click exfiltration"
		decision.Risk = domain.SeverityCritical
	}

	if match := overridePattern.FindString(body); match != "" {
		decision.Findings = append(decision.Findings, "instruction_override")
		decision.Evidence["instruction_override"] = excerptAround(body, match)
		if decision.Verdict == domain.GatewayAllow {
			decision.Verdict = domain.GatewayRequireApproval
			decision.Reason = "tool result contains text instructing the reader to disregard its previous instructions"
			decision.Risk = domain.SeverityHigh
		}
	}

	if match := hiddenMarkupPattern.FindString(body); match != "" {
		decision.Findings = append(decision.Findings, "hidden_markup")
		decision.Evidence["hidden_markup"] = excerptAround(body, match)
		if decision.Verdict == domain.GatewayAllow && addressedPattern.MatchString(body) {
			// Hidden markup alone is ordinary — every second web page has an
			// HTML comment. Hidden markup in content that also addresses a
			// model is not.
			decision.Verdict = domain.GatewayRequireApproval
			decision.Reason = "tool result hides text that speaks to a model rather than to a reader"
			decision.Risk = domain.SeverityHigh
		}
	}

	if match := addressedPattern.FindString(body); match != "" {
		decision.Findings = append(decision.Findings, "addresses_model")
		decision.Evidence["addresses_model"] = excerptAround(body, match)
	}

	if match := encodedBlobPattern.FindString(body); match != "" {
		decision.Findings = append(decision.Findings, "encoded_blob")
		decision.Evidence["encoded_blob"] = truncate(match, 80)
	}

	// A secret in returned content is worse than a secret in a request: the
	// request was the operator's own doing, this arrived from elsewhere and is
	// about to enter the agent's context.
	//
	// What is matched is the shape of a credential, not the vocabulary around
	// one. "To reset your password, visit…" contains the word and no secret;
	// treating the two alike is how a control earns its reputation for crying
	// wolf and gets switched off.
	if kind, redacted := credentialShaped(body); kind != "" {
		decision.Findings = append(decision.Findings, "credential_material:"+kind)
		decision.Evidence["credential_material"] = redacted
		decision.Taint = appendUniqueString(decision.Taint, "secret:"+kind)
		if decision.Verdict == domain.GatewayAllow {
			decision.Verdict = domain.GatewayRequireApproval
			decision.Reason = "tool result carries credential-shaped material into the agent's context"
			decision.Risk = domain.SeverityHigh
		}
	}

	if match := canaryPattern.FindString(body); match != "" {
		decision.Findings = append(decision.Findings, "canary_material")
		decision.Evidence["canary_material"] = match
		decision.Taint = appendUniqueString(decision.Taint, "canary:"+match)
		decision.Verdict = domain.GatewayDeny
		decision.Reason = "tool result touched a deception asset, which nothing legitimate reads"
		decision.Risk = domain.SeverityCritical
	}

	if len(decision.Findings) > 0 {
		decision.Taint = appendUniqueString(decision.Taint, "inspected:flagged")
	}
	if decision.Reason == "" && len(decision.Findings) > 0 {
		decision.Reason = "tool result carries signals worth review: " + strings.Join(decision.Findings, ", ")
	}
	return decision
}

// smuggledRunes reports invisible characters that a model reads and a person
// cannot see.
//
// The Unicode tag block is the sharp case: U+E0000–U+E007F mirrors ASCII, so a
// complete instruction can be written in characters that render as nothing at
// all. Zero-width spaces and joiners are the older trick, used both to hide text
// and to break up words so a keyword list misses them.
func smuggledRunes(body string) string {
	counts := map[string]int{}
	for _, r := range body {
		switch {
		case r >= 0xE0000 && r <= 0xE007F:
			counts["unicode tag characters"]++
		case r == 0x200B || r == 0x200C || r == 0x200D || r == 0xFEFF:
			counts["zero-width characters"]++
		case r == 0x2060 || r == 0x00AD:
			counts["word joiners or soft hyphens"]++
		case unicode.Is(unicode.Cf, r) && r != '\n' && r != '\r' && r != '\t':
			counts["other formatting characters"]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(counts))
	for name, count := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", count, name))
	}
	// Sorted so the same content always produces the same evidence string; an
	// audit record that changes between identical inputs is not evidence.
	sortStrings(parts)
	return strings.Join(parts, ", ")
}

func normalizeToolOrigin(origin string) string {
	if host := normalizeHost(origin); host != "" && strings.Contains(origin, "/") {
		return host
	}
	return strings.ToLower(strings.TrimSpace(origin))
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// decodeSmuggledText turns Unicode tag characters back into the text they
// stand for.
//
// The block mirrors ASCII, so an instruction written in it renders as nothing
// and reads as ordinary text to a model. Decoding it is what turns "162
// invisible characters" into a sentence somebody can weigh.
func decodeSmuggledText(body string) string {
	var decoded strings.Builder
	for _, r := range body {
		if r >= 0xE0000 && r <= 0xE007F {
			decoded.WriteRune(r - 0xE0000)
		}
	}
	return strings.TrimSpace(decoded.String())
}
