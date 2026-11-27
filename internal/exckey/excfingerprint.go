// Package exckey derives a grouping key for the exception events of a span.
// Span exceptions carry no such key, so one is computed: a hash over the
// exception type plus the most specific stable evidence the event offers,
// found by walking a fallback ladder from in-app stack frames down to the
// type alone. Same input, same key — on every machine, in every locale.
//
// The package imports nothing of this module. A caller hands it the fields
// of its events (Event) and gets the key and what it was computed from.
//
// What the readers were built on: the corpus under testdata/exceptions is
// written after what each runtime prints, and holds one trace per runtime
// that a throwaway program produced on OpenJDK 21, CPython 3.14, Node 24
// and Go 1.26 (the "real:" cases; the directory the program ran in is
// replaced by /srv/shop). No .NET runtime was at hand, so every .NET input,
// in the corpus and in the tests, is written by hand from the runtime's
// source and documentation. Traces from production will show forms nobody
// wrote down: the first raise of ExceptionKeyVersion is expected once they
// are seen.
//
// A known limit of the message rung: a value is replaced by its form (a
// quoted value, a UUID, a hex run of seven characters or more, a number, an
// id behind a lower-case prefix). An id of another form ("A1B2C3",
// "req-8Hk2Lm9Q", "12ab") is kept, and such a message has a key per value.
package exckey

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Event is one exception event of a span: what the fingerprint reads of it.
type Event struct {
	Type       string
	Message    string
	StackTrace string
	// Root is exception.is_caused_by_root: of the events of a cause chain,
	// the one that is the root cause.
	Root bool
}

// ExceptionKeyVersion prefixes every key. Once a release has shipped keys of
// a version, a change to the fingerprint, the frame parser or the message
// parameteriser that moves an exception to another key must raise it. Two
// golden files under testdata/exceptions stand in the way of doing that
// unnoticed: a digest of every table, expression and limit of these files
// (TestExceptionKeyInputsArePinned), and the keys of a corpus
// (TestExceptionKeysAreStable). The code of a function is covered by cases
// alone: a change to it shows only where the corpus or a test has an input
// it moves.
const ExceptionKeyVersion = "e1"

// FingerprintFrames is how many frames that count go into a key, from the
// throw site down: on the app-frames rung the application's frames that are
// not pass-through, on the all-frames rung every frame. The throw site is
// one of them where it counts. Where it does not (a runtime or pass-through
// frame above the application's) it is in the key on top of them, so a key
// holds at most FingerprintFrames+1 frames. Deeper frames are the callers,
// and one bug has many.
const FingerprintFrames = 5

// fingerprintKeyLen is the number of hex characters kept from the hash.
const fingerprintKeyLen = 12

// FingerprintRung is the step of the fallback ladder that produced a key,
// most specific first.
type FingerprintRung int

const (
	// RungAppFrames: type + the throw site + the top in-app frames.
	RungAppFrames FingerprintRung = iota + 1
	// RungFrames: type + the throw site + the top frames; no in-app frame
	// counts.
	RungFrames
	// RungMessage: type + the parameterised message. No frame was read: the
	// event has no stack, one no reader can use, or a Go trace that is not
	// a panic's.
	RungMessage
	// RungType: the type alone.
	RungType
)

// String is the rung's label for display.
func (r FingerprintRung) String() string {
	switch r {
	case RungAppFrames:
		return "app frames"
	case RungFrames:
		return "frames"
	case RungMessage:
		return "message"
	case RungType:
		return "type"
	}
	return ""
}

// ExceptionFingerprint is the grouping key of one span's exception and what
// it was computed from.
type ExceptionFingerprint struct {
	Key  string // ExceptionKeyVersion + ":" + 12 hex characters
	Rung FingerprintRung
	// Type is the exception type that went into the key. It names the same
	// exception as Frames: where the stack text of the event holds a chain
	// ("Caused by:", an inner exception, an earlier traceback), that is the
	// root cause, whose type is read from the text.
	Type string
	// EventType is the exception.type of the event the key was computed
	// from, for display: the outermost exception where Type is a deeper one.
	EventType string
	// Message is the event's message, parameterised (ParameterizeMessage).
	// It is set on every rung and hashed only on RungMessage.
	Message string
	// Frames are the frames that went into the key, the throw site first;
	// nil below RungFrames.
	//
	// The strings of Frames, Type, EventType and Message may share their
	// memory with the text of the event they were read from, and keep it
	// alive. A caller that holds a fingerprint longer than its events
	// clones them (strings.Clone).
	Frames []StackFrame
}

// FingerprintExceptions computes the grouping key for the exception events
// of one span. The event is the root of the cause chain, the last one where
// no chain is marked (rootException); false when no event gives anything a
// reader can use (exceptionReading.empty).
func FingerprintExceptions(excs []Event) (ExceptionFingerprint, bool) {
	root, ok := rootException(excs)
	if !ok {
		return ExceptionFingerprint{}, false
	}
	fp := ExceptionFingerprint{
		Rung:      RungType,
		EventType: root.typ,
		Message:   root.message,
	}
	fp.Type = fp.EventType

	// The type and the frames of a key come from one exception. Below the
	// frame rungs that is the event's, which the message belongs to as well.
	all := root.frames
	if root.rootType != "" && len(all) > 0 {
		fp.Type = root.rootType
	}
	var evidence []string
	if app := topFrames(all, frameInApp, isPassThrough); len(app) > 0 {
		fp.Rung, fp.Frames = RungAppFrames, app
	} else if len(all) > 0 {
		fp.Rung, fp.Frames = RungFrames, topFrames(all, anyFrame, noFrame)
	} else if fp.Message != "" {
		fp.Rung, evidence = RungMessage, []string{fp.Message}
	}
	for _, f := range fp.Frames {
		evidence = append(evidence, f.Module, f.Function)
	}
	fp.Key = fingerprintKey(fp.Rung, fp.Type, evidence)
	return fp, true
}

// anyFrame and noFrame are the two answers that do not depend on the frame.
func anyFrame(StackFrame) bool { return true }
func noFrame(StackFrame) bool  { return false }

// topFrames picks the frames of a key from a root cause's frames: the throw
// site, then the frames below it that count and do not only hand the call
// on, until FingerprintFrames such frames are in (the throw site included
// where it is one). The throw site is part of every key whether it counts
// or not: what is thrown where tells two bugs apart even where the same
// application frames lead to both. nil when no frame counts.
//
// Pass-through frames are left out only next to the application's other
// frames (handsOn is isPassThrough on the app-frames rung). On the
// all-frames rung every frame counts and none is left out: there a handler's
// or a filter's own frame may be all that tells it from the next one.
func topFrames(all []StackFrame, counts, handsOn func(StackFrame) bool) []StackFrame {
	if len(all) == 0 {
		return nil
	}
	top := []StackFrame{all[0]}
	counted := counts(all[0])
	left := FingerprintFrames
	if counted && !handsOn(all[0]) {
		left--
	}
	for _, f := range all[1:] {
		// Dropping the frames between two calls of one function leaves a
		// repeat behind; it is the same recursion.
		// What a frame names decides, as in collapseFrames: a
		// dependency's method and the wrapper generated for it are one.
		if last := top[len(top)-1]; left > 0 && counts(f) && !handsOn(f) &&
			(last.Module != f.Module || last.Function != f.Function || last.Syntax != f.Syntax) {
			top, counted = append(top, f), true
			left--
		}
	}
	if !counted {
		return nil
	}
	return top
}

// fingerprintKey hashes the canonical form of a fingerprint: the rung's
// number, the type and the evidence fields in order (a
// message, or a module and a function per frame). Each field is written
// behind its length in bytes ("7:addItem"), so no field can run into the
// next whatever it holds.
func fingerprintKey(rung FingerprintRung, typ string, evidence []string) string {
	canonical := appendKeyField(nil, strconv.Itoa(int(rung)))
	canonical = appendKeyField(canonical, typ)
	for _, field := range evidence {
		canonical = appendKeyField(canonical, field)
	}
	sum := sha256.Sum256(canonical)
	return ExceptionKeyVersion + ":" + hex.EncodeToString(sum[:])[:fingerprintKeyLen]
}

func appendKeyField(b []byte, field string) []byte {
	b = strconv.AppendInt(b, int64(len(field)), 10)
	b = append(b, ':')
	return append(b, field...)
}

// exceptionReading is what is read of one event: its type without the
// space around it, its message parameterised, and the frames of its root
// cause with the type the stack text gives that. A Go trace that is not a
// panic's shows where the error was seen and has no frames here.
type exceptionReading struct {
	typ      string
	message  string
	frames   []StackFrame
	rootType string
}

func readException(ex Event) exceptionReading {
	r := exceptionReading{typ: strings.TrimSpace(ex.Type), message: ParameterizeMessage(ex.Message)}
	r.frames, r.rootType = faultStack(ex.StackTrace)
	return r
}

// empty reports whether an event gives nothing a reader can use: no type,
// no message once that is parameterised, and no frame. Stack text that no
// reader can use gives nothing, and neither does a Go stack that is not a
// panic's. A key for such an event would be the same for every one of every
// service, and as the last event of a span it would stand in for the real
// one before it.
func (r exceptionReading) empty() bool {
	return r.typ == "" && r.message == "" && len(r.frames) == 0
}

// rootException picks the event a span's fingerprint is computed from, of
// those that give something a reader can use (exceptionReading.empty);
// false when none does: the last event flagged exception.is_caused_by_root,
// else the last event. Instrumentation that records a chain flags its root;
// without a flag the last event is the exception the span ended with.
//
// An event is read once, and only where the choice needs it: one with a
// type counts unread, and is read when it is chosen.
func rootException(excs []Event) (exceptionReading, bool) {
	last, read := -1, exceptionReading{}
	for i := len(excs) - 1; i >= 0 && (last < 0 || !excs[last].Root); i-- {
		if last >= 0 && !excs[i].Root {
			continue
		}
		if strings.TrimSpace(excs[i].Type) != "" {
			last, read = i, exceptionReading{}
			continue
		}
		if r := readException(excs[i]); !r.empty() {
			last, read = i, r
		}
	}
	if last < 0 {
		return exceptionReading{}, false
	}
	if read.empty() {
		read = readException(excs[last])
	}
	return read, true
}
