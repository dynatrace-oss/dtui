package tui

import (
	"strings"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
)

// queryNotice is a distilled, user-facing summary of a Grail query
// notification. Grail fails silently — a scan-limit hit or fetch timeout comes
// back as an empty result with exit 0 (docs/dev/learnings.md) — so without this
// the user cannot tell a genuinely empty answer from a truncated one. The text
// leads with what happened and the one lever that helps most: narrow the
// timeframe (the 't' picker).
type queryNotice struct {
	text string // banner text, incl. the mitigation hint
	warn bool   // WARNING/ERROR severity: the result may be PARTIAL/incomplete
}

// noticeFor picks the notice to show from a query's notifications, or
// reports false when none warrant surfacing. Of several it takes the gravest
// (gravestNotice), the one a sampled view's line above its table names too,
// so the footer and that line never give two reasons for one result.
//
// The category set mirrors dtctl's exec.classifyNotification, which is
// unexported, so the TUI and the CLI stay in step; exec.PartialCause and
// exec.ResultIsPartial are its exported readers
// (TestClassifyNoticeKnowsWhatDtctlCallsPartial). The size limit is told
// apart from the row limit here only.
func noticeFor(ns []exec.QueryNotification) (queryNotice, bool) {
	// INFO and below are noise in a one-line banner.
	switch gravestNotice(ns, noticeWarns) {
	case noticeScanLimit:
		return queryNotice{warn: true,
			text: "partial result — Grail scan limit reached; narrow the timeframe (t) to scan less"}, true
	case noticeResultLimit:
		return queryNotice{warn: true,
			text: "result truncated to the row limit — narrow the timeframe (t) or add a filter"}, true
	case noticeSizeLimit:
		return queryNotice{warn: true,
			text: "result truncated to the size limit — narrow the timeframe (t) or add a filter"}, true
	case noticeTimeout:
		return queryNotice{warn: true,
			text: "fetch timed out — result may be incomplete; narrow the timeframe (t)"}, true
	case noticeConsumption:
		return queryNotice{warn: true,
			text: "partial result — query consumption limit reached; narrow the timeframe (t) to read less"}, true
	case noticeSampling:
		return queryNotice{text: "results are sampled (approximate)"}, true
	}
	return queryNotice{}, false
}

// gravestNotice returns the category of the notice that says least about
// what arrived, among those counts accepts (nil = all): a timeout or a scan
// that stopped early before a consumption stop, that before the size and
// the row limit, and sampling last. After a scan that stopped early the
// records are not known to be the newest of the timeframe, whatever limit
// also applied, so the order of the notifications must not decide.
func gravestNotice(ns []exec.QueryNotification, counts func(exec.QueryNotification) bool) int {
	rank := map[int]int{
		noticeSampling: 1, noticeResultLimit: 2, noticeSizeLimit: 3,
		noticeConsumption: 4, noticeScanLimit: 5, noticeTimeout: 6,
	}
	gravest := noticeNone
	for _, n := range ns {
		if counts != nil && !counts(n) {
			continue
		}
		if c := classifyNotice(n.NotificationType, n.Message); rank[c] > rank[gravest] {
			gravest = c
		}
	}
	return gravest
}

// noticeWarns reports whether a notification is severe enough to act on.
func noticeWarns(n exec.QueryNotification) bool {
	switch strings.ToUpper(n.Severity) {
	case "WARNING", "WARN", "ERROR":
		return true
	}
	return false
}

// partialNotice reports whether the notifications say the result may be
// incomplete (scan limit, truncation, timeout, or consumption limit) — for
// the callers that must not react to an empty result as if the data simply
// weren't there.
func partialNotice(ns []exec.QueryNotification) bool {
	n, ok := noticeFor(ns)
	return ok && n.warn
}

// Notice categories, mirroring exec's notif* set (unexported there).
const (
	noticeNone = iota
	noticeScanLimit
	noticeResultLimit
	noticeSizeLimit
	noticeTimeout
	noticeSampling
	noticeConsumption
)

// classifyNotice maps a notification to a category by its type, falling back to
// a message-text match — not every deployment stamps a stable type, which is
// why the live-observed message ("stopped after N gigabytes of data were
// scanned") is a second signal.
//
// A result cut at its size limit (the request's maxResultBytes) arrives as
// BYTE_LIMIT_ADDED, "Your result has been limited because the maximum bytes
// (N) were reached." (validated live on a OneAgent-fed tenant); dtctl's own
// classifier does not know that type.
//
// Every type and message exec.ResultIsPartial reads as a cut result has a
// category here (TestClassifyNoticeKnowsWhatDtctlCallsPartial): one this
// function missed would leave a cut result looking whole.
func classifyNotice(notificationType, message string) int {
	switch notificationType {
	case "SCAN_LIMIT_GBYTES":
		return noticeScanLimit
	case "RESULT_LIMIT_RECORDS":
		return noticeResultLimit
	case "BYTE_LIMIT_ADDED", "RESULT_LIMIT_BYTES":
		return noticeSizeLimit
	case "FETCH_TIMEOUT", "FETCH_EXEC_TIME_LIMIT":
		return noticeTimeout
	case "SAMPLING_APPLIED":
		return noticeSampling
	case "QUERY_CONSUMPTION_LIMIT":
		return noticeConsumption
	}
	msg := strings.ToLower(message)
	switch {
	case strings.Contains(msg, "scan") && strings.Contains(msg, "gigabyte"):
		return noticeScanLimit
	case strings.Contains(msg, "maximum bytes"):
		return noticeSizeLimit
	case strings.Contains(msg, "result has been limited") || strings.Contains(msg, "limited to"):
		return noticeResultLimit
	case strings.Contains(msg, "timed out") || strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "internal time limit"):
		return noticeTimeout
	}
	return noticeNone
}
