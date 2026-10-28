package review

// reviewparts.go splits a consolidated review into the part that is posted to
// the PR and the part that is not. It is the Go port of ai-resources'
// workflows/review_payload.py split_review / cap_summary.
//
// The consolidated review in the spool is written for the human who decides
// whether to post it:
//
//	# Consolidated Review — <repo> PR #<n>
//	## Summary          <- the only prose posted, as the review body
//	## Findings         <- one `file:line - severity - issue -> fix` per line
//	## Reviewer notes   <- verdict rationale, verified non-issues, dropped
//	                       findings; never posted
//	VERDICT: <...>
//
// Only the summary and the findings reach GitHub. Everything else stays in
// the spool file, which is archived (whole) to done/ after posting.

import (
	"regexp"
	"strings"
)

// maxSummarySentences caps the posted summary, whatever the consolidator wrote.
const maxSummarySentences = 3

// headingRe matches a Markdown ATX heading, capturing its level marks and title.
var headingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)

// verdictOnlyLineRe matches a bare "VERDICT: <value>" line.
var verdictOnlyLineRe = regexp.MustCompile(`(?i)^\s*VERDICT:\s*\S+\s*$`)

// listMarkerRe matches a leading Markdown list marker.
var listMarkerRe = regexp.MustCompile(`^\s*(?:[-*+]\s+|\d+[.)]\s+)`)

// sentenceSplitRe finds the whitespace between two sentences: after ., ! or ?
// and before something that starts a new sentence. Go's RE2 has no
// lookbehind, so the punctuation and the next character are captured and
// re-attached by splitSentences.
var sentenceSplitRe = regexp.MustCompile("([.!?])\\s+([A-Z`*\"'(])")

var (
	summaryTitles = []string{"summary", "overview"}
	// notesTitles are matched as title prefixes, not substrings, so a
	// heading such as "Findings (verified)" is not mistaken for notes.
	notesTitles = []string{"reviewer notes", "notes", "noted", "not flagged", "verified",
		"cleared", "non-issues", "rationale", "dropped"}
	// droppedTitles are the notes prefixes that still mean "not posted" when
	// the heading also says "findings" ("Dropped findings").
	droppedTitles = []string{"dropped", "cleared", "not flagged", "non-issues"}
)

// reviewParts is a consolidated review split into what is posted (summary,
// findings) and what is not (notes, plus finding-list items that don't parse).
type reviewParts struct {
	summary  string
	findings []finding
	// raw holds each finding's source line, parallel to findings, so a
	// finding that can't be posted can be reported verbatim.
	raw   []string
	notes string
	// unparsed holds list items under a Findings heading that don't parse
	// as a finding. They are reported as unposted, never posted as prose.
	unparsed []string
}

// sectionKind classifies an H2 heading title. A heading that mentions
// findings is a findings section ("Verified findings", "Findings and notes")
// unless it names findings that were dropped ("Dropped findings"): a
// mis-classified findings section would silently withhold real findings.
func sectionKind(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	hasPrefix := func(prefixes []string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
		return false
	}
	switch {
	case hasPrefix(summaryTitles):
		return "summary"
	case strings.Contains(t, "finding") && !hasPrefix(droppedTitles):
		return "findings"
	case hasPrefix(notesTitles):
		return "notes"
	}
	return "other"
}

// firstParagraph returns the first prose paragraph in lines: not a heading,
// list item, finding, or VERDICT line. Used as the summary for older reviews
// that have no ## Summary section.
func firstParagraph(lines []string) string {
	var para []string
	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		_, isFinding := parseFindingLine(ln)
		prose := s != "" && !headingRe.MatchString(ln) && !verdictOnlyLineRe.MatchString(ln) &&
			!isFinding && !listMarkerRe.MatchString(ln)
		if prose {
			para = append(para, s)
		} else if len(para) > 0 {
			break
		}
	}
	return strings.Join(para, " ")
}

// splitReview splits a consolidated review into summary, findings and notes.
//
// It uses the ## Summary / ## Findings / ## Reviewer notes sections when
// present. Finding-shaped lines under a notes section are notes, not
// findings: that is where the consolidator records what it dropped. Older
// reviews without a ## Summary fall back to the first prose paragraph as the
// summary; every other non-finding line counts as notes.
func splitReview(body string) reviewParts {
	kind := "preamble"
	var summaryLines, otherLines, noteLines []string
	var parts reviewParts

	for _, ln := range strings.Split(body, "\n") {
		if h := headingRe.FindStringSubmatch(ln); h != nil {
			// Only H2 starts a new section; H3+ sub-headings inherit their
			// parent's, so "### Notes on X" under Findings stays a finding.
			if len(h[1]) == 2 {
				kind = sectionKind(h[2])
				if kind == "notes" {
					noteLines = append(noteLines, ln)
				}
			}
			continue // the H1 title is never posted
		}
		if verdictOnlyLineRe.MatchString(ln) {
			continue // the VERDICT line is never posted
		}
		if kind == "notes" {
			noteLines = append(noteLines, ln)
			continue
		}
		if f, ok := parseFindingLine(ln); ok {
			parts.findings = append(parts.findings, f)
			parts.raw = append(parts.raw, strings.TrimSpace(ln))
			continue
		}
		switch {
		case kind == "findings" && listMarkerRe.MatchString(ln):
			parts.unparsed = append(parts.unparsed, strings.TrimSpace(ln))
		case kind == "summary":
			summaryLines = append(summaryLines, ln)
		default:
			otherLines = append(otherLines, ln)
		}
	}

	var summaryText []string
	for _, s := range summaryLines {
		if t := strings.TrimSpace(s); t != "" {
			summaryText = append(summaryText, t)
		}
	}
	if len(summaryText) > 0 {
		parts.summary = strings.Join(summaryText, " ")
	} else {
		parts.summary = firstParagraph(otherLines)
		// The paragraph used as the summary is not also a note.
		kept := otherLines[:0:0]
		for _, ln := range otherLines {
			t := strings.TrimSpace(ln)
			if t == "" || !strings.Contains(parts.summary, t) {
				kept = append(kept, ln)
			}
		}
		otherLines = kept
	}

	all := append([]string{}, noteLines...)
	for _, ln := range otherLines {
		if strings.TrimSpace(ln) != "" {
			all = append(all, ln)
		}
	}
	parts.notes = strings.TrimSpace(strings.Join(all, "\n"))
	return parts
}

// splitSentences splits prose into sentences on ., ! or ? followed by
// whitespace and a sentence-starting character.
func splitSentences(text string) []string {
	marked := sentenceSplitRe.ReplaceAllString(strings.TrimSpace(text), "$1\x00$2")
	var out []string
	for _, s := range strings.Split(marked, "\x00") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// capSummary keeps at most maxSummarySentences sentences and, when there are
// findings, keeps the summary shorter than the findings' text by dropping
// trailing sentences (never below one).
func capSummary(summary string, findings []finding) string {
	sentences := splitSentences(summary)
	if len(sentences) > maxSummarySentences {
		sentences = sentences[:maxSummarySentences]
	}
	if len(findings) > 0 {
		budget := 0
		for _, f := range findings {
			budget += len(f.issue) + len(f.fix)
		}
		for len(sentences) > 1 && len(strings.Join(sentences, " ")) >= budget {
			sentences = sentences[:len(sentences)-1]
		}
	}
	return strings.Join(sentences, " ")
}
