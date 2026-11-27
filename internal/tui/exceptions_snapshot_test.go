package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dynatrace-oss/dtui/internal/tui/catalog"
)

// issueSample is a full sample, shaped like a busy shop's last minutes: one
// loud bug from two services, quieter ones in other runtimes, an error with
// a message and no stack, and spans whose exception event recorded nothing.
func issueSample() []map[string]any {
	kinds := []struct{ service, typ, message, stack string }{
		{"cart", "java.lang.IllegalStateException", "cart %d is locked", issueCartStack},
		{"checkout", "java.lang.IllegalStateException", "cart %d is locked", issueCartStack},
		{"orders", "java.sql.SQLException", "connection to 10.0.0.%d refused", issueOrdersStack},
		{"payments", "TimeoutError", "upstream timed out after %dms",
			"Traceback (most recent call last):\n" +
				"  File \"/app/payments/client.py\", line 88, in charge\n" +
				"    return self._post(order)\n" +
				"  File \"/app/payments/client.py\", line 41, in _post\n" +
				"    raise TimeoutError(msg)"},
		{"frontend", "TypeError", "Cannot read properties of undefined (reading 'items')",
			"TypeError: Cannot read properties of undefined (reading 'items')\n" +
				"    at renderCart (/app/src/cart/render.js:17:22)\n" +
				"    at handle (/app/src/server.js:90:5)"},
		{"inventory", "HttpError", "HTTP 503 from stock service", ""},
		{"legacy-batch", "", "", ""},
	}
	// Which kind each span of a run of twenty is.
	slots := []int{0, 0, 1, 0, 2, 0, 0, 1, 3, 0, 0, 2, 1, 0, 4, 0, 5, 0, 3, 6}
	newest := time.Date(2026, 7, 15, 11, 59, 59, 0, time.UTC)
	spans := make([]map[string]any, catalog.ExceptionSampleRows)
	for i := range spans {
		k := kinds[slots[i%len(slots)]]
		message := k.message
		if strings.Contains(message, "%d") {
			message = fmt.Sprintf(message, 100+i%150)
		}
		at := newest.Add(-time.Duration(i) * 700 * time.Millisecond).Format(time.RFC3339Nano)
		spans[i] = issueSpanRecord(i+1, at, k.service, k.typ, message, k.stack)
	}
	return spans
}

func TestSnapshotExceptionsTable(t *testing.T) {
	pinSnapshotClock(t)
	a := testApp(t, "exceptions")
	seedSample(t, a, issueSample(), nil)
	assertSnapshots(t, "exceptions", a)
}
