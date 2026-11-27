package tui

import (
	"strings"
	"testing"

	"github.com/dynatrace-oss/dtctl/pkg/exec"
)

func TestClassifyNoticeByType(t *testing.T) {
	cases := map[string]int{
		"SCAN_LIMIT_GBYTES":       noticeScanLimit,
		"RESULT_LIMIT_RECORDS":    noticeResultLimit,
		"RESULT_LIMIT_BYTES":      noticeSizeLimit,
		"BYTE_LIMIT_ADDED":        noticeSizeLimit,
		"FETCH_TIMEOUT":           noticeTimeout,
		"FETCH_EXEC_TIME_LIMIT":   noticeTimeout,
		"SAMPLING_APPLIED":        noticeSampling,
		"QUERY_CONSUMPTION_LIMIT": noticeConsumption,
		"":                        noticeNone,
	}
	for typ, want := range cases {
		if got := classifyNotice(typ, ""); got != want {
			t.Errorf("classifyNotice(%q) = %d, want %d", typ, got, want)
		}
	}
}

// The live scan-limit warning carries no stable type on some deployments, so
// the message text is the fallback signal (validated live on a large tenant).
func TestClassifyNoticeByMessageFallback(t *testing.T) {
	msg := "Your execution was stopped after 500 gigabytes of data were scanned."
	if got := classifyNotice("", msg); got != noticeScanLimit {
		t.Errorf("scan-limit message classified as %d, want scan limit", got)
	}
	if got := classifyNotice("", "The result has been limited to 1000 records"); got != noticeResultLimit {
		t.Errorf("result-limit message classified as %d, want result limit", got)
	}
}

// dtctl reads these notifications as a cut result (exec.ResultIsPartial). A
// type or message this package did not know would leave the footer silent
// and the result looking whole, so each must warn here too.
func TestClassifyNoticeKnowsWhatDtctlCallsPartial(t *testing.T) {
	for _, n := range []exec.QueryNotification{
		{NotificationType: "SCAN_LIMIT_GBYTES"},
		{NotificationType: "RESULT_LIMIT_RECORDS"},
		{NotificationType: "RESULT_LIMIT_BYTES"},
		{NotificationType: "FETCH_TIMEOUT"},
		{NotificationType: "FETCH_EXEC_TIME_LIMIT"},
		{NotificationType: "QUERY_CONSUMPTION_LIMIT"},
		{Message: "Your execution was stopped after 500 gigabytes of data were scanned."},
		{Message: "The result has been limited to 1000 records"},
		{Message: "The query reached an internal time limit."},
	} {
		if !exec.ResultIsPartial(n) {
			t.Fatalf("dtctl no longer reads %+v as a cut result; the case is stale", n)
		}
		if classifyNotice(n.NotificationType, n.Message) == noticeNone {
			t.Errorf("classifyNotice does not know %+v", n)
		}
		n.Severity = "WARNING"
		if !partialNotice([]exec.QueryNotification{n}) {
			t.Errorf("%+v raises no partial-result warning", n)
		}
	}
}

// A query stopped at its consumption limit has a footer of its own.
func TestNoticeForConsumptionLimit(t *testing.T) {
	n, ok := noticeFor([]exec.QueryNotification{{Severity: "WARNING", NotificationType: "QUERY_CONSUMPTION_LIMIT"}})
	if !ok || !n.warn || !strings.Contains(n.text, "consumption limit") || !strings.Contains(n.text, "narrow the timeframe (t)") {
		t.Errorf("notice = %+v ok=%v, want a consumption-limit warning with the lever", n, ok)
	}
}

// A result cut at maxResultBytes is announced as BYTE_LIMIT_ADDED (validated
// live). Its message also reads "has been limited", which without the type
// would pass for the row limit.
func TestNoticeForSizeLimit(t *testing.T) {
	const live = "Your result has been limited because the maximum bytes (2000) were reached."
	for _, typ := range []string{"BYTE_LIMIT_ADDED", ""} {
		if got := classifyNotice(typ, live); got != noticeSizeLimit {
			t.Errorf("classifyNotice(%q, live message) = %d, want the size limit", typ, got)
		}
	}
	n, ok := noticeFor([]exec.QueryNotification{{Severity: "WARNING", NotificationType: "BYTE_LIMIT_ADDED", Message: live}})
	if !ok || !n.warn || !strings.Contains(n.text, "size limit") {
		t.Errorf("notice = %+v ok=%v, want a size-limit warning", n, ok)
	}
	if !partialNotice([]exec.QueryNotification{{Severity: "WARNING", NotificationType: "BYTE_LIMIT_ADDED"}}) {
		t.Error("a result cut at its size limit is partial")
	}
}

// Of several notices the footer names the gravest, whatever their order: a
// sampling notice ahead of a scan limit must not make a cut result read as
// merely approximate.
func TestNoticeForTakesTheGravestOfSeveral(t *testing.T) {
	note := func(typ string) exec.QueryNotification {
		return exec.QueryNotification{Severity: "WARNING", NotificationType: typ}
	}
	// Each type outranks every one before it.
	ranked := []string{"SAMPLING_APPLIED", "RESULT_LIMIT_RECORDS", "BYTE_LIMIT_ADDED",
		"QUERY_CONSUMPTION_LIMIT", "SCAN_LIMIT_GBYTES", "FETCH_TIMEOUT"}
	for hi := 1; hi < len(ranked); hi++ {
		alone, _ := noticeFor([]exec.QueryNotification{note(ranked[hi])})
		for lo := range hi {
			for _, ns := range [][]exec.QueryNotification{
				{note(ranked[lo]), note(ranked[hi])}, {note(ranked[hi]), note(ranked[lo])},
			} {
				if got, _ := noticeFor(ns); got != alone {
					t.Errorf("%s with %s: notice = %q, want that of %s", ns[0].NotificationType, ns[1].NotificationType, got.text, ranked[hi])
				}
				if !partialNotice(ns) {
					t.Errorf("%s with %s is not read as partial", ns[0].NotificationType, ns[1].NotificationType)
				}
			}
		}
	}
	// A notice below a warning never outranks one: it is not shown at all.
	got, ok := noticeFor([]exec.QueryNotification{{Severity: "INFO", NotificationType: "FETCH_TIMEOUT"}, note("SAMPLING_APPLIED")})
	if !ok || got.warn || !strings.Contains(got.text, "sampled") {
		t.Errorf("an INFO timeout beside a sampling warning gave %+v", got)
	}
}

func TestNoticeForSkipsInfoSeverity(t *testing.T) {
	if _, ok := noticeFor([]exec.QueryNotification{
		{Severity: "INFO", NotificationType: "SCAN_LIMIT_GBYTES"},
	}); ok {
		t.Error("INFO-severity notifications must not raise a banner")
	}
}

func TestNoticeForScanLimitIsPartial(t *testing.T) {
	n, ok := noticeFor([]exec.QueryNotification{
		{Severity: "WARNING", NotificationType: "SCAN_LIMIT_GBYTES",
			Message: "stopped after 500 gigabytes of data were scanned"},
	})
	if !ok || !n.warn {
		t.Fatalf("scan-limit warning should be a partial-result notice: %+v ok=%v", n, ok)
	}
	if !strings.Contains(n.text, "narrow the timeframe") {
		t.Errorf("notice must offer the mitigation, got %q", n.text)
	}
	if !partialNotice([]exec.QueryNotification{
		{Severity: "WARNING", NotificationType: "SCAN_LIMIT_GBYTES"},
	}) {
		t.Error("partialNotice should be true for a scan-limit warning")
	}
}

func TestNoticeForSamplingIsNotPartial(t *testing.T) {
	n, ok := noticeFor([]exec.QueryNotification{
		{Severity: "WARNING", NotificationType: "SAMPLING_APPLIED"},
	})
	if !ok || n.warn {
		t.Errorf("sampling is approximate, not partial: %+v ok=%v", n, ok)
	}
	if partialNotice([]exec.QueryNotification{{Severity: "WARNING", NotificationType: "SAMPLING_APPLIED"}}) {
		t.Error("sampling must not count as a partial result")
	}
}

func scanLimitNotices() []exec.QueryNotification {
	return []exec.QueryNotification{{
		Severity:         "WARNING",
		NotificationType: "SCAN_LIMIT_GBYTES",
		Message:          "stopped after 500 gigabytes of data were scanned",
	}}
}

// A scan-limit notification on the visible view's own result must reach the
// footer — Grail returns it as an empty success, so this banner is the only
// signal the user gets that the answer is incomplete.
func TestScanLimitNoticeSurfacedInFooter(t *testing.T) {
	a := testApp(t, "problems")
	top := a.top()

	a.Update(dataMsg{owner: top}) // clean result: no banner
	if strings.Contains(a.View(), "scan limit") {
		t.Fatal("no notice expected before a scan-limit result")
	}

	a.Update(dataMsg{owner: top, notices: scanLimitNotices()})
	if !strings.Contains(a.View(), "scan limit") {
		t.Fatal("scan-limit notice must appear in the footer")
	}

	a.Update(dataMsg{owner: top}) // later clean result on the same view
	if strings.Contains(a.View(), "scan limit") {
		t.Error("a clean result must clear the notice")
	}
}

// A secondary or background query must not clear the visible view's warning.
func TestNoticeIgnoresNonTopOwner(t *testing.T) {
	a := testApp(t, "problems")
	a.Update(dataMsg{owner: a.top(), notices: scanLimitNotices()})
	a.Update(dataMsg{owner: dictOwner{}}) // a different owner, clean
	if !strings.Contains(a.View(), "scan limit") {
		t.Error("a foreign owner's result must not clear the top view's notice")
	}
}

func TestNoticeClearedOnNavigate(t *testing.T) {
	a := testApp(t, "problems")
	a.Update(dataMsg{owner: a.top(), notices: scanLimitNotices()})
	if !strings.Contains(a.View(), "scan limit") {
		t.Fatal("precondition: notice should be showing")
	}
	a.navigate(newInspectorView(a.ds, "x", map[string]any{}), false)
	if strings.Contains(a.View(), "scan limit") {
		t.Error("leaving the view must drop its notice")
	}
}
