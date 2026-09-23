package relay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// Forge frozen-test flag B13: second human-approved spec amendment adds malformed-grammar and whitespace-wrapped scalar vectors.
func TestB13(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    string
		wantErr bool
	}{
		{
			name:  "canonical object",
			input: []byte(` { "b":2,"a":1 } `),
			want:  `{"a":1,"b":2}`,
		},
		{
			name:  "recursive object order",
			input: []byte(`{"z":[3,{"b":2,"a":1}],"a":0}`),
			want:  `{"a":0,"z":[3,{"a":1,"b":2}]}`,
		},
		{
			name:  "RFC string serialization",
			input: []byte(`{"s":"\u20ac\u000F\u000a\/"}`),
			want:  `{"s":"€\u000f\n/"}`,
		},
		{
			name:  "literal escaped-backslash u+d800",
			input: []byte(`{"s":"\\ud800"}`),
			want:  `{"s":"\\ud800"}`,
		},
		{
			name: "RFC number formatting",
			input: []byte(`{
				"numbers": [333333333.33333329, 1E30, 4.50, 2e-3, 0.000000000000000000000000001],
				"string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
				"literals": [null, true, false]
			}`),
			want: `{"literals":[null,true,false],"numbers":[333333333.3333333,1e+30,4.5,0.002,1e-27],"string":"€$\u000f\nA'B\"\\\\\"/"}`,
		},
		{
			name:  "UTF-16 property order",
			input: []byte(`{"\ue000":1,"\ud83d\ude00":2}`),
			want:  `{"😀":2,"":1}`,
		},
		{
			name:    "duplicate decoded names",
			input:   []byte(`{"a":1,"\u0061":2}`),
			wantErr: true,
		},
		{
			name:    "invalid UTF-8",
			input:   []byte("{\"a\":\"\xff\"}"),
			wantErr: true,
		},
		{
			name:    "invalid syntax",
			input:   []byte(`{"a":}`),
			wantErr: true,
		},
		{
			name:    "truncated object key",
			input:   []byte(`{"a`),
			wantErr: true,
		},
		{
			name:    "whitespace inside number",
			input:   []byte(`{"n":1 2}`),
			wantErr: true,
		},
		{
			name:    "leading plus",
			input:   []byte(`{"n":+1}`),
			wantErr: true,
		},
		{
			name:    "leading zero",
			input:   []byte(`{"n":01}`),
			wantErr: true,
		},
		{
			name:  "whitespace-wrapped scalar number",
			input: []byte(" \n1\t "),
			want:  `1`,
		},
		{
			name:  "whitespace-wrapped scalar true",
			input: []byte(" \ntrue\t "),
			want:  `true`,
		},
		{
			name:  "whitespace-wrapped scalar null",
			input: []byte(" \r\nnull\t "),
			want:  `null`,
		},
		{
			name:    "isolated high surrogate",
			input:   []byte(`{"a":"\ud800"}`),
			wantErr: true,
		},
		{
			name:    "isolated low surrogate",
			input:   []byte(`{"a":"\udc00"}`),
			wantErr: true,
		},
		{
			name:    "invalid surrogate pair",
			input:   []byte(`{"a":"\ud800\u0041"}`),
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := canonicalizeJSON(test.input)
			if test.wantErr {
				if err == nil {
					t.Errorf("canonicalizeJSON(%q) returned no error", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalizeJSON(%q): %v", test.input, err)
			}
			if string(got) != test.want {
				t.Errorf("canonicalizeJSON(%q) = %q, want %q", test.input, got, test.want)
			}

			again, err := canonicalizeJSON(got)
			if err != nil {
				t.Fatalf("canonicalizeJSON(canonical output): %v", err)
			}
			if string(again) != test.want {
				t.Errorf("canonicalizeJSON(canonical output) = %q, want %q", again, test.want)
			}
		})
	}
}

// Forge frozen-test flag B15: human-approved signature correction requires the safe empty-chain call to return no error.
func TestB15(t *testing.T) {
	const wallMilliseconds = 1_700_000_000_000

	got, err := nextEventPosition(nil, wallMilliseconds)
	if err != nil {
		t.Fatalf("nextEventPosition(empty, %d): %v", wallMilliseconds, err)
	}
	if got.sequence != 1 || got.predecessor != nil || got.physicalMilliseconds != wallMilliseconds || got.logical != 0 {
		t.Fatalf("nextEventPosition(empty, %d) = %+v", wallMilliseconds, got)
	}
}

func TestB16(t *testing.T) {
	const priorEventID = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	prior := eventPosition{
		eventID:              priorEventID,
		sequence:             7,
		physicalMilliseconds: 100,
		logical:              2,
	}
	tests := []struct {
		name             string
		wallMilliseconds int64
		wantPhysical     int64
		wantLogical      uint64
	}{
		{name: "advancing wall", wallMilliseconds: 101, wantPhysical: 101, wantLogical: 0},
		{name: "regressed wall", wallMilliseconds: 99, wantPhysical: 100, wantLogical: 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := nextEventPosition(&prior, test.wallMilliseconds)
			if err != nil {
				t.Fatalf("nextEventPosition(prior, %d): %v", test.wallMilliseconds, err)
			}
			if got.predecessor == nil {
				t.Fatalf("nextEventPosition(prior, %d) predecessor is nil", test.wallMilliseconds)
			}
			if got.sequence != prior.sequence+1 || *got.predecessor != priorEventID || got.physicalMilliseconds != test.wantPhysical || got.logical != test.wantLogical {
				t.Errorf("nextEventPosition(prior, %d) = %+v, predecessor %q", test.wallMilliseconds, got, *got.predecessor)
			}
		})
	}
}

func TestB14(t *testing.T) {
	seed := []byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
	}
	const payload = `{"author":"u-11111111111111111111111111111111","author_seq":1,"body":"Hello, relay!","created":{"logical":0,"physical_ms":1700000000000},"prev":null,"project_epoch":1,"project_id":"p-00000000000000000000000000000000","signing_algorithm":"ed25519","signing_key_id":"sha256:56475aa75463474c0285df5dbf2bcab73da651358839e9b77481b2eab107708c","to":"u-22222222222222222222222222222222","type":"message","version":1}`
	const wantSignature = `t-8vyLamtw0ls4o2FxoOcriiNREfU7_SiJclcRVyKwHFMKb9x-HvEGzZrw4UCJ893xQWxKHV-r1GfSPAFMWwCQ`
	const wantEnvelope = `{"payload":{"author":"u-11111111111111111111111111111111","author_seq":1,"body":"Hello, relay!","created":{"logical":0,"physical_ms":1700000000000},"prev":null,"project_epoch":1,"project_id":"p-00000000000000000000000000000000","signing_algorithm":"ed25519","signing_key_id":"sha256:56475aa75463474c0285df5dbf2bcab73da651358839e9b77481b2eab107708c","to":"u-22222222222222222222222222222222","type":"message","version":1},"signature":"t-8vyLamtw0ls4o2FxoOcriiNREfU7_SiJclcRVyKwHFMKb9x-HvEGzZrw4UCJ893xQWxKHV-r1GfSPAFMWwCQ"}`
	const wantEventID = "sha256:eb924ecd3a4946fa48ef45283f1735b2422d0159ff28ea14d0deb1f68c41c952"

	envelope, eventID, err := signEvent(seed, []byte(payload))
	if err != nil {
		t.Fatalf("signEvent: %v", err)
	}
	if got := string(envelope); got != wantEnvelope {
		t.Fatalf("signEvent envelope = %q, want %q", got, wantEnvelope)
	}
	signatureStart := len(envelope) - len(wantSignature) - 2
	if signatureStart < 0 || string(envelope[signatureStart:len(envelope)-2]) != wantSignature {
		t.Errorf("signEvent signature is not %q", wantSignature)
	}
	if eventID != wantEventID {
		t.Errorf("signEvent event ID = %q, want %q", eventID, wantEventID)
	}
}

func TestB17(t *testing.T) {
	const maxInteger = 9007199254740991
	const priorEventID = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		name             string
		prior            *eventPosition
		wallMilliseconds int64
	}{
		{name: "negative wall on empty chain", wallMilliseconds: -1},
		{name: "wall above maximum on empty chain", wallMilliseconds: maxInteger + 1},
		{name: "prior sequence at maximum", prior: &eventPosition{eventID: priorEventID, sequence: maxInteger, physicalMilliseconds: 100}, wallMilliseconds: 101},
		{name: "prior logical at maximum without wall advance", prior: &eventPosition{eventID: priorEventID, sequence: 7, physicalMilliseconds: 100, logical: maxInteger}, wallMilliseconds: 100},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := nextEventPosition(test.prior, test.wallMilliseconds); err != errIntegerLimit {
				t.Errorf("nextEventPosition(%+v, %d) error = %v, want errIntegerLimit", test.prior, test.wallMilliseconds, err)
			}
		})
	}
}

func TestB18(t *testing.T) {
	const finalName = "event.json"
	contents := []byte("complete event")

	assertEntries := func(t *testing.T, directory string, want ...string) {
		t.Helper()
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != len(want) {
			t.Fatalf("directory entries = %v, want %v", entries, want)
		}
		for i := range want {
			if entries[i].Name() != want[i] {
				t.Fatalf("directory entry %d = %q, want %q", i, entries[i].Name(), want[i])
			}
		}
	}

	t.Run("new publication", func(t *testing.T) {
		directory := t.TempDir()
		result, err := publish(context.Background(), directory, finalName, contents)
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		if result.state != publicationNew || result.cleanupWarning != nil {
			t.Fatalf("publish result = %+v, want new without cleanup warning", result)
		}

		final := filepath.Join(directory, finalName)
		info, err := os.Lstat(final)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(final)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || string(got) != string(contents) {
			t.Fatalf("final mode/content = %v/%q, want regular/%q", info.Mode(), got, contents)
		}
		assertEntries(t, directory, finalName)
	})

	t.Run("identical retry", func(t *testing.T) {
		directory := t.TempDir()
		final := filepath.Join(directory, finalName)
		if err := os.WriteFile(final, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		before, err := os.Stat(final)
		if err != nil {
			t.Fatal(err)
		}

		result, err := publish(context.Background(), directory, finalName, contents)
		if err != nil {
			t.Fatalf("publish: %v", err)
		}
		after, err := os.Stat(final)
		if err != nil {
			t.Fatal(err)
		}
		if result.state != publicationIdentical || result.cleanupWarning != nil {
			t.Fatalf("publish result = %+v, want identical without cleanup warning", result)
		}
		if !os.SameFile(before, after) {
			t.Error("identical retry replaced the final file")
		}
		assertEntries(t, directory, finalName)
	})

	t.Run("prefix plus extra conflicts", func(t *testing.T) {
		directory := t.TempDir()
		final := filepath.Join(directory, finalName)
		existing := append(append([]byte(nil), contents...), '!')
		if err := os.WriteFile(final, existing, 0o600); err != nil {
			t.Fatal(err)
		}

		if _, err := publish(context.Background(), directory, finalName, contents); !errors.Is(err, errPublishConflict) {
			t.Fatalf("publish error = %v, want errPublishConflict", err)
		}
		got, err := os.ReadFile(final)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(existing) {
			t.Fatalf("final content = %q, want preserved %q", got, existing)
		}
		assertEntries(t, directory, finalName)
	})

	t.Run("nonregular final conflicts", func(t *testing.T) {
		directory := t.TempDir()
		final := filepath.Join(directory, finalName)
		if err := os.Mkdir(final, 0o700); err != nil {
			t.Fatal(err)
		}

		if _, err := publish(context.Background(), directory, finalName, contents); !errors.Is(err, errPublishConflict) {
			t.Fatalf("publish error = %v, want errPublishConflict", err)
		}
		info, err := os.Lstat(final)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("final mode = %v, want preserved directory", info.Mode())
		}
		assertEntries(t, directory, finalName)
	})

	t.Run("already canceled", func(t *testing.T) {
		directory := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if _, err := publish(ctx, directory, finalName, contents); !errors.Is(err, errCanceled) {
			t.Fatalf("publish error = %v, want errCanceled", err)
		}
		assertEntries(t, directory)
	})

	t.Run("missing directory", func(t *testing.T) {
		parent := t.TempDir()
		directory := filepath.Join(parent, "missing")

		if _, err := publish(context.Background(), directory, finalName, contents); !errors.Is(err, errStorage) {
			t.Fatalf("publish error = %v, want errStorage", err)
		}
		if _, err := os.Lstat(filepath.Join(directory, finalName)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("final stat error = %v, want not exist", err)
		}
		assertEntries(t, parent)
	})
}

func TestB12(t *testing.T) {
	const recipient = "u-22222222222222222222222222222222"
	tests := []struct {
		name     string
		to       string
		body     string
		wantBody string
		wantErr  error
	}{
		{name: "one byte", to: recipient, body: "x", wantBody: "x"},
		{name: "maximum bytes", to: recipient, body: strings.Repeat("x", 16384), wantBody: strings.Repeat("x", 16384)},
		{name: "empty body", to: recipient, wantErr: errBodyLimit},
		{name: "one byte over", to: recipient, body: strings.Repeat("x", 16385), wantErr: errBodyLimit},
		{name: "multibyte byte count over", to: recipient, body: strings.Repeat("€", 5462), wantErr: errBodyLimit},
		{name: "recipient before body", to: "u-11111111111111111111111111111111", wantErr: errInvalidRecipient},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := validateMessage(recipient, test.to, test.body)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("validateMessage error = %v, want %v", err, test.wantErr)
			}
			if got != test.wantBody {
				t.Errorf("validateMessage body length = %d, want %d", len(got), len(test.wantBody))
			}
		})
	}
}

func TestB5(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []byte
	}{
		{name: "LF", input: "record\n", want: []byte("record")},
		{name: "CRLF removes one CR", input: "record\r\n", want: []byte("record")},
		{name: "nonfinal CR is preserved", input: "a\rb\n", want: []byte("a\rb")},
		{name: "maximum bytes", input: strings.Repeat("x", 131072) + "\n", want: bytes.Repeat([]byte("x"), 131072)},
		{name: "final CR counts within maximum", input: strings.Repeat("x", 131071) + "\r\n", want: bytes.Repeat([]byte("x"), 131071)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := readRecord(bufio.NewReader(strings.NewReader(test.input)))
			if err != nil {
				t.Fatalf("readRecord: %v", err)
			}
			if !bytes.Equal(got, test.want) {
				t.Errorf("readRecord bytes differ: got length %d, want %d", len(got), len(test.want))
			}
		})
	}
}

func TestB6(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  error
	}{
		{name: "oversized record before LF", input: strings.Repeat("x", 131073) + "\n", want: errRecordTooLarge},
		{name: "oversized partial record at EOF", input: strings.Repeat("x", 131073), want: errRecordTooLarge},
		{name: "short partial record at EOF", input: "record", want: errTruncatedRecord},
		{name: "maximum partial record at EOF", input: strings.Repeat("x", 131072), want: errTruncatedRecord},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := readRecord(bufio.NewReader(strings.NewReader(test.input)))
			if err != test.want {
				t.Errorf("readRecord error = %v, want exact sentinel %v", err, test.want)
			}
		})
	}
}

// Forge frozen-test flag B22: standing Continue approval adds fresh-eyes start-time integer-limit cases.
func TestB22(t *testing.T) {
	start := time.Date(2026, time.September, 20, 19, 0, 0, 123000000, time.UTC)
	status := newProcessStatus(start)
	marshal := func(now time.Time) string {
		t.Helper()
		result, err := status.snapshot(now)
		if err != nil {
			t.Fatalf("snapshot status: %v", err)
		}
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal status: %v", err)
		}
		return string(data)
	}

	initial := `{"started_at_ms":1789930800123,"uptime_ms":0,"sent":0,"rejected":0,"last_error":null}`
	if got := marshal(start); got != initial {
		t.Fatalf("initial status = %s, want %s", got, initial)
	}
	if got := marshal(start.Add(-time.Second)); got != initial {
		t.Errorf("status before start = %s, want nonnegative uptime %s", got, initial)
	}

	status.noteSent(false)
	status.noteSent(true)
	status.noteRejected("invalid_request", "not serious")
	if got := marshal(start); got != `{"started_at_ms":1789930800123,"uptime_ms":0,"sent":1,"rejected":1,"last_error":null}` {
		t.Errorf("ordinary rejection status = %s", got)
	}

	serious := []string{"invalid_project", "invalid_event", "storage_error", "integer_limit", "internal_error"}
	for i, code := range serious {
		message := fmt.Sprintf("serious %d", i)
		status.noteRejected(code, message)
		var snapshot struct {
			LastError json.RawMessage `json:"last_error"`
		}
		if err := json.Unmarshal([]byte(marshal(start)), &snapshot); err != nil {
			t.Fatalf("decode status after %s: %v", code, err)
		}
		want := fmt.Sprintf(`{"code":%q,"message":%q}`, code, message)
		if string(snapshot.LastError) != want {
			t.Errorf("last_error after %s = %s, want %s", code, snapshot.LastError, want)
		}
	}
	status.noteRejected("body_limit", "must not replace")
	if got := marshal(start.Add(2500 * time.Millisecond)); got != `{"started_at_ms":1789930800123,"uptime_ms":2500,"sent":1,"rejected":7,"last_error":{"code":"internal_error","message":"serious 4"}}` {
		t.Errorf("updated status = %s", got)
	}

	status.sent = maxSafeInteger
	status.rejected = maxSafeInteger
	status.noteSent(true)
	status.noteRejected("invalid_request", "still not serious")
	if got := marshal(start); got != `{"started_at_ms":1789930800123,"uptime_ms":0,"sent":9007199254740991,"rejected":9007199254740991,"last_error":{"code":"internal_error","message":"serious 4"}}` {
		t.Errorf("saturated status = %s", got)
	}

	for _, startedAtMilliseconds := range []int64{-1, maxSafeInteger + 1} {
		startedAt := time.UnixMilli(startedAtMilliseconds)
		invalidStatus := newProcessStatus(startedAt)
		if _, err := invalidStatus.snapshot(startedAt); err != errIntegerLimit {
			t.Errorf("snapshot start %d error = %v, want errIntegerLimit", startedAtMilliseconds, err)
		}
	}
}

func TestB7(t *testing.T) {
	var decode func([]byte) (decodedRequest, error) = decodeRequest

	tests := []struct {
		name        string
		input       []byte
		wantID      string
		wantOp      string
		wantVersion int
		wantErr     bool
	}{
		{name: "open initialize", input: []byte(`{"id":"open_1","op":"initialize","params":{"versions":[1]}}`), wantID: "open_1", wantOp: "initialize"},
		{name: "create initialize", input: []byte(`{"id":"create-1","op":"initialize","params":{"versions":[1],"create":{"participant_id":"participant","recipient_id":"recipient","recipient_public_key":"key text"}}}`), wantID: "create-1", wantOp: "initialize"},
		{name: "send", input: []byte(`{"version":1,"id":"send_1","op":"send","params":{"to":"recipient","body":"hello"}}`), wantID: "send_1", wantOp: "send", wantVersion: 1},
		{name: "history without after sequence", input: []byte(`{"version":1,"id":"history_1","op":"history","params":{}}`), wantID: "history_1", wantOp: "history", wantVersion: 1},
		{name: "history with after sequence", input: []byte(`{"version":1,"id":"history_2","op":"history","params":{"after_seq":7}}`), wantID: "history_2", wantOp: "history", wantVersion: 1},
		{name: "status", input: []byte(`{"version":1,"id":"status_1","op":"status","params":{}}`), wantID: "status_1", wantOp: "status", wantVersion: 1},
		{name: "shutdown", input: []byte(`{"version":1,"id":"shutdown_1","op":"shutdown","params":{}}`), wantID: "shutdown_1", wantOp: "shutdown", wantVersion: 1},
		{name: "unknown operation is decoded", input: []byte(`{"version":1,"id":"future_1","op":"future_operation","params":{}}`), wantID: "future_1", wantOp: "future_operation", wantVersion: 1},

		{name: "empty", input: []byte{}, wantErr: true},
		{name: "whitespace only", input: []byte(" \t\r\n"), wantErr: true},
		{name: "BOM", input: []byte("\xef\xbb\xbf{\"version\":1,\"id\":\"x\",\"op\":\"status\",\"params\":{}}"), wantErr: true},
		{name: "nonobject root", input: []byte(`[]`), wantErr: true},
		{name: "malformed", input: []byte(`{"id":`), wantErr: true},
		{name: "trailing data", input: []byte(`{"version":1,"id":"x","op":"status","params":{}}x`), wantErr: true},
		{name: "second root", input: []byte(`{"version":1,"id":"x","op":"status","params":{}} {}`), wantErr: true},
		{name: "direct duplicate nested name", input: []byte(`{"version":1,"id":"x","op":"send","params":{"to":"first","to":"second","body":"hello"}}`), wantErr: true},
		{name: "escaped duplicate nested name", input: []byte(`{"version":1,"id":"x","op":"send","params":{"to":"first","\u0074o":"second","body":"hello"}}`), wantErr: true},
		{name: "invalid UTF-8", input: append([]byte(`{"version":1,"id":"`), append([]byte{0xff}, []byte(`","op":"status","params":{}}`)...)...), wantErr: true},
		{name: "isolated high surrogate", input: []byte(`{"version":1,"id":"\ud800","op":"status","params":{}}`), wantErr: true},
		{name: "isolated low surrogate", input: []byte(`{"version":1,"id":"\udc00","op":"status","params":{}}`), wantErr: true},
		{name: "invalid surrogate pair", input: []byte(`{"version":1,"id":"\ud800\u0041","op":"status","params":{}}`), wantErr: true},
		{name: "depth nine", input: []byte(`{"version":1,"id":"x","op":"future","params":{"a":{"b":{"c":{"d":{"e":{"f":{"g":{}}}}}}}}}`), wantErr: true},
		{name: "seventeen object members", input: []byte(`{"version":1,"id":"x","op":"future","params":{"a":0,"b":0,"c":0,"d":0,"e":0,"f":0,"g":0,"h":0,"i":0,"j":0,"k":0,"l":0,"m":0,"n":0,"o":0,"p":0,"q":0}}`), wantErr: true},
		{name: "signed integer", input: []byte(`{"version":1,"id":"x","op":"history","params":{"after_seq":-1}}`), wantErr: true},
		{name: "fractional integer", input: []byte(`{"version":1,"id":"x","op":"history","params":{"after_seq":1.0}}`), wantErr: true},
		{name: "exponential integer", input: []byte(`{"version":1,"id":"x","op":"history","params":{"after_seq":1e0}}`), wantErr: true},
		{name: "negative zero integer", input: []byte(`{"version":1,"id":"x","op":"history","params":{"after_seq":-0}}`), wantErr: true},
		{name: "integer above maximum", input: []byte(`{"version":1,"id":"x","op":"history","params":{"after_seq":9007199254740992}}`), wantErr: true},
		{name: "unknown field", input: []byte(`{"version":1,"id":"x","op":"status","params":{},"extra":true}`), wantErr: true},
		{name: "case-mismatched field", input: []byte(`{"version":1,"ID":"x","op":"status","params":{}}`), wantErr: true},
		{name: "missing field", input: []byte(`{"version":1,"id":"x","op":"status"}`), wantErr: true},
		{name: "null field", input: []byte(`{"version":1,"id":null,"op":"status","params":{}}`), wantErr: true},
		{name: "wrongly typed field", input: []byte(`{"version":1,"id":"x","op":"status","params":[]}`), wantErr: true},
		{name: "initialize params missing versions", input: []byte(`{"id":"x","op":"initialize","params":{}}`), wantErr: true},
		{name: "initialize versions duplicate", input: []byte(`{"id":"x","op":"initialize","params":{"versions":[1,1]}}`), wantErr: true},
		{name: "initialize versions over limit", input: []byte(`{"id":"x","op":"initialize","params":{"versions":[1,2,3,4,5,6,7,8,9]}}`), wantErr: true},
		{name: "create params incomplete", input: []byte(`{"id":"x","op":"initialize","params":{"versions":[1],"create":{"participant_id":"participant","recipient_id":"recipient"}}}`), wantErr: true},
		{name: "send params incomplete", input: []byte(`{"version":1,"id":"x","op":"send","params":{"to":"recipient"}}`), wantErr: true},
		{name: "history params unknown field", input: []byte(`{"version":1,"id":"x","op":"history","params":{"extra":0}}`), wantErr: true},
		{name: "status params not empty", input: []byte(`{"version":1,"id":"x","op":"status","params":{"extra":0}}`), wantErr: true},
		{name: "shutdown params not empty", input: []byte(`{"version":1,"id":"x","op":"shutdown","params":{"extra":0}}`), wantErr: true},
		{name: "empty request ID", input: []byte(`{"version":1,"id":"","op":"status","params":{}}`), wantErr: true},
		{name: "request ID invalid character", input: []byte(`{"version":1,"id":"bad.id","op":"status","params":{}}`), wantErr: true},
		{name: "request ID over limit", input: []byte(`{"version":1,"id":"` + strings.Repeat("a", 65) + `","op":"status","params":{}}`), wantErr: true},
		{name: "empty operation", input: []byte(`{"version":1,"id":"x","op":"","params":{}}`), wantErr: true},
		{name: "uppercase operation", input: []byte(`{"version":1,"id":"x","op":"Status","params":{}}`), wantErr: true},
		{name: "hyphenated operation", input: []byte(`{"version":1,"id":"x","op":"not-valid","params":{}}`), wantErr: true},
		{name: "operation over limit", input: []byte(`{"version":1,"id":"x","op":"` + strings.Repeat("a", 33) + `","params":{}}`), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decode(test.input)
			if test.wantErr {
				if !errors.Is(err, errInvalidRequest) {
					t.Errorf("decodeRequest(%q) error = %v, want errInvalidRequest", test.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeRequest(%q): %v", test.input, err)
			}
			if got.id != test.wantID || got.op != test.wantOp || got.version != test.wantVersion {
				t.Errorf("decodeRequest(%q) id/op/version = %q/%q/%d, want %q/%q/%d", test.input, got.id, got.op, got.version, test.wantID, test.wantOp, test.wantVersion)
			}
		})
	}
}

func TestB8(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		initialized bool
		want        error
	}{
		{name: "initialize offer lacks version 1", input: `{"id":"init_1","op":"initialize","params":{"versions":[2]}}`, want: errUnsupportedVersion},
		{name: "initialize offer validation precedes initialized state", input: `{"id":"init_2","op":"initialize","params":{"versions":[2]}}`, initialized: true, want: errUnsupportedVersion},
		{name: "initialize again", input: `{"id":"init_3","op":"initialize","params":{"versions":[1]}}`, initialized: true, want: errAlreadyInitialized},
		{name: "later operation requires version 1", input: `{"version":2,"id":"status_1","op":"status","params":{}}`, initialized: true, want: errUnsupportedVersion},
		{name: "recognized operation requires initialization", input: `{"version":1,"id":"status_2","op":"status","params":{}}`, want: errNotInitialized},
		{name: "unknown operation when uninitialized", input: `{"version":1,"id":"future_1","op":"future_operation","params":{}}`, want: errUnsupportedOperation},
		{name: "unknown operation when initialized", input: `{"version":1,"id":"future_2","op":"future_operation","params":{}}`, initialized: true, want: errUnsupportedOperation},
		{name: "initialize", input: `{"id":"init_4","op":"initialize","params":{"versions":[1]}}`},
		{name: "send", input: `{"version":1,"id":"send_1","op":"send","params":{"to":"recipient","body":"hello"}}`, initialized: true},
		{name: "history", input: `{"version":1,"id":"history_1","op":"history","params":{}}`, initialized: true},
		{name: "status", input: `{"version":1,"id":"status_3","op":"status","params":{}}`, initialized: true},
		{name: "shutdown", input: `{"version":1,"id":"shutdown_1","op":"shutdown","params":{}}`, initialized: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := decodeRequest([]byte(test.input))
			if err != nil {
				t.Fatalf("decodeRequest(%q): %v", test.input, err)
			}

			err = validateProtocol(request, test.initialized)
			if !errors.Is(err, test.want) {
				t.Errorf("validateProtocol(%q, %t) error = %v, want %v", test.input, test.initialized, err, test.want)
			}
		})
	}
}

func TestB3(t *testing.T) {
	valid := strings.Repeat("a", 32)
	ids := []string{"p-" + valid, "h-" + valid, "s-" + valid}
	tests := []struct {
		name      string
		projectID string
		harnessID string
		sessionID string
		wantErr   bool
	}{
		{name: "valid", projectID: ids[0], harnessID: ids[1], sessionID: ids[2]},
	}

	for kind := range ids {
		for _, invalid := range []struct {
			name string
			id   string
		}{
			{name: "wrong prefix", id: "x-" + valid},
			{name: "31 digits", id: ids[kind][:2] + strings.Repeat("a", 31)},
			{name: "33 digits", id: ids[kind][:2] + strings.Repeat("a", 33)},
			{name: "uppercase", id: ids[kind][:2] + "A" + valid[1:]},
			{name: "nonhex", id: ids[kind][:2] + "g" + valid[1:]},
			{name: "slash", id: ids[kind][:2] + "/" + valid[1:]},
			{name: "backslash", id: ids[kind][:2] + `\` + valid[1:]},
			{name: "dot-dot", id: ids[kind][:2] + ".." + valid[2:]},
		} {
			candidate := append([]string(nil), ids...)
			candidate[kind] = invalid.id
			tests = append(tests, struct {
				name      string
				projectID string
				harnessID string
				sessionID string
				wantErr   bool
			}{
				name:      []string{"project", "harness", "session"}[kind] + " " + invalid.name,
				projectID: candidate[0], harnessID: candidate[1], sessionID: candidate[2], wantErr: true,
			})
		}
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateScopeIDs(test.projectID, test.harnessID, test.sessionID)
			if test.wantErr {
				if !errors.Is(err, errInvalidRequest) {
					t.Errorf("validateScopeIDs(%q, %q, %q) error = %v, want errInvalidRequest", test.projectID, test.harnessID, test.sessionID, err)
				}
				return
			}
			if err != nil {
				t.Errorf("validateScopeIDs(%q, %q, %q): %v", test.projectID, test.harnessID, test.sessionID, err)
			}
		})
	}
}

func TestB1(t *testing.T) {
	const (
		projectID          = "p-00000000000000000000000000000000"
		participantID      = "u-11111111111111111111111111111111"
		recipientID        = "u-22222222222222222222222222222222"
		harnessID          = "h-33333333333333333333333333333333"
		sessionID          = "s-44444444444444444444444444444444"
		recipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	)

	root := filepath.Join(t.TempDir(), ".ihr")
	scope := scopeIDs{projectID: projectID, harnessID: harnessID, sessionID: sessionID}
	create := &projectCreate{
		participantID:      participantID,
		recipientID:        recipientID,
		recipientPublicKey: recipientPublicKey,
	}
	state, err := initializeProject(root, scope, create)
	if err != nil {
		t.Fatalf("initializeProject: %v", err)
	}

	projectDirectory := filepath.Join(root, "projects", projectID)
	seedPath := filepath.Join(projectDirectory, "local", "harnesses", harnessID, "sessions", sessionID, "identity.seed")
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) != ed25519.SeedSize {
		t.Fatalf("seed length = %d, want %d", len(seed), ed25519.SeedSize)
	}

	localPublicBytes := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	localPublicKey := base64.RawURLEncoding.EncodeToString(localPublicBytes)
	localDigest := sha256.Sum256(localPublicBytes)
	localSigningKeyID := "sha256:" + hex.EncodeToString(localDigest[:])
	recipientPublicBytes, err := base64.RawURLEncoding.DecodeString(recipientPublicKey)
	if err != nil || len(recipientPublicBytes) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(recipientPublicBytes) != recipientPublicKey {
		t.Fatalf("recipient public key is not canonical unpadded base64url Ed25519: %v", err)
	}
	recipientDigest := sha256.Sum256(recipientPublicBytes)
	recipientSigningKeyID := "sha256:" + hex.EncodeToString(recipientDigest[:])

	assertState := func(got projectState) {
		t.Helper()
		if got.scope != scope || got.projectEpoch != 1 || got.participantID != participantID ||
			got.publicKey != localPublicKey || got.signingKeyID != localSigningKeyID ||
			got.recipientID != recipientID || got.recipientPublicKey != recipientPublicKey ||
			got.recipientSigningKeyID != recipientSigningKeyID || !bytes.Equal(got.seed, seed) {
			t.Fatal("project state does not contain the generated local identity and configured recipient trust")
		}
	}
	assertState(state)

	type participant struct {
		ParticipantID string `json:"participant_id"`
		PublicKey     string `json:"public_key"`
		SigningKeyID  string `json:"signing_key_id"`
	}
	wantMarker, err := json.Marshal(struct {
		Administrator string        `json:"administrator"`
		Participants  []participant `json:"participants"`
		ProjectEpoch  int           `json:"project_epoch"`
		ProjectID     string        `json:"project_id"`
		Version       int           `json:"version"`
	}{
		Administrator: participantID,
		Participants: []participant{
			{participantID, localPublicKey, localSigningKeyID},
			{recipientID, recipientPublicKey, recipientSigningKeyID},
		},
		ProjectEpoch: 1,
		ProjectID:    projectID,
		Version:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(projectDirectory, "project.json")
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marker, wantMarker) {
		t.Fatalf("project.json = %s, want %s", marker, wantMarker)
	}
	canonicalMarker, err := canonicalizeJSON(marker)
	if err != nil || !bytes.Equal(marker, canonicalMarker) {
		t.Fatalf("project.json is not canonical: %v", err)
	}

	// Publication order is not observable after return; exact complete state excludes events and temporary files.
	wantTree := map[string]bool{
		".":                                  true,
		"projects":                           true,
		filepath.Join("projects", projectID): true,
		filepath.Join("projects", projectID, "events"):                                                                true,
		filepath.Join("projects", projectID, "events", participantID):                                                 true,
		filepath.Join("projects", projectID, "local"):                                                                 true,
		filepath.Join("projects", projectID, "local", "harnesses"):                                                    true,
		filepath.Join("projects", projectID, "local", "harnesses", harnessID):                                         true,
		filepath.Join("projects", projectID, "local", "harnesses", harnessID, "sessions"):                             true,
		filepath.Join("projects", projectID, "local", "harnesses", harnessID, "sessions", sessionID):                  true,
		filepath.Join("projects", projectID, "local", "harnesses", harnessID, "sessions", sessionID, "identity.seed"): false,
		filepath.Join("projects", projectID, "project.json"):                                                          false,
	}
	seen := make(map[string]bool, len(wantTree))
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		wantDirectory, ok := wantTree[relative]
		if !ok {
			return fmt.Errorf("unexpected path %q", relative)
		}
		if entry.IsDir() != wantDirectory {
			return fmt.Errorf("path %q directory = %t, want %t", relative, entry.IsDir(), wantDirectory)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !wantDirectory && !info.Mode().IsRegular() {
			return fmt.Errorf("path %q mode = %v, want regular file", relative, info.Mode())
		}
		if runtime.GOOS != "windows" {
			wantMode := os.FileMode(0o600)
			if wantDirectory {
				wantMode = 0o700
			}
			if info.Mode().Perm() != wantMode {
				return fmt.Errorf("path %q mode = %o, want %o", relative, info.Mode().Perm(), wantMode)
			}
		}
		seen[relative] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(wantTree) {
		t.Fatalf("tree has %d entries, want %d: %v", len(seen), len(wantTree), seen)
	}
}

func TestB2(t *testing.T) {
	const (
		projectID          = "p-00000000000000000000000000000000"
		participantID      = "u-11111111111111111111111111111111"
		recipientID        = "u-22222222222222222222222222222222"
		harnessID          = "h-33333333333333333333333333333333"
		sessionID          = "s-44444444444444444444444444444444"
		recipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	)

	root := filepath.Join(t.TempDir(), ".ihr")
	scope := scopeIDs{projectID: projectID, harnessID: harnessID, sessionID: sessionID}
	created, err := initializeProject(root, scope, &projectCreate{
		participantID:      participantID,
		recipientID:        recipientID,
		recipientPublicKey: recipientPublicKey,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	type treeEntry struct {
		mode     fs.FileMode
		contents []byte
	}
	snapshot := func() map[string]treeEntry {
		t.Helper()
		tree := make(map[string]treeEntry)
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			var contents []byte
			if info.Mode().IsRegular() {
				contents, err = os.ReadFile(path)
				if err != nil {
					return err
				}
			}
			tree[relative] = treeEntry{mode: info.Mode(), contents: contents}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return tree
	}
	before := snapshot()

	opened, err := initializeProject(root, scope, nil)
	if err != nil {
		t.Fatalf("open project: %v", err)
	}
	if opened.scope != created.scope || opened.projectEpoch != created.projectEpoch ||
		opened.participantID != created.participantID || opened.publicKey != created.publicKey ||
		opened.signingKeyID != created.signingKeyID || opened.recipientID != created.recipientID ||
		opened.recipientPublicKey != created.recipientPublicKey ||
		opened.recipientSigningKeyID != created.recipientSigningKeyID || !bytes.Equal(opened.seed, created.seed) {
		t.Fatal("opened project state differs from created scope, epoch, identity, trust, or seed")
	}

	after := snapshot()
	if len(after) != len(before) {
		t.Fatalf("open changed tree entry count from %d to %d", len(before), len(after))
	}
	for path, want := range before {
		got, ok := after[path]
		if !ok || got.mode != want.mode || !bytes.Equal(got.contents, want.contents) {
			t.Fatalf("open changed tree entry %q: got %#v, want %#v", path, got, want)
		}
	}
}

const (
	b4ProjectID          = "p-00000000000000000000000000000000"
	b4ParticipantID      = "u-11111111111111111111111111111111"
	b4RecipientID        = "u-22222222222222222222222222222222"
	b4HarnessID          = "h-33333333333333333333333333333333"
	b4SessionID          = "s-44444444444444444444444444444444"
	b4RecipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
)

type b4Fixture struct {
	root             string
	projectDirectory string
	markerPath       string
	seedPath         string
	eventDirectory   string
}

type b4TreeEntry struct {
	mode fs.FileMode
	data []byte
}

// Forge frozen-test flag B4: standing Continue approval adds R2 root-symlink and events-entry blockers.
func TestB4(t *testing.T) {
	scope, create := b4Inputs()

	t.Run("create with symlink root", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "target")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(base, "selected-root")
		if err := os.Symlink(target, root); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		rootBefore := b4SnapshotTree(t, root)
		targetBefore := b4SnapshotTree(t, target)

		_, gotErr := initializeProject(root, scope, create)

		if !errors.Is(gotErr, errInvalidProject) {
			t.Errorf("initializeProject error = %v, want %v", gotErr, errInvalidProject)
		}
		b4AssertSameTree(t, rootBefore, b4SnapshotTree(t, root))
		b4AssertSameTree(t, targetBefore, b4SnapshotTree(t, target))
	})

	t.Run("create when project directory exists", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), ".ihr")
		if err := os.MkdirAll(filepath.Join(root, "projects", b4ProjectID), 0o700); err != nil {
			t.Fatal(err)
		}
		b4AssertErrorWithoutChanges(t, root, errProjectExists, func() error {
			_, err := initializeProject(root, scope, create)
			return err
		})
	})

	t.Run("retry create preserves partial seed", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), ".ihr")
		seedPath := filepath.Join(root, "projects", b4ProjectID, "local", "harnesses", b4HarnessID, "sessions", b4SessionID, "identity.seed")
		if err := os.MkdirAll(filepath.Dir(seedPath), 0o700); err != nil {
			t.Fatal(err)
		}
		sentinel := bytes.Repeat([]byte{0xa5}, 32)
		if err := os.WriteFile(seedPath, sentinel, 0o600); err != nil {
			t.Fatal(err)
		}
		b4AssertErrorWithoutChanges(t, root, errProjectExists, func() error {
			_, err := initializeProject(root, scope, create)
			return err
		})
		got, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, sentinel) {
			t.Fatalf("partial seed changed: got %x, want %x", got, sentinel)
		}
	})

	t.Run("open missing root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing", ".ihr")
		b4AssertErrorWithoutChanges(t, root, errProjectMissing, func() error {
			_, err := initializeProject(root, scope, nil)
			return err
		})
	})

	t.Run("open missing project", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), ".ihr")
		if err := os.MkdirAll(filepath.Join(root, "projects"), 0o700); err != nil {
			t.Fatal(err)
		}
		b4AssertErrorWithoutChanges(t, root, errProjectMissing, func() error {
			_, err := initializeProject(root, scope, nil)
			return err
		})
	})

	invalidCases := []struct {
		name   string
		mutate func(*testing.T, b4Fixture)
	}{
		{"missing marker", func(t *testing.T, fixture b4Fixture) {
			if err := os.Remove(fixture.markerPath); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing seed", func(t *testing.T, fixture b4Fixture) {
			if err := os.Remove(fixture.seedPath); err != nil {
				t.Fatal(err)
			}
		}},
		{"truncated marker", func(t *testing.T, fixture b4Fixture) {
			b4WriteFile(t, fixture.markerPath, []byte(`{"version":1`))
		}},
		{"oversized marker", func(t *testing.T, fixture b4Fixture) {
			b4WriteFile(t, fixture.markerPath, bytes.Repeat([]byte("x"), maxRecordSize+1))
		}},
		{"noncanonical marker", func(t *testing.T, fixture b4Fixture) {
			marker, err := os.ReadFile(fixture.markerPath)
			if err != nil {
				t.Fatal(err)
			}
			b4WriteFile(t, fixture.markerPath, append(marker, '\n'))
		}},
		{"inconsistent project value", func(t *testing.T, fixture b4Fixture) {
			markerBytes, err := os.ReadFile(fixture.markerPath)
			if err != nil {
				t.Fatal(err)
			}
			var marker projectMarker
			if err := json.Unmarshal(markerBytes, &marker); err != nil {
				t.Fatal(err)
			}
			marker.ProjectID = "p-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			markerBytes, err = json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			markerBytes, err = canonicalizeJSON(markerBytes)
			if err != nil {
				t.Fatal(err)
			}
			b4WriteFile(t, fixture.markerPath, markerBytes)
		}},
		{"symlink marker", func(t *testing.T, fixture b4Fixture) {
			b4ReplaceWithSymlink(t, fixture.root, fixture.markerPath)
		}},
		{"symlink seed", func(t *testing.T, fixture b4Fixture) {
			b4ReplaceWithSymlink(t, fixture.root, fixture.seedPath)
		}},
		{"nonregular managed directory", func(t *testing.T, fixture b4Fixture) {
			if err := os.Remove(fixture.eventDirectory); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.eventDirectory, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"unexpected project directory entry", func(t *testing.T, fixture b4Fixture) {
			if err := os.WriteFile(filepath.Join(fixture.projectDirectory, "unexpected"), []byte("sentinel"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"unexpected events directory entry", func(t *testing.T, fixture b4Fixture) {
			if err := os.Mkdir(filepath.Join(fixture.projectDirectory, "events", "unexpected"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"unexpected nested local directory entry", func(t *testing.T, fixture b4Fixture) {
			if err := os.Mkdir(filepath.Join(fixture.projectDirectory, "local", "unapproved"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"marker over 4096 bytes", func(t *testing.T, fixture b4Fixture) {
			markerBytes, err := os.ReadFile(fixture.markerPath)
			if err != nil {
				t.Fatal(err)
			}
			var marker map[string]any
			if err := json.Unmarshal(markerBytes, &marker); err != nil {
				t.Fatal(err)
			}
			marker["padding"] = ""
			markerBytes, err = json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			marker["padding"] = strings.Repeat("x", 4097-len(markerBytes))
			markerBytes, err = json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := canonicalizeJSON(markerBytes)
			if err != nil || !bytes.Equal(markerBytes, canonical) || len(markerBytes) != 4097 {
				t.Fatalf("oversized project.json length/canonical = %d/%t, want 4097/true: %v", len(markerBytes), bytes.Equal(markerBytes, canonical), err)
			}
			b4WriteFile(t, fixture.markerPath, markerBytes)
		}},
	}
	for _, test := range invalidCases {
		t.Run(test.name, func(t *testing.T) {
			fixture := b4MakeValidProject(t)
			test.mutate(t, fixture)
			b4AssertErrorWithoutChanges(t, fixture.root, errInvalidProject, func() error {
				_, err := initializeProject(fixture.root, scope, nil)
				return err
			})
		})
	}

	t.Run("permission denied marker read", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX permission test")
		}
		fixture := b4MakeValidProject(t)
		before := b4SnapshotTree(t, fixture.root)
		info, err := os.Lstat(fixture.markerPath)
		if err != nil {
			t.Fatal(err)
		}
		originalMode := info.Mode().Perm()
		if err := os.Chmod(fixture.markerPath, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(fixture.markerPath, originalMode) })

		file, probeErr := os.Open(fixture.markerPath)
		if probeErr == nil {
			_ = file.Close()
			t.Skip("platform permits reading a mode-000 file")
		}
		if !errors.Is(probeErr, fs.ErrPermission) {
			t.Fatalf("permission probe error = %v, want fs.ErrPermission", probeErr)
		}
		modeBefore, err := os.Lstat(fixture.markerPath)
		if err != nil {
			t.Fatal(err)
		}
		_, gotErr := initializeProject(fixture.root, scope, nil)
		modeAfter, err := os.Lstat(fixture.markerPath)
		if err != nil {
			t.Fatal(err)
		}
		if modeAfter.Mode() != modeBefore.Mode() {
			t.Fatalf("permission-denied call changed marker mode from %v to %v", modeBefore.Mode(), modeAfter.Mode())
		}
		if err := os.Chmod(fixture.markerPath, originalMode); err != nil {
			t.Fatal(err)
		}
		b4AssertSameTree(t, before, b4SnapshotTree(t, fixture.root))
		if !errors.Is(gotErr, errStorage) {
			t.Fatalf("initializeProject error = %v, want errStorage", gotErr)
		}
	})
}

func b4Inputs() (scopeIDs, *projectCreate) {
	return scopeIDs{projectID: b4ProjectID, harnessID: b4HarnessID, sessionID: b4SessionID}, &projectCreate{
		participantID:      b4ParticipantID,
		recipientID:        b4RecipientID,
		recipientPublicKey: b4RecipientPublicKey,
	}
}

func b4MakeValidProject(t *testing.T) b4Fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".ihr")
	scope, create := b4Inputs()
	if _, err := initializeProject(root, scope, create); err != nil {
		t.Fatalf("create valid project: %v", err)
	}
	projectDirectory := filepath.Join(root, "projects", b4ProjectID)
	return b4Fixture{
		root:             root,
		projectDirectory: projectDirectory,
		markerPath:       filepath.Join(projectDirectory, "project.json"),
		seedPath:         filepath.Join(projectDirectory, "local", "harnesses", b4HarnessID, "sessions", b4SessionID, "identity.seed"),
		eventDirectory:   filepath.Join(projectDirectory, "events", b4ParticipantID),
	}
}

func b4WriteFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func b4ReplaceWithSymlink(t *testing.T, root, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires additional Windows privileges")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(root), "symlink-target")
	if err := os.WriteFile(target, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func b4AssertErrorWithoutChanges(t *testing.T, root string, want error, call func() error) {
	t.Helper()
	before := b4SnapshotTree(t, root)
	got := call()
	after := b4SnapshotTree(t, root)
	b4AssertSameTree(t, before, after)
	if !errors.Is(got, want) {
		t.Fatalf("initializeProject error = %v, want %v", got, want)
	}
}

func b4SnapshotTree(t *testing.T, root string) map[string]b4TreeEntry {
	t.Helper()
	tree := make(map[string]b4TreeEntry)
	if _, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return tree
	} else if err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		var data []byte
		switch {
		case info.Mode().IsRegular():
			data, err = os.ReadFile(path)
		case info.Mode()&fs.ModeSymlink != 0:
			var target string
			target, err = os.Readlink(path)
			data = []byte(target)
		}
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		tree[relative] = b4TreeEntry{mode: info.Mode(), data: data}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return tree
}

func b4AssertSameTree(t *testing.T, want, got map[string]b4TreeEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tree entry count changed from %d to %d", len(want), len(got))
	}
	for path, wantEntry := range want {
		gotEntry, ok := got[path]
		if !ok {
			t.Fatalf("tree entry %q was removed", path)
		}
		if gotEntry.mode != wantEntry.mode || !bytes.Equal(gotEntry.data, wantEntry.data) {
			t.Fatalf("tree entry %q changed: got mode %v bytes %x, want mode %v bytes %x", path, gotEntry.mode, gotEntry.data, wantEntry.mode, wantEntry.data)
		}
	}
}

func TestB19(t *testing.T) {
	const (
		projectID          = "p-00000000000000000000000000000000"
		participantID      = "u-11111111111111111111111111111111"
		recipientID        = "u-22222222222222222222222222222222"
		harnessID          = "h-33333333333333333333333333333333"
		sessionID          = "s-44444444444444444444444444444444"
		recipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	)

	root := filepath.Join(t.TempDir(), ".ihr")
	state, err := initializeProject(root, scopeIDs{
		projectID: projectID,
		harnessID: harnessID,
		sessionID: sessionID,
	}, &projectCreate{
		participantID:      participantID,
		recipientID:        recipientID,
		recipientPublicKey: recipientPublicKey,
	})
	if err != nil {
		t.Fatalf("initializeProject: %v", err)
	}

	empty, err := history(root, state, 0)
	if err != nil {
		t.Fatalf("history before events: %v", err)
	}
	if len(empty.Events) != 0 || empty.NextAfterSequence != 0 {
		t.Fatalf("empty history = %+v, want no events and next_after_seq 0", empty)
	}
	emptyJSON, err := json.Marshal(empty)
	if err != nil || string(emptyJSON) != `{"events":[],"next_after_seq":0}` {
		t.Fatalf("empty history JSON = %s, %v", emptyJSON, err)
	}

	payload1 := []byte(fmt.Sprintf(`{"author":"%s","author_seq":1,"body":"first","created":{"logical":0,"physical_ms":100},"prev":null,"project_epoch":%d,"project_id":"%s","signing_algorithm":"ed25519","signing_key_id":"%s","to":"%s","type":"message","version":1}`,
		state.participantID, state.projectEpoch, state.scope.projectID, state.signingKeyID, state.recipientID))
	envelope1, eventID1, err := signEvent(state.seed, payload1)
	if err != nil {
		t.Fatalf("sign first event: %v", err)
	}
	payload2 := []byte(fmt.Sprintf(`{"author":"%s","author_seq":2,"body":"second","created":{"logical":1,"physical_ms":100},"prev":"%s","project_epoch":%d,"project_id":"%s","signing_algorithm":"ed25519","signing_key_id":"%s","to":"%s","type":"message","version":1}`,
		state.participantID, eventID1, state.projectEpoch, state.scope.projectID, state.signingKeyID, state.recipientID))
	envelope2, eventID2, err := signEvent(state.seed, payload2)
	if err != nil {
		t.Fatalf("sign second event: %v", err)
	}

	eventDirectory := filepath.Join(root, "projects", projectID, "events", state.participantID)
	writeEvent := func(sequence uint64, eventID string, envelope []byte) {
		t.Helper()
		name := fmt.Sprintf("%016d-%s.json", sequence, eventID[len("sha256:"):])
		if err := os.WriteFile(filepath.Join(eventDirectory, name), envelope, 0o600); err != nil {
			t.Fatalf("write event %d: %v", sequence, err)
		}
	}
	writeEvent(1, eventID1, envelope1)
	writeEvent(2, eventID2, envelope2)

	first, err := history(root, state, 0)
	if err != nil {
		t.Fatalf("history after 0: %v", err)
	}
	if len(first.Events) != 1 || first.NextAfterSequence != 1 || first.Events[0].EventID != eventID1 || !bytes.Equal(first.Events[0].Envelope, envelope1) {
		t.Fatalf("history after 0 = %+v, want first exact event and next_after_seq 1", first)
	}
	firstJSON, err := json.Marshal(first)
	wantFirstJSON := fmt.Sprintf(`{"events":[{"event_id":"%s","envelope":%s}],"next_after_seq":1}`, eventID1, envelope1)
	if err != nil || string(firstJSON) != wantFirstJSON {
		t.Fatalf("history after 0 JSON = %s, %v; want %s", firstJSON, err, wantFirstJSON)
	}

	second, err := history(root, state, 1)
	if err != nil {
		t.Fatalf("history after 1: %v", err)
	}
	if len(second.Events) != 1 || second.NextAfterSequence != 2 || second.Events[0].EventID != eventID2 || !bytes.Equal(second.Events[0].Envelope, envelope2) {
		t.Fatalf("history after 1 = %+v, want second exact event and next_after_seq 2", second)
	}
	secondJSON, err := json.Marshal(second)
	wantSecondJSON := fmt.Sprintf(`{"events":[{"event_id":"%s","envelope":%s}],"next_after_seq":2}`, eventID2, envelope2)
	if err != nil || string(secondJSON) != wantSecondJSON {
		t.Fatalf("history after 1 JSON = %s, %v; want %s", secondJSON, err, wantSecondJSON)
	}

	end, err := history(root, state, 2)
	if err != nil {
		t.Fatalf("history after 2: %v", err)
	}
	if len(end.Events) != 0 || end.NextAfterSequence != 2 {
		t.Fatalf("history after 2 = %+v, want no events and next_after_seq 2", end)
	}
	endJSON, err := json.Marshal(end)
	if err != nil || string(endJSON) != `{"events":[],"next_after_seq":2}` {
		t.Fatalf("history after 2 JSON = %s, %v", endJSON, err)
	}
}

type b9Writer struct {
	bytes.Buffer
	calls int
	limit int
	err   error
}

func (writer *b9Writer) Write(p []byte) (int, error) {
	writer.calls++
	if writer.limit > 0 && writer.limit < len(p) {
		p = p[:writer.limit]
	}
	n, _ := writer.Buffer.Write(p)
	return n, writer.err
}

// Forge frozen-test flag B9: human-approved oversized-result correction remains; standing continuation changes only successful exact order to RFC 8785.
func TestB9(t *testing.T) {
	requestID := "request_1"

	assertResponse := func(t *testing.T, operationErr error, result any, want string, replacementError ...bool) {
		t.Helper()
		var output bytes.Buffer
		if err := writeResponse(&output, &requestID, result, operationErr); err != nil {
			t.Fatalf("writeResponse error = %v", err)
		}
		if got := output.String(); got != want {
			t.Fatalf("response = %q, want %q", got, want)
		}
		line := output.Bytes()
		if len(line) == 0 || line[len(line)-1] != '\n' || bytes.Count(line, []byte{'\n'}) != 1 {
			t.Fatalf("response is not exactly one LF-terminated line: %q", line)
		}
		line = line[:len(line)-1]
		if len(line) > 131072 {
			t.Fatalf("response length = %d, want <= 131072", len(line))
		}

		var object map[string]json.RawMessage
		if err := json.Unmarshal(line, &object); err != nil {
			t.Fatalf("response JSON: %v", err)
		}
		wantKeys := []string{"version", "id", "ok", "result"}
		wantError := operationErr != nil || len(replacementError) != 0 && replacementError[0]
		if wantError {
			wantKeys = []string{"version", "id", "ok", "error"}
		}
		if len(object) != len(wantKeys) {
			t.Fatalf("response fields = %v, want exactly %v", object, wantKeys)
		}
		for _, key := range wantKeys {
			if _, ok := object[key]; !ok {
				t.Fatalf("response lacks %q: %s", key, line)
			}
		}
		if string(object["version"]) != "1" || string(object["id"]) != `"request_1"` {
			t.Fatalf("response version/id = %s/%s", object["version"], object["id"])
		}
		if !wantError {
			if string(object["ok"]) != "true" {
				t.Fatalf("success ok = %s", object["ok"])
			}
			return
		}
		if string(object["ok"]) != "false" {
			t.Fatalf("error ok = %s", object["ok"])
		}
		var publicError map[string]string
		if err := json.Unmarshal(object["error"], &publicError); err != nil || len(publicError) != 2 || publicError["code"] == "" || publicError["message"] == "" {
			t.Fatalf("error schema = %s: %v", object["error"], err)
		}
	}

	assertResponse(t, nil, struct {
		Value string `json:"value"`
	}{"accepted"}, `{"id":"request_1","ok":true,"result":{"value":"accepted"},"version":1}`+"\n")

	stableErrors := []struct {
		err     error
		code    string
		message string
	}{
		{errInvalidRequest, "invalid_request", "Invalid request."},
		{errRecordTooLarge, "record_too_large", "Record exceeds limit."},
		{errTruncatedRecord, "truncated_record", "Record lacks LF."},
		{errUnsupportedVersion, "unsupported_version", "Version 1 is required."},
		{errNotInitialized, "not_initialized", "Initialize first."},
		{errAlreadyInitialized, "already_initialized", "Connection is initialized."},
		{errUnsupportedOperation, "unsupported_operation", "Operation is unavailable."},
		{errProjectExists, "project_exists", "Project already exists."},
		{errProjectMissing, "project_missing", "Project does not exist."},
		{errInvalidProject, "invalid_project", "Project state is invalid."},
		{errInvalidRecipient, "invalid_recipient", "Recipient is not registered."},
		{errBodyLimit, "body_limit", "Body length is invalid."},
		{errIntegerLimit, "integer_limit", "Event integer exceeds limit."},
		{errInvalidEvent, "invalid_event", "Stored event is invalid."},
		{errStorage, "storage_error", "Storage operation failed."},
		{errCanceled, "canceled", "Operation was canceled."},
		{errInternalError, "internal_error", "Operation failed."},
	}
	for _, test := range stableErrors {
		t.Run(test.code, func(t *testing.T) {
			want := `{"version":1,"id":"request_1","ok":false,"error":{"code":"` + test.code + `","message":"` + test.message + `"}}` + "\n"
			assertResponse(t, test.err, nil, want)
			assertResponse(t, errors.Join(errors.New("context"), test.err), nil, want)
		})
	}

	assertResponse(t, errors.New("private failure"), nil, `{"version":1,"id":"request_1","ok":false,"error":{"code":"internal_error","message":"Operation failed."}}`+"\n")
	assertResponse(t, nil, strings.Repeat("x", 131072), `{"version":1,"id":"request_1","ok":false,"error":{"code":"internal_error","message":"Operation failed."}}`+"\n", true)

	var nilIDOutput bytes.Buffer
	if err := writeResponse(&nilIDOutput, nil, nil, errInvalidRequest); err != nil {
		t.Fatal(err)
	}
	if got, want := nilIDOutput.String(), `{"version":1,"id":null,"ok":false,"error":{"code":"invalid_request","message":"Invalid request."}}`+"\n"; got != want {
		t.Fatalf("nil-ID response = %q, want %q", got, want)
	}
	if line := nilIDOutput.Bytes(); len(line)-1 > 131072 || line[len(line)-1] != '\n' || bytes.Count(line, []byte{'\n'}) != 1 {
		t.Fatalf("nil-ID response is not one bounded LF-terminated line: %q", line)
	}
	var nilIDSchema struct {
		Version int             `json:"version"`
		ID      json.RawMessage `json:"id"`
		OK      bool            `json:"ok"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSuffix(nilIDOutput.Bytes(), []byte{'\n'}), &nilIDSchema); err != nil || nilIDSchema.Version != 1 || string(nilIDSchema.ID) != "null" || nilIDSchema.OK || nilIDSchema.Error.Code != "invalid_request" || nilIDSchema.Error.Message != "Invalid request." {
		t.Fatalf("nil-ID error schema = %+v: %v", nilIDSchema, err)
	}

	writeFailure := errors.New("write failed")
	for _, writer := range []*b9Writer{{err: writeFailure}, {limit: 1}} {
		err := writeResponse(writer, &requestID, map[string]any{"ok": true}, nil)
		if !errors.Is(err, errOutput) {
			t.Errorf("writeResponse error = %v, want errOutput", err)
		}
		if writer.calls != 1 {
			t.Errorf("writer calls = %d, want 1", writer.calls)
		}
	}
}

type b23Writer struct {
	calls int
	limit int
	err   error
}

func (writer *b23Writer) Write(p []byte) (int, error) {
	writer.calls++
	if writer.limit > 0 && writer.limit < len(p) {
		return writer.limit, writer.err
	}
	return len(p), writer.err
}

func TestB23(t *testing.T) {
	var logRecord func(io.Writer, time.Time, slog.Level, string, logFields) = writeLog
	now := time.Date(2026, time.September, 21, 14, 59, 44, 636000000, time.UTC)
	requestID := "request_1"
	bodyText := "BODY_RESPONSE_TEXT_7f3c"
	rawErrorText := "RAW_ERROR_TEXT_4a91"
	keyText := "PRIVATE_KEY_TEXT_53bd"
	signatureText := "SIGNATURE_TEXT_28ce"
	pathText := "/private/PATH_TEXT_9d12"

	var stdout, stderr bytes.Buffer
	if err := writeResponse(&stdout, &requestID, map[string]string{"body": bodyText}, nil); err != nil {
		t.Fatal(err)
	}
	wantResponse := `{"id":"request_1","ok":true,"result":{"body":"` + bodyText + `"},"version":1}` + "\n"
	if got := stdout.String(); got != wantResponse {
		t.Fatalf("stdout = %q, want protocol response %q", got, wantResponse)
	}
	if bytes.Count(stdout.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("stdout is not one protocol line: %q", stdout.Bytes())
	}

	allFields := logFields{
		projectID:     "p-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		participantID: "u-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		harnessID:     "h-cccccccccccccccccccccccccccccccc",
		sessionID:     "s-dddddddddddddddddddddddddddddddd",
		requestID:     requestID,
		eventID:       "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		code:          "invalid_request",
	}
	messages := []string{"startup", "initialize", "send", "history", "status", "shutdown", "request_rejected", "recovery_warning", "internal_error"}
	levels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	optionalCounts := map[string]int{}
	for index, message := range messages {
		fields := logFields{}
		if message == "send" {
			fields = allFields
		}
		start := stderr.Len()
		logRecord(&stderr, now, levels[index%len(levels)], message, fields)
		line := stderr.Bytes()[start:]
		if len(line) == 0 || len(line)-1 > 2048 || line[len(line)-1] != '\n' || bytes.Count(line, []byte{'\n'}) != 1 {
			t.Fatalf("%s log is not one bounded LF-terminated record: %q", message, line)
		}

		var record map[string]json.RawMessage
		if err := json.Unmarshal(line[:len(line)-1], &record); err != nil {
			t.Fatalf("%s log JSON: %v", message, err)
		}
		wantFields := 3
		if message == "send" {
			wantFields = 10
		}
		if len(record) != wantFields || string(record["time"]) != `"2026-09-21T14:59:44.636Z"` || string(record["msg"]) != `"`+message+`"` {
			t.Fatalf("%s log fields = %s", message, line)
		}
		wantLevel := []string{"debug", "info", "warn", "error"}[index%len(levels)]
		if string(record["level"]) != `"`+wantLevel+`"` {
			t.Errorf("%s level = %s, want %q", message, record["level"], wantLevel)
		}
		for _, name := range []string{"project_id", "participant_id", "harness_id", "session_id", "request_id", "event_id", "code"} {
			if _, ok := record[name]; ok {
				optionalCounts[name]++
			}
		}
	}
	for name, count := range optionalCounts {
		if count != 1 {
			t.Errorf("optional field %s occurs %d times, want once", name, count)
		}
	}
	if len(optionalCounts) != 7 {
		t.Fatalf("optional fields = %v, want all seven allowed fields", optionalCounts)
	}

	minimal := `{"time":"2026-09-21T14:59:44.636Z","level":"error","msg":"internal_error"}` + "\n"
	unsafe := []struct {
		name    string
		message string
		fields  logFields
		text    string
	}{
		{"invalid message", rawErrorText, logFields{}, rawErrorText},
		{"oversized message", strings.Repeat(keyText, 3), logFields{}, keyText},
		{"project ID", "send", logFields{projectID: pathText}, pathText},
		{"participant ID", "send", logFields{participantID: bodyText}, bodyText},
		{"harness ID", "send", logFields{harnessID: keyText}, keyText},
		{"session ID", "send", logFields{sessionID: rawErrorText}, rawErrorText},
		{"request ID", "send", logFields{requestID: pathText}, pathText},
		{"event ID", "send", logFields{eventID: signatureText}, signatureText},
		{"code", "send", logFields{code: rawErrorText}, rawErrorText},
	}
	for _, test := range unsafe {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logRecord(&output, now, slog.LevelInfo, test.message, test.fields)
			if got := output.String(); got != minimal {
				t.Fatalf("unsafe log = %q, want fixed minimal %q", got, minimal)
			}
			if strings.Contains(output.String(), test.text) {
				t.Errorf("unsafe input leaked: %q", output.String())
			}
		})
	}

	for _, text := range []string{bodyText, rawErrorText, keyText, signatureText, pathText} {
		if strings.Contains(stderr.String(), text) {
			t.Errorf("stderr contains sensitive text %q: %s", text, stderr.Bytes())
		}
	}
	if strings.Contains(stdout.String(), `"time"`) || strings.Contains(stdout.String(), `"level"`) || strings.Contains(stdout.String(), `"msg"`) {
		t.Errorf("stdout contains log text: %q", stdout.Bytes())
	}

	writeFailure := errors.New("write failed")
	for _, writer := range []*b23Writer{{err: writeFailure}, {limit: 1}} {
		logRecord(writer, now, slog.LevelInfo, "status", logFields{})
		if writer.calls != 1 {
			t.Errorf("stderr writer calls = %d, want one best-effort attempt", writer.calls)
		}
	}
}

func TestB24(t *testing.T) {
	const (
		projectID          = "p-00000000000000000000000000000000"
		participantID      = "u-11111111111111111111111111111111"
		recipientID        = "u-22222222222222222222222222222222"
		harnessID          = "h-33333333333333333333333333333333"
		sessionID          = "s-44444444444444444444444444444444"
		recipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	)

	root := filepath.Join(t.TempDir(), ".ihr")
	scope := scopeIDs{projectID: projectID, harnessID: harnessID, sessionID: sessionID}
	_, err := initializeProject(root, scope, &projectCreate{
		participantID:      participantID,
		recipientID:        recipientID,
		recipientPublicKey: recipientPublicKey,
	})
	if err != nil {
		t.Fatalf("initializeProject: %v", err)
	}

	type treeEntry struct {
		mode     fs.FileMode
		contents []byte
	}
	snapshot := func() map[string]treeEntry {
		t.Helper()
		tree := make(map[string]treeEntry)
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			var contents []byte
			if info.Mode().IsRegular() {
				contents, err = os.ReadFile(path)
				if err != nil {
					return err
				}
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			tree[relative] = treeEntry{mode: info.Mode(), contents: contents}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return tree
	}
	before := snapshot()
	eventDirectory := filepath.Join(root, "projects", projectID, "events", participantID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, test := range []struct {
		name    string
		to      string
		body    string
		wantErr error
	}{
		{name: "canceled", to: recipientID, body: "hello", wantErr: errCanceled},
		{name: "recipient before cancellation", to: participantID, body: "hello", wantErr: errInvalidRecipient},
		{name: "body before cancellation", to: recipientID, wantErr: errBodyLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			var outcome sendOutcome
			outcome, err := sendMessage(ctx, root, scope, test.to, test.body, 100)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("sendMessage error = %v, want %v", err, test.wantErr)
			}
			if outcome.eventID != "" {
				t.Errorf("sendMessage event ID = %q, want empty", outcome.eventID)
			}
			entries, err := os.ReadDir(eventDirectory)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("author event directory = %v, want empty", entries)
			}
			after := snapshot()
			if !reflect.DeepEqual(after, before) {
				for path, want := range before {
					got, ok := after[path]
					if !ok || got.mode != want.mode || !bytes.Equal(got.contents, want.contents) {
						t.Fatalf("send changed tree entry %q: got %#v, want %#v", path, got, want)
					}
				}
				t.Fatalf("send changed tree paths: got %v, want %v", after, before)
			}
		})
	}
}

type b21Writer struct {
	accepted []byte
}

func (writer *b21Writer) Write(value []byte) (int, error) {
	writer.accepted = append(writer.accepted, value...)
	return len(value), errors.New("output failed")
}

func TestB21(t *testing.T) {
	const (
		projectID          = "p-00000000000000000000000000000000"
		participantID      = "u-11111111111111111111111111111111"
		recipientID        = "u-22222222222222222222222222222222"
		harnessID          = "h-33333333333333333333333333333333"
		sessionID          = "s-44444444444444444444444444444444"
		recipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	)

	root := filepath.Join(t.TempDir(), ".ihr")
	scope := scopeIDs{projectID: projectID, harnessID: harnessID, sessionID: sessionID}
	_, err := initializeProject(root, scope, &projectCreate{
		participantID:      participantID,
		recipientID:        recipientID,
		recipientPublicKey: recipientPublicKey,
	})
	if err != nil {
		t.Fatalf("initializeProject: %v", err)
	}

	var outcome sendOutcome
	outcome, err = sendMessage(context.Background(), root, scope, recipientID, "hello", 1700000000000)
	if err != nil {
		t.Fatalf("sendMessage: %v", err)
	}
	if outcome.eventID == "" {
		t.Fatal("sendMessage returned an empty event ID")
	}
	if outcome.recoveryWarning {
		t.Fatal("sendMessage returned a recovery warning for a normal send")
	}

	eventDirectory := filepath.Join(root, "projects", projectID, "events", participantID)
	entries, err := os.ReadDir(eventDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || validTemporaryName(entries[0].Name()) || !entries[0].Type().IsRegular() {
		t.Fatalf("event directory after send = %v, want one final regular event and no temporary", entries)
	}
	beforeOutput, err := os.Stat(filepath.Join(eventDirectory, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}

	writer := new(b21Writer)
	requestID := "send_1"
	if err := writeResponse(writer, &requestID, map[string]string{"event_id": outcome.eventID}, nil); !errors.Is(err, errOutput) {
		t.Fatalf("writeResponse error = %v, want errOutput", err)
	}
	if len(writer.accepted) == 0 {
		t.Fatal("failing writer accepted no response bytes")
	}

	reopened, err := initializeProject(root, scope, nil)
	if err != nil {
		t.Fatalf("reopen project: %v", err)
	}
	got, err := history(root, reopened, 0)
	if err != nil {
		t.Fatalf("history after restart: %v", err)
	}
	if len(got.Events) != 1 || got.NextAfterSequence != 1 || got.Events[0].EventID != outcome.eventID || len(got.Events[0].Envelope) == 0 {
		t.Fatalf("history after restart = %+v, want one complete event %q", got, outcome.eventID)
	}

	afterEntries, err := os.ReadDir(eventDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEntries) != 1 || afterEntries[0].Name() != entries[0].Name() || validTemporaryName(afterEntries[0].Name()) {
		t.Fatalf("event directory after output failure = %v, want unchanged final event only", afterEntries)
	}
	afterOutput, err := os.Stat(filepath.Join(eventDirectory, afterEntries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeOutput, afterOutput) {
		t.Fatal("output failure removed or replaced the published event")
	}
}

const (
	b20ProjectID          = "p-00000000000000000000000000000000"
	b20ParticipantID      = "u-11111111111111111111111111111111"
	b20RecipientID        = "u-22222222222222222222222222222222"
	b20HarnessID          = "h-33333333333333333333333333333333"
	b20SessionID          = "s-44444444444444444444444444444444"
	b20RecipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
)

type b20Event struct {
	name     string
	eventID  string
	envelope []byte
}

type b20Fixture struct {
	root   string
	scope  scopeIDs
	state  projectState
	dir    string
	events []b20Event
}

type b20Final struct {
	mode   fs.FileMode
	size   int64
	digest [sha256.Size]byte
}

func TestB20(t *testing.T) {
	const otherProjectID = "p-ffffffffffffffffffffffffffffffff"
	const wrongPredecessor = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

	cases := []struct {
		name       string
		eventCount int
		mutate     func(*testing.T, *b20Fixture)
	}{
		{
			name:       "changed envelope byte",
			eventCount: 1,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				changed := bytes.Replace(fixture.events[0].envelope, []byte(`"body":"event-1"`), []byte(`"body":"Event-1"`), 1)
				if bytes.Equal(changed, fixture.events[0].envelope) {
					t.Fatal("fixture body byte was not changed")
				}
				b20Write(t, filepath.Join(fixture.dir, fixture.events[0].name), changed)
			},
		},
		{
			name:       "wrong final digest name",
			eventCount: 1,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				old := fixture.events[0]
				digest := old.name[17 : 17+64]
				replacement := "0"
				if digest[0] == '0' {
					replacement = "1"
				}
				name := old.name[:17] + replacement + digest[1:] + ".json"
				if err := os.Rename(filepath.Join(fixture.dir, old.name), filepath.Join(fixture.dir, name)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "unsafe final type",
			eventCount: 1,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				path := filepath.Join(fixture.dir, fixture.events[0].name)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "duplicate sequence fork",
			eventCount: 1,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				b20StoreEvent(t, fixture, 1, nil, 100, 0, "fork", nil)
			},
		},
		{
			name:       "gap",
			eventCount: 2,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				if err := os.Remove(filepath.Join(fixture.dir, fixture.events[0].name)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "bad canonical signature",
			eventCount: 1,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				event := fixture.events[0]
				envelope := append([]byte(nil), event.envelope...)
				start := bytes.Index(envelope, []byte(`"signature":"`)) + len(`"signature":"`)
				if start < len(`"signature":"`) {
					t.Fatal("signature not found")
				}
				if envelope[start] == 'A' {
					envelope[start] = 'B'
				} else {
					envelope[start] = 'A'
				}
				b20ReplaceFile(t, fixture, 0, envelope)
			},
		},
		{
			name:       "wrong trusted payload value",
			eventCount: 1,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				b20ReplaceEvent(t, fixture, 0, nil, 100, 0, func(payload map[string]any) {
					payload["project_id"] = otherProjectID
				})
			},
		},
		{
			name:       "predecessor mismatch",
			eventCount: 2,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				b20ReplaceEvent(t, fixture, 1, b20String(wrongPredecessor), 100, 1, nil)
			},
		},
		{
			name:       "non-increasing HLC",
			eventCount: 2,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				b20ReplaceEvent(t, fixture, 1, b20String(fixture.events[0].eventID), 100, 0, nil)
			},
		},
		{
			name:       "physical advance with nonzero logical",
			eventCount: 2,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				b20ReplaceEvent(t, fixture, 1, b20String(fixture.events[0].eventID), 101, 1, nil)
			},
		},
		{
			name:       "same physical with logical jump",
			eventCount: 2,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				b20ReplaceEvent(t, fixture, 1, b20String(fixture.events[0].eventID), 100, 2, nil)
			},
		},
		{
			name:       "oversized stored event",
			eventCount: 1,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				b20Write(t, filepath.Join(fixture.dir, fixture.events[0].name), bytes.Repeat([]byte{'x'}, maxRecordSize+1))
			},
		},
		{
			name:       "unexpected directory entry",
			eventCount: 1,
			mutate: func(t *testing.T, fixture *b20Fixture) {
				b20Write(t, filepath.Join(fixture.dir, "unexpected"), []byte("diagnostic"))
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := b20ValidFixture(t, test.eventCount)
			test.mutate(t, fixture)
			before := b20Finals(t, fixture.dir)

			got, err := history(fixture.root, fixture.state, 0)
			if !errors.Is(err, errInvalidEvent) {
				t.Errorf("history error = %v, want errInvalidEvent", err)
			}
			if len(got.Events) != 0 || got.NextAfterSequence != 0 {
				t.Errorf("history returned %d events and next_after_seq %d, want zero event content", len(got.Events), got.NextAfterSequence)
			}
			b20AssertFinals(t, fixture.dir, before, "history")

			var outcome sendOutcome
			outcome, err = sendMessage(context.Background(), fixture.root, fixture.scope, fixture.state.recipientID, "new", 200)
			if !errors.Is(err, errInvalidEvent) {
				t.Errorf("sendMessage error = %v, want errInvalidEvent", err)
			}
			if outcome.eventID != "" {
				t.Errorf("sendMessage event ID = %q, want empty", outcome.eventID)
			}
			b20AssertFinals(t, fixture.dir, before, "sendMessage")
		})
	}

	t.Run("exact temporary name is ignored", func(t *testing.T) {
		fixture := b20ValidFixture(t, 1)
		temporary := filepath.Join(fixture.dir, ".tmp-00000000000000000000000000000000")
		b20Write(t, temporary, []byte("incomplete"))

		got, err := history(fixture.root, fixture.state, 0)
		if err != nil || len(got.Events) != 1 || got.Events[0].EventID != fixture.events[0].eventID {
			t.Fatalf("history with exact temporary name = %+v, %v", got, err)
		}
		var outcome sendOutcome
		outcome, err = sendMessage(context.Background(), fixture.root, fixture.scope, fixture.state.recipientID, "new", 200)
		if err != nil || outcome.eventID == "" {
			t.Fatalf("sendMessage with exact temporary name = %q, %v", outcome.eventID, err)
		}
		if outcome.recoveryWarning {
			t.Fatal("sendMessage returned a recovery warning for a normal send")
		}
		contents, err := os.ReadFile(temporary)
		if err != nil || string(contents) != "incomplete" {
			t.Fatalf("temporary file = %q, %v; want unchanged", contents, err)
		}
		got, err = history(fixture.root, fixture.state, 1)
		if err != nil || len(got.Events) != 1 || got.Events[0].EventID != outcome.eventID {
			t.Fatalf("history after send with exact temporary name = %+v, %v", got, err)
		}
	})
}

func b20ValidFixture(t *testing.T, eventCount int) *b20Fixture {
	t.Helper()
	fixture := &b20Fixture{
		root:  filepath.Join(t.TempDir(), ".ihr"),
		scope: scopeIDs{projectID: b20ProjectID, harnessID: b20HarnessID, sessionID: b20SessionID},
	}
	state, err := initializeProject(fixture.root, fixture.scope, &projectCreate{
		participantID:      b20ParticipantID,
		recipientID:        b20RecipientID,
		recipientPublicKey: b20RecipientPublicKey,
	})
	if err != nil {
		t.Fatalf("initializeProject: %v", err)
	}
	fixture.state = state
	fixture.dir = filepath.Join(fixture.root, "projects", b20ProjectID, "events", b20ParticipantID)

	var predecessor *string
	for sequence := 1; sequence <= eventCount; sequence++ {
		event := b20StoreEvent(t, fixture, uint64(sequence), predecessor, 100, uint64(sequence-1), fmt.Sprintf("event-%d", sequence), nil)
		fixture.events = append(fixture.events, event)
		predecessor = b20String(event.eventID)
	}
	got, err := history(fixture.root, fixture.state, 0)
	if err != nil || len(got.Events) != 1 || got.Events[0].EventID != fixture.events[0].eventID {
		t.Fatalf("independently valid history = %+v, %v", got, err)
	}
	return fixture
}

func b20StoreEvent(t *testing.T, fixture *b20Fixture, sequence uint64, predecessor *string, physical, logical uint64, body string, mutate func(map[string]any)) b20Event {
	t.Helper()
	payload := map[string]any{
		"author":            fixture.state.participantID,
		"author_seq":        sequence,
		"body":              body,
		"created":           map[string]any{"logical": logical, "physical_ms": physical},
		"prev":              predecessor,
		"project_epoch":     fixture.state.projectEpoch,
		"project_id":        fixture.state.scope.projectID,
		"signing_algorithm": "ed25519",
		"signing_key_id":    fixture.state.signingKeyID,
		"to":                fixture.state.recipientID,
		"type":              "message",
		"version":           1,
	}
	if mutate != nil {
		mutate(payload)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	envelope, eventID, err := signEvent(fixture.state.seed, encoded)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%016d-%s.json", sequence, eventID[len("sha256:"):])
	b20Write(t, filepath.Join(fixture.dir, name), envelope)
	return b20Event{name: name, eventID: eventID, envelope: envelope}
}

func b20ReplaceEvent(t *testing.T, fixture *b20Fixture, index int, predecessor *string, physical, logical uint64, mutate func(map[string]any)) {
	t.Helper()
	old := fixture.events[index]
	if err := os.Remove(filepath.Join(fixture.dir, old.name)); err != nil {
		t.Fatal(err)
	}
	fixture.events[index] = b20StoreEvent(t, fixture, uint64(index+1), predecessor, physical, logical, fmt.Sprintf("event-%d", index+1), mutate)
}

func b20ReplaceFile(t *testing.T, fixture *b20Fixture, index int, contents []byte) {
	t.Helper()
	old := fixture.events[index]
	if err := os.Remove(filepath.Join(fixture.dir, old.name)); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	name := fmt.Sprintf("%016d-%s.json", index+1, hex.EncodeToString(digest[:]))
	b20Write(t, filepath.Join(fixture.dir, name), contents)
	fixture.events[index] = b20Event{name: name, eventID: "sha256:" + hex.EncodeToString(digest[:]), envelope: contents}
}

func b20Write(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func b20Finals(t *testing.T, directory string) map[string]b20Final {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	finals := make(map[string]b20Final)
	for _, entry := range entries {
		if _, ok := parseStoredEventName(entry.Name()); !ok {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		var digest [sha256.Size]byte
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			digest = sha256.Sum256(contents)
		}
		finals[entry.Name()] = b20Final{mode: info.Mode(), size: info.Size(), digest: digest}
	}
	return finals
}

func b20AssertFinals(t *testing.T, directory string, want map[string]b20Final, operation string) {
	t.Helper()
	if got := b20Finals(t, directory); !maps.Equal(got, want) {
		t.Errorf("%s changed final files: got %d, want %d", operation, len(got), len(want))
	}
}

func b20String(value string) *string {
	return &value
}

func TestB10(t *testing.T) {
	const (
		projectID          = "p-00000000000000000000000000000000"
		participantID      = "u-11111111111111111111111111111111"
		recipientID        = "u-22222222222222222222222222222222"
		harnessID          = "h-33333333333333333333333333333333"
		sessionID          = "s-44444444444444444444444444444444"
		recipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
	)

	scope := scopeIDs{projectID: projectID, harnessID: harnessID, sessionID: sessionID}
	newConfig := func(t *testing.T) connectionConfig {
		t.Helper()
		return connectionConfig{root: filepath.Join(t.TempDir(), ".ihr"), scope: scope}
	}
	initialize := `{"id":"initialize_1","op":"initialize","params":{"versions":[2,1],"create":{"participant_id":"` + participantID + `","recipient_id":"` + recipientID + `","recipient_public_key":"` + recipientPublicKey + `"}}}`
	status := `{"version":1,"id":"status_1","op":"status","params":{}}`
	shutdown := `{"version":1,"id":"shutdown_1","op":"shutdown","params":{}}`

	parseJSONL := func(t *testing.T, output []byte) []map[string]json.RawMessage {
		t.Helper()
		if len(output) == 0 {
			return nil
		}
		if output[len(output)-1] != '\n' {
			t.Fatalf("output is not LF terminated: %q", output)
		}
		lines := bytes.Split(output[:len(output)-1], []byte{'\n'})
		responses := make([]map[string]json.RawMessage, len(lines))
		for i, line := range lines {
			if len(line) == 0 {
				t.Fatalf("output line %d is empty", i)
			}
			if err := json.Unmarshal(line, &responses[i]); err != nil {
				t.Fatalf("output line %d is not JSON: %v", i, err)
			}
		}
		return responses
	}
	requireFields := func(t *testing.T, object map[string]json.RawMessage, names ...string) {
		t.Helper()
		if len(object) != len(names) {
			t.Fatalf("fields = %v, want exactly %v", object, names)
		}
		for _, name := range names {
			if _, ok := object[name]; !ok {
				t.Fatalf("object lacks %q: %v", name, object)
			}
		}
	}
	assertSuccess := func(t *testing.T, response map[string]json.RawMessage, wantID string) json.RawMessage {
		t.Helper()
		requireFields(t, response, "version", "id", "ok", "result")
		var version int
		var id string
		var ok bool
		if err := json.Unmarshal(response["version"], &version); err != nil || version != 1 {
			t.Fatalf("success version = %s, want 1: %v", response["version"], err)
		}
		if err := json.Unmarshal(response["id"], &id); err != nil || id != wantID {
			t.Fatalf("success id = %s, want %q: %v", response["id"], wantID, err)
		}
		if err := json.Unmarshal(response["ok"], &ok); err != nil || !ok {
			t.Fatalf("success ok = %s, want true: %v", response["ok"], err)
		}
		return response["result"]
	}
	assertError := func(t *testing.T, response map[string]json.RawMessage, wantID *string, wantCode, wantMessage string) {
		t.Helper()
		requireFields(t, response, "version", "id", "ok", "error")
		var version int
		var ok bool
		if err := json.Unmarshal(response["version"], &version); err != nil || version != 1 {
			t.Fatalf("error version = %s, want 1: %v", response["version"], err)
		}
		if wantID == nil {
			if string(response["id"]) != "null" {
				t.Fatalf("error id = %s, want null", response["id"])
			}
		} else {
			var id string
			if err := json.Unmarshal(response["id"], &id); err != nil || id != *wantID {
				t.Fatalf("error id = %s, want %q: %v", response["id"], *wantID, err)
			}
		}
		if err := json.Unmarshal(response["ok"], &ok); err != nil || ok {
			t.Fatalf("error ok = %s, want false: %v", response["ok"], err)
		}
		var public map[string]string
		if err := json.Unmarshal(response["error"], &public); err != nil {
			t.Fatalf("error body = %s: %v", response["error"], err)
		}
		if len(public) != 2 || public["code"] != wantCode || public["message"] != wantMessage {
			t.Fatalf("error body = %v, want code %q and message %q", public, wantCode, wantMessage)
		}
	}

	t.Run("clean EOF", func(t *testing.T) {
		var output bytes.Buffer
		if exit := operateConnection(context.Background(), strings.NewReader(""), &output, io.Discard, newConfig(t)); exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}
		if output.Len() != 0 {
			t.Fatalf("output = %q, want none", output.Bytes())
		}
	})

	t.Run("complete invalid request continues to EOF", func(t *testing.T) {
		var output bytes.Buffer
		if exit := operateConnection(context.Background(), strings.NewReader("{}\n"), &output, io.Discard, newConfig(t)); exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}
		responses := parseJSONL(t, output.Bytes())
		if len(responses) != 1 {
			t.Fatalf("responses = %d, want 1", len(responses))
		}
		assertError(t, responses[0], nil, "invalid_request", "Invalid request.")
	})

	for _, test := range []struct {
		name    string
		input   string
		code    string
		message string
	}{
		{name: "truncated framing", input: "{}", code: "truncated_record", message: "Record lacks LF."},
		{name: "oversized framing", input: strings.Repeat("x", maxRecordSize+1) + "\n", code: "record_too_large", message: "Record exceeds limit."},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if exit := operateConnection(context.Background(), strings.NewReader(test.input), &output, io.Discard, newConfig(t)); exit != 2 {
				t.Fatalf("exit = %d, want 2", exit)
			}
			responses := parseJSONL(t, output.Bytes())
			if len(responses) != 1 {
				t.Fatalf("responses = %d, want 1", len(responses))
			}
			assertError(t, responses[0], nil, test.code, test.message)
		})
	}

	t.Run("initialize status shutdown", func(t *testing.T) {
		input := strings.Join([]string{
			initialize,
			status,
			shutdown,
			`{"version":1,"id":"after_shutdown","op":"status","params":{}}`,
		}, "\n") + "\n"
		var output bytes.Buffer
		if exit := operateConnection(context.Background(), strings.NewReader(input), &output, io.Discard, newConfig(t)); exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}
		responses := parseJSONL(t, output.Bytes())
		if len(responses) != 3 {
			t.Fatalf("responses = %d, want 3 before trailing record", len(responses))
		}

		initializeResult := assertSuccess(t, responses[0], "initialize_1")
		var initialized map[string]json.RawMessage
		if err := json.Unmarshal(initializeResult, &initialized); err != nil {
			t.Fatalf("initialize result: %v", err)
		}
		requireFields(t, initialized, "project_id", "project_epoch", "participant_id", "public_key", "signing_key_id", "recipient_id", "capabilities")
		if string(initialized["project_id"]) != `"`+projectID+`"` || string(initialized["project_epoch"]) != "1" ||
			string(initialized["participant_id"]) != `"`+participantID+`"` || string(initialized["recipient_id"]) != `"`+recipientID+`"` {
			t.Fatalf("initialize fixed result fields = %s", initializeResult)
		}
		var publicKey, signingKeyID string
		if err := json.Unmarshal(initialized["public_key"], &publicKey); err != nil || publicKey == "" {
			t.Fatalf("initialize public_key = %s: %v", initialized["public_key"], err)
		}
		if err := json.Unmarshal(initialized["signing_key_id"], &signingKeyID); err != nil || signingKeyID == "" {
			t.Fatalf("initialize signing_key_id = %s: %v", initialized["signing_key_id"], err)
		}
		var capabilities []string
		if err := json.Unmarshal(initialized["capabilities"], &capabilities); err != nil {
			t.Fatalf("initialize capabilities: %v", err)
		}
		wantCapabilities := []string{"send", "history", "status", "shutdown"}
		if len(capabilities) != len(wantCapabilities) {
			t.Fatalf("capabilities = %v, want %v", capabilities, wantCapabilities)
		}
		for i := range wantCapabilities {
			if capabilities[i] != wantCapabilities[i] {
				t.Fatalf("capabilities = %v, want ordered %v", capabilities, wantCapabilities)
			}
		}

		statusResult := assertSuccess(t, responses[1], "status_1")
		var snapshot map[string]json.RawMessage
		if err := json.Unmarshal(statusResult, &snapshot); err != nil {
			t.Fatalf("status result: %v", err)
		}
		requireFields(t, snapshot, "started_at_ms", "uptime_ms", "sent", "rejected", "last_error")
		if string(snapshot["sent"]) != "0" || string(snapshot["rejected"]) != "0" || string(snapshot["last_error"]) != "null" {
			t.Fatalf("initial status = %s", statusResult)
		}
		if result := assertSuccess(t, responses[2], "shutdown_1"); string(result) != "{}" {
			t.Fatalf("shutdown result = %s, want {}", result)
		}
	})

	t.Run("second initialize is recoverable", func(t *testing.T) {
		input := initialize + "\n" + `{"id":"initialize_2","op":"initialize","params":{"versions":[1]}}` + "\n" + shutdown + "\n"
		var output bytes.Buffer
		if exit := operateConnection(context.Background(), strings.NewReader(input), &output, io.Discard, newConfig(t)); exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}
		responses := parseJSONL(t, output.Bytes())
		if len(responses) != 3 {
			t.Fatalf("responses = %d, want 3", len(responses))
		}
		assertSuccess(t, responses[0], "initialize_1")
		secondID := "initialize_2"
		assertError(t, responses[1], &secondID, "already_initialized", "Connection is initialized.")
		if result := assertSuccess(t, responses[2], "shutdown_1"); string(result) != "{}" {
			t.Fatalf("shutdown result = %s, want {}", result)
		}
	})

	for _, test := range []struct {
		name   string
		writer *b9Writer
	}{
		{name: "response writer error", writer: &b9Writer{err: errors.New("write failed")}},
		{name: "response short write", writer: &b9Writer{limit: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if exit := operateConnection(context.Background(), strings.NewReader("{}\n"), test.writer, io.Discard, newConfig(t)); exit != 1 {
				t.Fatalf("exit = %d, want 1", exit)
			}
			if test.writer.calls != 1 {
				t.Fatalf("writer calls = %d, want 1 without retry", test.writer.calls)
			}
		})
	}
}

type b11UnreadReader struct {
	calls int
}

func (reader *b11UnreadReader) Read([]byte) (int, error) {
	reader.calls++
	return 0, errors.New("direct command read input")
}

func TestB11(t *testing.T) {
	const (
		projectID          = "p-00000000000000000000000000000000"
		participantID      = "u-11111111111111111111111111111111"
		recipientID        = "u-22222222222222222222222222222222"
		harnessID          = "h-33333333333333333333333333333333"
		sessionID          = "s-44444444444444444444444444444444"
		recipientPublicKey = "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"
		body               = "B11 direct body 7f3c"
	)

	root := filepath.Join(t.TempDir(), ".ihr")
	scopeArgs := []string{
		"--root", root,
		"--project-id", projectID,
		"--harness-id", harnessID,
		"--session-id", sessionID,
	}
	arguments := func(tail ...string) []string {
		return append(append([]string(nil), scopeArgs...), tail...)
	}

	parseJSONL := func(t *testing.T, data []byte) []map[string]json.RawMessage {
		t.Helper()
		if len(data) == 0 || data[len(data)-1] != '\n' {
			t.Fatalf("output is not nonempty LF-terminated JSONL: %q", data)
		}
		lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
		records := make([]map[string]json.RawMessage, len(lines))
		for index, line := range lines {
			if len(line) == 0 || len(line) > maxRecordSize {
				t.Fatalf("output line %d length = %d", index, len(line))
			}
			if err := json.Unmarshal(line, &records[index]); err != nil {
				t.Fatalf("output line %d is not a JSON object: %v", index, err)
			}
		}
		return records
	}
	requireFields := func(t *testing.T, object map[string]json.RawMessage, names ...string) {
		t.Helper()
		if len(object) != len(names) {
			t.Fatalf("fields = %v, want exactly %v", object, names)
		}
		for _, name := range names {
			if _, ok := object[name]; !ok {
				t.Fatalf("object lacks %q: %v", name, object)
			}
		}
	}
	assertLogs := func(t *testing.T, data []byte, forbidden ...string) {
		t.Helper()
		if len(data) == 0 || data[len(data)-1] != '\n' {
			t.Fatalf("stderr is not nonempty LF-terminated JSONL: %q", data)
		}
		allowedFields := map[string]bool{
			"time": true, "level": true, "msg": true,
			"project_id": true, "participant_id": true, "harness_id": true,
			"session_id": true, "request_id": true, "event_id": true, "code": true,
		}
		allowedMessages := map[string]bool{
			"startup": true, "initialize": true, "send": true, "history": true,
			"status": true, "shutdown": true, "request_rejected": true,
			"recovery_warning": true, "internal_error": true,
		}
		for index, line := range bytes.Split(data[:len(data)-1], []byte{'\n'}) {
			if len(line) == 0 || len(line) > maxLogSize {
				t.Fatalf("stderr line %d length = %d", index, len(line))
			}
			var record map[string]json.RawMessage
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatalf("stderr line %d is not a JSON object: %v", index, err)
			}
			for name := range record {
				if !allowedFields[name] {
					t.Fatalf("stderr line %d has protocol/private field %q: %s", index, name, line)
				}
			}
			var timestamp, level, message string
			if err := json.Unmarshal(record["time"], &timestamp); err != nil {
				t.Fatalf("stderr line %d time: %v", index, err)
			}
			if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
				t.Fatalf("stderr line %d time %q: %v", index, timestamp, err)
			}
			if err := json.Unmarshal(record["level"], &level); err != nil ||
				(level != "debug" && level != "info" && level != "warn" && level != "error") {
				t.Fatalf("stderr line %d level = %s: %v", index, record["level"], err)
			}
			if err := json.Unmarshal(record["msg"], &message); err != nil || !allowedMessages[message] {
				t.Fatalf("stderr line %d msg = %s: %v", index, record["msg"], err)
			}
		}
		for _, text := range forbidden {
			if text != "" && bytes.Contains(data, []byte(text)) {
				t.Fatalf("stderr contains private/protocol text %q: %s", text, data)
			}
		}
	}
	oneResponse := func(t *testing.T, output []byte) map[string]json.RawMessage {
		t.Helper()
		records := parseJSONL(t, output)
		if len(records) != 1 {
			t.Fatalf("responses = %d, want exactly 1", len(records))
		}
		return records[0]
	}
	assertSuccess := func(t *testing.T, response map[string]json.RawMessage, wantID string) json.RawMessage {
		t.Helper()
		requireFields(t, response, "version", "id", "ok", "result")
		var version int
		var id string
		var ok bool
		if err := json.Unmarshal(response["version"], &version); err != nil || version != 1 {
			t.Fatalf("success version = %s: %v", response["version"], err)
		}
		if err := json.Unmarshal(response["id"], &id); err != nil || id != wantID {
			t.Fatalf("success id = %s, want %q: %v", response["id"], wantID, err)
		}
		if err := json.Unmarshal(response["ok"], &ok); err != nil || !ok {
			t.Fatalf("success ok = %s: %v", response["ok"], err)
		}
		return response["result"]
	}
	assertError := func(t *testing.T, response map[string]json.RawMessage, wantCode, wantMessage string) {
		t.Helper()
		requireFields(t, response, "version", "id", "ok", "error")
		if string(response["version"]) != "1" || string(response["ok"]) != "false" {
			t.Fatalf("error version/ok = %s/%s", response["version"], response["ok"])
		}
		if string(response["id"]) != "null" {
			var id string
			if err := json.Unmarshal(response["id"], &id); err != nil || id == "" {
				t.Fatalf("error id = %s: %v", response["id"], err)
			}
		}
		var public map[string]string
		if err := json.Unmarshal(response["error"], &public); err != nil || len(public) != 2 ||
			public["code"] != wantCode || public["message"] != wantMessage {
			t.Fatalf("error = %s, want %s/%s: %v", response["error"], wantCode, wantMessage, err)
		}
	}

	type directResult struct {
		response map[string]json.RawMessage
		logs     []byte
	}
	runDirect := func(t *testing.T, wantExit int, tail ...string) directResult {
		t.Helper()
		input := &b11UnreadReader{}
		var output, logs bytes.Buffer
		if exit := Run(context.Background(), arguments(tail...), input, &output, &logs); exit != wantExit {
			t.Fatalf("Run(%q) exit = %d, want %d", tail, exit, wantExit)
		}
		if input.calls != 0 {
			t.Fatalf("Run(%q) read direct input %d times", tail, input.calls)
		}
		assertLogs(t, logs.Bytes(), body, recipientPublicKey, root, `"params"`)
		return directResult{oneResponse(t, output.Bytes()), append([]byte(nil), logs.Bytes()...)}
	}

	initializeParams := `{"versions":[2,1],"create":{"participant_id":"` + participantID + `","recipient_id":"` + recipientID + `","recipient_public_key":"` + recipientPublicKey + `"}}`
	initialized := assertSuccess(t, runDirect(t, 0, "initialize", "--id", "initialize_1", "--params", initializeParams).response, "initialize_1")
	var initializeResult map[string]json.RawMessage
	if err := json.Unmarshal(initialized, &initializeResult); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	requireFields(t, initializeResult, "project_id", "project_epoch", "participant_id", "public_key", "signing_key_id", "recipient_id", "capabilities")
	if string(initializeResult["project_id"]) != `"`+projectID+`"` || string(initializeResult["project_epoch"]) != "1" ||
		string(initializeResult["participant_id"]) != `"`+participantID+`"` || string(initializeResult["recipient_id"]) != `"`+recipientID+`"` {
		t.Fatalf("initialize fixed fields = %s", initialized)
	}
	var publicKey, signingKeyID string
	var capabilities []string
	if err := json.Unmarshal(initializeResult["public_key"], &publicKey); err != nil || publicKey == "" {
		t.Fatalf("initialize public_key = %s: %v", initializeResult["public_key"], err)
	}
	if err := json.Unmarshal(initializeResult["signing_key_id"], &signingKeyID); err != nil || !strings.HasPrefix(signingKeyID, "sha256:") {
		t.Fatalf("initialize signing_key_id = %s: %v", initializeResult["signing_key_id"], err)
	}
	if err := json.Unmarshal(initializeResult["capabilities"], &capabilities); err != nil ||
		len(capabilities) != 4 || capabilities[0] != "send" || capabilities[1] != "history" || capabilities[2] != "status" || capabilities[3] != "shutdown" {
		t.Fatalf("initialize capabilities = %s: %v", initializeResult["capabilities"], err)
	}

	sent := assertSuccess(t, runDirect(t, 0, "send", "--id", "send_1", "--params", `{"to":"`+recipientID+`","body":"`+body+`"}`).response, "send_1")
	var sendResult map[string]json.RawMessage
	if err := json.Unmarshal(sent, &sendResult); err != nil {
		t.Fatalf("send result: %v", err)
	}
	requireFields(t, sendResult, "event_id")
	var eventID string
	if err := json.Unmarshal(sendResult["event_id"], &eventID); err != nil || len(eventID) != len("sha256:")+64 || !strings.HasPrefix(eventID, "sha256:") {
		t.Fatalf("send event_id = %s: %v", sendResult["event_id"], err)
	}

	historyOutput := assertSuccess(t, runDirect(t, 0, "history", "--id", "history_1", "--params", `{}`).response, "history_1")
	var historyObject map[string]json.RawMessage
	if err := json.Unmarshal(historyOutput, &historyObject); err != nil {
		t.Fatalf("history result: %v", err)
	}
	requireFields(t, historyObject, "events", "next_after_seq")
	if string(historyObject["next_after_seq"]) != "1" {
		t.Fatalf("history next_after_seq = %s, want 1", historyObject["next_after_seq"])
	}
	var events []map[string]json.RawMessage
	if err := json.Unmarshal(historyObject["events"], &events); err != nil || len(events) != 1 {
		t.Fatalf("history events = %s: %v", historyObject["events"], err)
	}
	requireFields(t, events[0], "event_id", "envelope")
	if string(events[0]["event_id"]) != `"`+eventID+`"` {
		t.Fatalf("history event_id = %s, want %q", events[0]["event_id"], eventID)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(events[0]["envelope"], &envelope); err != nil {
		t.Fatalf("history envelope: %v", err)
	}
	requireFields(t, envelope, "payload", "signature")
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(envelope["payload"], &payload); err != nil {
		t.Fatalf("history payload: %v", err)
	}
	requireFields(t, payload, "author", "author_seq", "body", "created", "prev", "project_epoch", "project_id", "signing_algorithm", "signing_key_id", "to", "type", "version")
	if string(payload["body"]) != `"`+body+`"` || string(payload["to"]) != `"`+recipientID+`"` ||
		string(payload["project_id"]) != `"`+projectID+`"` || string(payload["author_seq"]) != "1" {
		t.Fatalf("history payload = %s", envelope["payload"])
	}

	statusOutput := assertSuccess(t, runDirect(t, 0, "status", "--id", "status_1", "--params", `{}`).response, "status_1")
	var status map[string]json.RawMessage
	if err := json.Unmarshal(statusOutput, &status); err != nil {
		t.Fatalf("status result: %v", err)
	}
	requireFields(t, status, "started_at_ms", "uptime_ms", "sent", "rejected", "last_error")
	if string(status["sent"]) != "0" || string(status["rejected"]) != "0" || string(status["last_error"]) != "null" {
		t.Fatalf("direct status = %s", statusOutput)
	}

	shutdownOutput := assertSuccess(t, runDirect(t, 0, "shutdown", "--id", "shutdown_1", "--params", `{}`).response, "shutdown_1")
	if string(shutdownOutput) != "{}" {
		t.Fatalf("shutdown result = %s, want {}", shutdownOutput)
	}

	for _, test := range []struct {
		name string
		tail []string
	}{
		{name: "missing command", tail: nil},
		{name: "missing id", tail: []string{"status", "--params", `{}`}},
		{name: "missing params", tail: []string{"status", "--id", "missing_params"}},
		{name: "malformed params", tail: []string{"status", "--id", "bad_params", "--params", `{`}},
		{name: "malformed flag", tail: []string{"status", "--id", "bad_flag", "--params", `{}`, "--unknown"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertError(t, runDirect(t, 2, test.tail...).response, "invalid_request", "Invalid request.")
		})
	}

	missingRoot := filepath.Join(t.TempDir(), ".ihr")
	missingArgs := []string{"--root", missingRoot, "--project-id", projectID, "--harness-id", harnessID, "--session-id", sessionID,
		"status", "--id", "missing_project", "--params", `{}`}
	missingInput := &b11UnreadReader{}
	var missingOutput, missingLogs bytes.Buffer
	if exit := Run(context.Background(), missingArgs, missingInput, &missingOutput, &missingLogs); exit != 2 {
		t.Fatalf("missing project exit = %d, want 2", exit)
	}
	if missingInput.calls != 0 {
		t.Fatalf("missing project read direct input %d times", missingInput.calls)
	}
	assertLogs(t, missingLogs.Bytes(), body, recipientPublicKey, missingRoot, `"params"`)
	assertError(t, oneResponse(t, missingOutput.Bytes()), "project_missing", "Project does not exist.")

	assertError(t, runDirect(t, 2, "status", "--id", "oversized", "--params", strings.Repeat("x", maxRecordSize+1)).response,
		"record_too_large", "Record exceeds limit.")
	assertError(t, runDirect(t, 2, "future", "--id", "unknown", "--params", `{}`).response,
		"invalid_request", "Invalid request.")

	outputFailureInput := &b11UnreadReader{}
	outputFailure := &b9Writer{err: errors.New("write failed")}
	var outputFailureLogs bytes.Buffer
	if exit := Run(context.Background(), arguments("status", "--id", "output_failure", "--params", `{}`), outputFailureInput, outputFailure, &outputFailureLogs); exit != 1 {
		t.Fatalf("output failure exit = %d, want 1", exit)
	}
	if outputFailureInput.calls != 0 || outputFailure.calls != 1 {
		t.Fatalf("output failure input reads/writes = %d/%d, want 0/1", outputFailureInput.calls, outputFailure.calls)
	}
	assertLogs(t, outputFailureLogs.Bytes(), body, recipientPublicKey, root, `"params"`)

	stdioInput := strings.NewReader(strings.Join([]string{
		`{"id":"stdio_initialize","op":"initialize","params":{"versions":[1]}}`,
		`{"version":1,"id":"stdio_shutdown","op":"shutdown","params":{}}`,
	}, "\n") + "\n")
	var stdioOutput, stdioLogs bytes.Buffer
	if exit := Run(context.Background(), arguments("stdio"), stdioInput, &stdioOutput, &stdioLogs); exit != 0 {
		t.Fatalf("stdio exit = %d, want 0", exit)
	}
	stdioResponses := parseJSONL(t, stdioOutput.Bytes())
	if len(stdioResponses) != 2 {
		t.Fatalf("stdio responses = %d, want 2", len(stdioResponses))
	}
	assertSuccess(t, stdioResponses[0], "stdio_initialize")
	if result := assertSuccess(t, stdioResponses[1], "stdio_shutdown"); string(result) != "{}" {
		t.Fatalf("stdio shutdown result = %s, want {}", result)
	}
	assertLogs(t, stdioLogs.Bytes(), body, recipientPublicKey, root, `"params"`)

	var _ io.Reader = (*b11UnreadReader)(nil)
}

const (
	b25ProjectID     = "p-00000000000000000000000000000025"
	b25ParticipantID = "u-11111111111111111111111111111125"
	b25RecipientID   = "u-22222222222222222222222222222225"
	b25HarnessID     = "h-33333333333333333333333333333325"
	b25SessionID     = "s-44444444444444444444444444444425"
	b25Body          = "B25 subprocess <>&  body"
	b25MaxResponse   = 131072
	b25MaxLog        = 2048
)

type b25Run struct {
	stdout []byte
	stderr []byte
	input  []string
}

type b25ExpectedLog struct {
	level   string
	message string
	fields  map[string]string
}

// Forge frozen-test flag B25: standing continuation adds literal HTML-sensitive characters and U+2028 to the signed body.
func TestB25(t *testing.T) {
	buildDir := t.TempDir()
	binary := filepath.Join(buildDir, "relay")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/relay")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real cmd/relay executable: %v\n%s", err, output)
	}

	storageParent := t.TempDir()
	root := filepath.Join(storageParent, "state")
	processDir := t.TempDir()
	scope := []string{
		"--root", root,
		"--project-id", b25ProjectID,
		"--harness-id", b25HarnessID,
		"--session-id", b25SessionID,
		"stdio",
	}

	recipientSeed := make([]byte, ed25519.SeedSize)
	for index := range recipientSeed {
		recipientSeed[index] = byte(index + 1)
	}
	recipientPublic := ed25519.NewKeyFromSeed(recipientSeed).Public().(ed25519.PublicKey)
	recipientPublicText := base64.RawURLEncoding.EncodeToString(recipientPublic)

	process1Input := []string{
		fmt.Sprintf(`{"id":"p1_initialize","op":"initialize","params":{"versions":[1],"create":{"participant_id":"%s","recipient_id":"%s","recipient_public_key":"%s"}}}`, b25ParticipantID, b25RecipientID, recipientPublicText),
		fmt.Sprintf(`{"version":1,"id":"p1_send","op":"send","params":{"to":"%s","body":"%s"}}`, b25RecipientID, b25Body),
		`{"version":1,"id":"p1_shutdown","op":"shutdown","params":{}}`,
	}
	process1 := b25RunProcess(t, binary, processDir, scope, process1Input)
	responses1 := b25JSONLines(t, "process 1 stdout", process1.stdout, b25MaxResponse)
	if len(responses1) != 3 {
		t.Fatalf("process 1 responses = %d, want 3", len(responses1))
	}
	publicKey, signingKeyID := b25CheckInitialize(t, b25Success(t, responses1[0], "p1_initialize"))
	eventID := b25CheckSend(t, b25Success(t, responses1[1], "p1_send"))
	b25CheckShutdown(t, b25Success(t, responses1[2], "p1_shutdown"))

	eventPath := filepath.Join(root, "projects", b25ProjectID, "events", b25ParticipantID,
		"0000000000000001-"+strings.TrimPrefix(eventID, "sha256:")+".json")
	canonicalEnvelope, err := os.ReadFile(eventPath)
	if err != nil {
		t.Fatalf("retain process 1 stored envelope: %v", err)
	}

	process2Input := []string{
		`{"id":"p2_initialize","op":"initialize","params":{"versions":[1]}}`,
		`{"version":1,"id":"p2_history","op":"history","params":{}}`,
		`{"version":1,"id":"p2_shutdown","op":"shutdown","params":{}}`,
	}
	process2 := b25RunProcess(t, binary, processDir, scope, process2Input)
	responses2 := b25JSONLines(t, "process 2 stdout", process2.stdout, b25MaxResponse)
	if len(responses2) != 3 {
		t.Fatalf("process 2 responses = %d, want 3", len(responses2))
	}
	restartedPublicKey, restartedSigningKeyID := b25CheckInitialize(t, b25Success(t, responses2[0], "p2_initialize"))
	if restartedPublicKey != publicKey || restartedSigningKeyID != signingKeyID {
		t.Fatalf("restart identity changed: public/signing key %q/%q, want %q/%q", restartedPublicKey, restartedSigningKeyID, publicKey, signingKeyID)
	}
	restartedEnvelope, canonicalPayload, signature := b25CheckHistory(t, b25Success(t, responses2[1], "p2_history"), eventID, publicKey, signingKeyID)
	b25CheckShutdown(t, b25Success(t, responses2[2], "p2_shutdown"))
	if !bytes.Equal(restartedEnvelope, canonicalEnvelope) {
		t.Fatalf("restart envelope differs from process 1 stored bytes\nprocess 1: %s\nrestart:   %s", canonicalEnvelope, restartedEnvelope)
	}

	publicBytes := b25RawBase64(t, "initialization public key", publicKey, ed25519.PublicKeySize)
	signatureBytes := b25RawBase64(t, "event signature", signature, ed25519.SignatureSize)
	if !ed25519.Verify(ed25519.PublicKey(publicBytes), append([]byte("IHR-EVENT-V1\x00"), canonicalPayload...), signatureBytes) {
		t.Fatal("independent Ed25519 verification failed")
	}
	publicDigest := sha256.Sum256(publicBytes)
	if want := "sha256:" + hex.EncodeToString(publicDigest[:]); signingKeyID != want {
		t.Fatalf("signing key ID = %q, want %q", signingKeyID, want)
	}
	envelopeDigest := sha256.Sum256(canonicalEnvelope)
	if want := "sha256:" + hex.EncodeToString(envelopeDigest[:]); eventID != want {
		t.Fatalf("event ID = %q, want %q", eventID, want)
	}

	b25CheckLogs(t, "process 1", process1, []b25ExpectedLog{
		{level: "info", message: "startup"},
		{level: "info", message: "initialize", fields: b25SuccessLogFields("p1_initialize", "")},
		{level: "info", message: "send", fields: b25SuccessLogFields("p1_send", eventID)},
		{level: "info", message: "shutdown", fields: b25SuccessLogFields("p1_shutdown", "")},
		{level: "info", message: "shutdown"},
	}, b25Body, recipientPublicText, publicKey, signingKeyID, signature, string(canonicalEnvelope), root, eventPath)
	b25CheckLogs(t, "process 2", process2, []b25ExpectedLog{
		{level: "info", message: "startup"},
		{level: "info", message: "initialize", fields: b25SuccessLogFields("p2_initialize", "")},
		{level: "info", message: "history", fields: b25SuccessLogFields("p2_history", "")},
		{level: "info", message: "shutdown", fields: b25SuccessLogFields("p2_shutdown", "")},
		{level: "info", message: "shutdown"},
	}, b25Body, recipientPublicText, publicKey, signingKeyID, signature, string(canonicalEnvelope), root, eventPath)

	tampered := append([]byte(nil), canonicalEnvelope...)
	bodyAt := bytes.Index(tampered, []byte(b25Body))
	if bodyAt < 0 || bytes.LastIndex(tampered, []byte(b25Body)) != bodyAt {
		t.Fatalf("stored envelope does not contain exactly one unchanged body: %s", tampered)
	}
	tampered[bodyAt+len(b25Body)-1] = 'z'
	changed := 0
	for index := range tampered {
		if tampered[index] != canonicalEnvelope[index] {
			changed++
		}
	}
	if changed != 1 || len(tampered) == 0 || len(tampered) > b25MaxResponse || !json.Valid(tampered) {
		t.Fatalf("tamper changed %d bytes or made unbounded/unparseable JSON", changed)
	}
	if err := os.WriteFile(eventPath, tampered, 0o600); err != nil {
		t.Fatalf("tamper stored event: %v", err)
	}
	beforeProcess3 := b25Snapshot(t, filepath.Dir(eventPath))

	process3Input := []string{
		`{"id":"p3_initialize","op":"initialize","params":{"versions":[1]}}`,
		`{"version":1,"id":"p3_history","op":"history","params":{}}`,
		`{"version":1,"id":"p3_shutdown","op":"shutdown","params":{}}`,
	}
	process3 := b25RunProcess(t, binary, processDir, scope, process3Input)
	responses3 := b25JSONLines(t, "process 3 stdout", process3.stdout, b25MaxResponse)
	if len(responses3) != 3 {
		t.Fatalf("process 3 responses = %d, want 3", len(responses3))
	}
	thirdPublicKey, thirdSigningKeyID := b25CheckInitialize(t, b25Success(t, responses3[0], "p3_initialize"))
	if thirdPublicKey != publicKey || thirdSigningKeyID != signingKeyID {
		t.Fatal("second restart changed identity")
	}
	b25Error(t, responses3[1], "p3_history", "invalid_event", "Stored event is invalid.")
	b25CheckShutdown(t, b25Success(t, responses3[2], "p3_shutdown"))
	for _, forbidden := range [][]byte{[]byte(b25Body), canonicalEnvelope, []byte(eventID), []byte(`"events"`), []byte(`"envelope"`)} {
		if bytes.Contains(process3.stdout, forbidden) {
			t.Fatalf("process 3 invalid_event response exposes event content %q: %s", forbidden, process3.stdout)
		}
	}
	if after := b25Snapshot(t, filepath.Dir(eventPath)); !reflect.DeepEqual(after, beforeProcess3) {
		t.Fatalf("process 3 changed or published event files\nbefore: %#v\nafter:  %#v", beforeProcess3, after)
	}
	b25CheckLogs(t, "process 3", process3, []b25ExpectedLog{
		{level: "info", message: "startup"},
		{level: "info", message: "initialize", fields: b25SuccessLogFields("p3_initialize", "")},
		{level: "warn", message: "request_rejected", fields: map[string]string{"request_id": "p3_history", "code": "invalid_event"}},
		{level: "info", message: "shutdown", fields: b25SuccessLogFields("p3_shutdown", "")},
		{level: "info", message: "shutdown"},
	}, b25Body, recipientPublicText, publicKey, signingKeyID, signature, string(canonicalEnvelope), root, eventPath)

	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if len(name) == len(".tmp-")+32 && strings.HasPrefix(name, ".tmp-") && b25LowerHex(name[len(".tmp-"):]) {
			t.Errorf("temporary file remains in final tree: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk final tree: %v", err)
	}
}

func b25RunProcess(t *testing.T, binary, directory string, args, input []string) b25Run {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Stdin = strings.NewReader(strings.Join(input, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run relay process: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.Bytes(), stderr.Bytes())
	}
	if ctx.Err() != nil {
		t.Fatalf("relay process timeout: %v", ctx.Err())
	}
	return b25Run{stdout: append([]byte(nil), stdout.Bytes()...), stderr: append([]byte(nil), stderr.Bytes()...), input: append([]string(nil), input...)}
}

func b25JSONLines(t *testing.T, name string, data []byte, limit int) []map[string]json.RawMessage {
	t.Helper()
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("%s is not nonempty LF-terminated JSONL: %q", name, data)
	}
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	objects := make([]map[string]json.RawMessage, len(lines))
	for index, line := range lines {
		if len(line) == 0 || len(line) > limit {
			t.Fatalf("%s line %d length = %d, want 1..%d", name, index, len(line), limit)
		}
		b25StrictJSON(t, fmt.Sprintf("%s line %d", name, index), line)
		if err := json.Unmarshal(line, &objects[index]); err != nil || objects[index] == nil {
			t.Fatalf("%s line %d is not a JSON object: %v", name, index, err)
		}
	}
	return objects
}

func b25StrictJSON(t *testing.T, name string, data []byte) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := b25JSONValue(decoder, 1); err != nil {
		t.Fatalf("%s is not strict JSON: %v", name, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		t.Fatalf("%s has trailing JSON data: %v", name, err)
	}
}

func b25JSONValue(decoder *json.Decoder, depth int) error {
	if depth > 8 {
		return fmt.Errorf("nesting exceeds 8")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or non-string object name %q", name)
			}
			seen[name] = true
			if len(seen) > 16 {
				return fmt.Errorf("object has more than 16 members")
			}
			if err := b25JSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("object close: %v", err)
		}
	case '[':
		for decoder.More() {
			if err := b25JSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("array close: %v", err)
		}
	default:
		return fmt.Errorf("unexpected delimiter %q", delimiter)
	}
	return nil
}

func b25Fields(t *testing.T, object map[string]json.RawMessage, names ...string) {
	t.Helper()
	if len(object) != len(names) {
		t.Fatalf("fields = %v, want exactly %v", b25Names(object), names)
	}
	for _, name := range names {
		if _, ok := object[name]; !ok {
			t.Fatalf("object lacks %q: %v", name, object)
		}
	}
}

func b25Names(object map[string]json.RawMessage) []string {
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func b25Success(t *testing.T, response map[string]json.RawMessage, id string) json.RawMessage {
	t.Helper()
	b25Fields(t, response, "version", "id", "ok", "result")
	if string(response["version"]) != "1" || string(response["ok"]) != "true" || b25String(t, response["id"]) != id {
		t.Fatalf("invalid success response for %q: %v", id, response)
	}
	return response["result"]
}

func b25Error(t *testing.T, response map[string]json.RawMessage, id, code, message string) {
	t.Helper()
	b25Fields(t, response, "version", "id", "ok", "error")
	if string(response["version"]) != "1" || string(response["ok"]) != "false" || b25String(t, response["id"]) != id {
		t.Fatalf("invalid correlated error response for %q: %v", id, response)
	}
	var public map[string]json.RawMessage
	if err := json.Unmarshal(response["error"], &public); err != nil {
		t.Fatalf("decode public error: %v", err)
	}
	b25Fields(t, public, "code", "message")
	if b25String(t, public["code"]) != code || b25String(t, public["message"]) != message {
		t.Fatalf("public error = %s, want %s/%s", response["error"], code, message)
	}
}

func b25CheckInitialize(t *testing.T, raw json.RawMessage) (string, string) {
	t.Helper()
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	b25Fields(t, result, "project_id", "project_epoch", "participant_id", "public_key", "signing_key_id", "recipient_id", "capabilities")
	if b25String(t, result["project_id"]) != b25ProjectID || string(result["project_epoch"]) != "1" ||
		b25String(t, result["participant_id"]) != b25ParticipantID || b25String(t, result["recipient_id"]) != b25RecipientID {
		t.Fatalf("initialize fixed fields are incorrect: %s", raw)
	}
	var capabilities []string
	if err := json.Unmarshal(result["capabilities"], &capabilities); err != nil ||
		!reflect.DeepEqual(capabilities, []string{"send", "history", "status", "shutdown"}) {
		t.Fatalf("initialize capabilities = %s: %v", result["capabilities"], err)
	}
	publicKey := b25String(t, result["public_key"])
	signingKeyID := b25String(t, result["signing_key_id"])
	b25RawBase64(t, "initialize public key", publicKey, ed25519.PublicKeySize)
	if len(signingKeyID) != len("sha256:")+64 || !strings.HasPrefix(signingKeyID, "sha256:") || !b25LowerHex(strings.TrimPrefix(signingKeyID, "sha256:")) {
		t.Fatalf("invalid signing key ID %q", signingKeyID)
	}
	return publicKey, signingKeyID
}

func b25CheckSend(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode send result: %v", err)
	}
	b25Fields(t, result, "event_id")
	eventID := b25String(t, result["event_id"])
	if len(eventID) != len("sha256:")+64 || !strings.HasPrefix(eventID, "sha256:") || !b25LowerHex(strings.TrimPrefix(eventID, "sha256:")) {
		t.Fatalf("invalid event ID %q", eventID)
	}
	return eventID
}

func b25CheckShutdown(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil || len(result) != 0 {
		t.Fatalf("shutdown result = %s, want exact empty object: %v", raw, err)
	}
}

func b25CheckHistory(t *testing.T, raw json.RawMessage, eventID, publicKey, signingKeyID string) ([]byte, []byte, string) {
	t.Helper()
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode history result: %v", err)
	}
	b25Fields(t, result, "events", "next_after_seq")
	if string(result["next_after_seq"]) != "1" {
		t.Fatalf("history next_after_seq = %s, want 1", result["next_after_seq"])
	}
	var events []json.RawMessage
	if err := json.Unmarshal(result["events"], &events); err != nil || len(events) != 1 {
		t.Fatalf("history events = %s, want one: %v", result["events"], err)
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(events[0], &item); err != nil {
		t.Fatalf("decode history item: %v", err)
	}
	b25Fields(t, item, "event_id", "envelope")
	if b25String(t, item["event_id"]) != eventID {
		t.Fatalf("history event ID = %s, want %q", item["event_id"], eventID)
	}

	envelopeRaw := append([]byte(nil), item["envelope"]...)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(envelopeRaw, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	b25Fields(t, envelope, "payload", "signature")
	payloadRaw := append([]byte(nil), envelope["payload"]...)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	b25Fields(t, payload, "version", "project_id", "project_epoch", "type", "author", "signing_algorithm", "signing_key_id", "author_seq", "prev", "created", "to", "body")
	if string(payload["version"]) != "1" || b25String(t, payload["project_id"]) != b25ProjectID ||
		string(payload["project_epoch"]) != "1" || b25String(t, payload["type"]) != "message" ||
		b25String(t, payload["author"]) != b25ParticipantID || b25String(t, payload["signing_algorithm"]) != "ed25519" ||
		b25String(t, payload["signing_key_id"]) != signingKeyID || string(payload["author_seq"]) != "1" ||
		string(payload["prev"]) != "null" || b25String(t, payload["to"]) != b25RecipientID || b25String(t, payload["body"]) != b25Body {
		t.Fatalf("history payload fields are incorrect: %s", payloadRaw)
	}
	var created map[string]json.RawMessage
	if err := json.Unmarshal(payload["created"], &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	b25Fields(t, created, "physical_ms", "logical")
	if string(created["logical"]) != "0" {
		t.Fatalf("first event logical = %s, want 0", created["logical"])
	}
	var physical uint64
	if err := json.Unmarshal(created["physical_ms"], &physical); err != nil || physical == 0 || physical > 9007199254740991 {
		t.Fatalf("first event physical_ms = %s: %v", created["physical_ms"], err)
	}

	b25CanonicalObject(t, "created", payload["created"])
	b25CanonicalObject(t, "payload", payloadRaw)
	b25CanonicalObject(t, "envelope", envelopeRaw)
	signature := b25String(t, envelope["signature"])
	b25RawBase64(t, "signature", signature, ed25519.SignatureSize)
	b25RawBase64(t, "public key", publicKey, ed25519.PublicKeySize)
	return envelopeRaw, payloadRaw, signature
}

func b25CanonicalObject(t *testing.T, name string, raw []byte) {
	t.Helper()
	canonical, err := canonicalizeJSON(raw)
	if err != nil {
		t.Fatalf("canonicalize %s: %v", name, err)
	}
	if !bytes.Equal(canonical, raw) {
		t.Fatalf("%s is not exact canonical JSON\ngot:  %s\nwant: %s", name, raw, canonical)
	}
}

func b25RawBase64(t *testing.T, name, text string, size int) []byte {
	t.Helper()
	if strings.Contains(text, "=") {
		t.Fatalf("%s is padded base64url: %q", name, text)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil || len(decoded) != size || base64.RawURLEncoding.EncodeToString(decoded) != text {
		t.Fatalf("%s is not canonical unpadded base64url of %d bytes: %q: %v", name, size, text, err)
	}
	return decoded
}

func b25SuccessLogFields(requestID, eventID string) map[string]string {
	fields := map[string]string{
		"project_id": b25ProjectID, "participant_id": b25ParticipantID,
		"harness_id": b25HarnessID, "session_id": b25SessionID, "request_id": requestID,
	}
	if eventID != "" {
		fields["event_id"] = eventID
	}
	return fields
}

func b25CheckLogs(t *testing.T, name string, run b25Run, expected []b25ExpectedLog, forbidden ...string) {
	t.Helper()
	records := b25JSONLines(t, name+" stderr", run.stderr, b25MaxLog)
	if len(records) != len(expected) {
		t.Fatalf("%s log records = %d, want %d: %s", name, len(records), len(expected), run.stderr)
	}
	allowedFields := map[string]bool{
		"time": true, "level": true, "msg": true, "project_id": true, "participant_id": true,
		"harness_id": true, "session_id": true, "request_id": true, "event_id": true, "code": true,
	}
	allowedLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	allowedMessages := map[string]bool{
		"startup": true, "initialize": true, "send": true, "history": true, "status": true,
		"shutdown": true, "request_rejected": true, "recovery_warning": true, "internal_error": true,
	}
	for index, record := range records {
		for field := range record {
			if !allowedFields[field] {
				t.Fatalf("%s log %d has forbidden field %q", name, index, field)
			}
		}
		want := expected[index]
		if len(record) != 3+len(want.fields) {
			t.Fatalf("%s log %d fields = %v, want base fields plus %v", name, index, b25Names(record), want.fields)
		}
		timestamp := b25String(t, record["time"])
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
			t.Fatalf("%s log %d timestamp %q: %v", name, index, timestamp, err)
		}
		level, message := b25String(t, record["level"]), b25String(t, record["msg"])
		if !allowedLevels[level] || !allowedMessages[message] || level != want.level || message != want.message {
			t.Fatalf("%s log %d level/message = %q/%q, want %q/%q", name, index, level, message, want.level, want.message)
		}
		for field, value := range want.fields {
			if b25String(t, record[field]) != value {
				t.Fatalf("%s log %d %s = %s, want %q", name, index, field, record[field], value)
			}
		}
	}
	for _, raw := range run.input {
		forbidden = append(forbidden, raw)
	}
	forbidden = append(forbidden, `"body"`, `"params"`, `"payload"`, `"envelope"`, `"public_key"`, `"signing_key_id"`, `"signature"`, "project.json", "identity.seed")
	for _, text := range forbidden {
		if text != "" && bytes.Contains(run.stderr, []byte(text)) {
			t.Fatalf("%s stderr contains body/protocol/envelope/key/signature/root/path text %q: %s", name, text, run.stderr)
		}
	}
}

func b25String(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("value %s is not a string: %v", raw, err)
	}
	return value
}

type b25SnapshotEntry struct {
	mode fs.FileMode
	data string
}

func b25Snapshot(t *testing.T, directory string) map[string]b25SnapshotEntry {
	t.Helper()
	result := map[string]b25SnapshotEntry{}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read event directory snapshot: %v", err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("inspect event snapshot entry %q: %v", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("event snapshot entry %q is not regular", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatalf("read event snapshot entry %q: %v", entry.Name(), err)
		}
		result[entry.Name()] = b25SnapshotEntry{mode: info.Mode(), data: string(data)}
	}
	return result
}

func b25LowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func ExampleRun() {
	root, err := os.MkdirTemp("", "relay-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)

	scope := []string{
		"--root", root,
		"--project-id", "p-00000000000000000000000000000001",
		"--harness-id", "h-33333333333333333333333333333333",
		"--session-id", "s-44444444444444444444444444444444",
	}
	run := func(operation, id, params string) (int, struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}) {
		var output bytes.Buffer
		exit := Run(context.Background(), append(scope, operation, "--id", id, "--params", params), bytes.NewReader(nil), &output, io.Discard)
		var response struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(output.Bytes(), &response); err != nil {
			panic(err)
		}
		return exit, response
	}

	initializeExit, initializeResponse := run("initialize", "init_1", `{"versions":[1],"create":{"participant_id":"u-11111111111111111111111111111111","recipient_id":"u-22222222222222222222222222222222","recipient_public_key":"A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg"}}`)
	var initialized struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(initializeResponse.Result, &initialized); err != nil {
		panic(err)
	}
	fmt.Println("initialize exit", initializeExit, "ok", initializeResponse.OK, "capabilities", len(initialized.Capabilities))

	sendExit, sendResponse := run("send", "send_1", `{"to":"u-22222222222222222222222222222222","body":"hello"}`)
	var sent struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(sendResponse.Result, &sent); err != nil {
		panic(err)
	}
	validEventID := len(sent.EventID) == len("sha256:")+sha256.Size*2 && strings.HasPrefix(sent.EventID, "sha256:") && b25LowerHex(sent.EventID[len("sha256:"):])
	fmt.Println("send exit", sendExit, "ok", sendResponse.OK, "valid sha256 ID", validEventID)

	historyExit, historyResponse := run("history", "history_1", `{}`)
	var found struct {
		Events []struct {
			EventID string `json:"event_id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(historyResponse.Result, &found); err != nil {
		panic(err)
	}
	sameEventID := len(found.Events) == 1 && found.Events[0].EventID == sent.EventID
	fmt.Println("history exit", historyExit, "ok", historyResponse.OK, "same ID", sameEventID)
	// Output:
	// initialize exit 0 ok true capabilities 4
	// send exit 0 ok true valid sha256 ID true
	// history exit 0 ok true same ID true
}

func FuzzDecodeRequest(f *testing.F) {
	for _, input := range [][]byte{
		[]byte(`{"id":"open_1","op":"initialize","params":{"versions":[1]}}`),
		[]byte(`{"version":1,"id":"send_1","op":"send","params":{"to":"u-22222222222222222222222222222222","body":"hello"}}`),
		[]byte(`{"version":1,"id":"history_1","op":"history","params":{"after_seq":7}}`),
		nil,
		[]byte(`{"version":1,"id":"x","op":"status","params":{},"extra":true}`),
		[]byte(`{"version":1,"id":"x","op":"send","params":{"to":"first","to":"second","body":"hello"}}`),
		[]byte(`{"version":1,"id":"\ud800","op":"status","params":{}}`),
		[]byte{0xff},
	} {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		request, err := decodeRequest(input)
		if err != nil {
			if !errors.Is(err, errInvalidRequest) {
				t.Fatalf("decodeRequest error = %v, want errInvalidRequest", err)
			}
			return
		}
		if !validRequestID(request.id) || !validOperation(request.op) || request.params == nil {
			t.Fatalf("decodeRequest returned unsafe request: id=%q op=%q params=%v", request.id, request.op, request.params)
		}
		if request.op == "initialize" {
			if request.version != 0 || !validInitializeParams(request.params) {
				t.Fatalf("decodeRequest returned unsafe initialize request: %+v", request)
			}
			return
		}
		if request.version < 0 || uint64(request.version) > maxSafeInteger || !validOperationParams(request.op, request.params) {
			t.Fatalf("decodeRequest returned unsafe version or params: %+v", request)
		}
	})
}

func FuzzValidateStoredEvent(f *testing.F) {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index)
	}
	publicKey := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	state := projectState{
		scope:         scopeIDs{projectID: "p-00000000000000000000000000000000"},
		projectEpoch:  1,
		participantID: "u-11111111111111111111111111111111",
		publicKey:     base64.RawURLEncoding.EncodeToString(publicKey),
		signingKeyID:  projectSigningKeyID(publicKey),
		recipientID:   "u-22222222222222222222222222222222",
	}
	payload := []byte(`{"author":"u-11111111111111111111111111111111","author_seq":1,"body":"Hello, relay!","created":{"logical":0,"physical_ms":1700000000000},"prev":null,"project_epoch":1,"project_id":"p-00000000000000000000000000000000","signing_algorithm":"ed25519","signing_key_id":"sha256:56475aa75463474c0285df5dbf2bcab73da651358839e9b77481b2eab107708c","to":"u-22222222222222222222222222222222","type":"message","version":1}`)
	envelope, _, err := signEvent(seed, payload)
	if err != nil {
		f.Fatal(err)
	}
	changed := append([]byte(nil), envelope...)
	changed[len(changed)/2] ^= 1
	for _, input := range [][]byte{
		envelope,
		changed,
		envelope[:len(envelope)-1],
		append(append([]byte(nil), envelope...), '\n'),
		nil,
		[]byte(`{}`),
		[]byte{0x00, 0xff, '{', '}'},
	} {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, contents []byte) {
		digest := sha256.Sum256(contents)
		digestText := hex.EncodeToString(digest[:])
		file := storedEventFile{name: "0000000000000001-" + digestText + ".json", sequence: 1, digest: digestText}

		event, err := validateStoredEvent(state, file, contents)
		if err != nil {
			if !errors.Is(err, errInvalidEvent) {
				t.Fatalf("validateStoredEvent error = %v, want errInvalidEvent", err)
			}
			return
		}
		if event.eventID != "sha256:"+digestText || event.sequence != file.sequence || event.predecessor != nil ||
			event.physicalMilliseconds != 1700000000000 || event.logical != 0 || !bytes.Equal(event.envelope, contents) {
			t.Fatalf("validateStoredEvent returned inconsistent metadata: %+v", event)
		}
	})
}

func TestOversizedResponseAccounting(t *testing.T) {
	const (
		projectID     = "p-00000000000000000000000000000000"
		participantID = "u-11111111111111111111111111111111"
		harnessID     = "h-33333333333333333333333333333333"
		sessionID     = "s-44444444444444444444444444444444"
		privateMarker = "PRIVATE_OVERSIZED_RESULT_7f3c"
	)

	requestID := "status_oversized"
	status := newProcessStatus(time.Now())
	scope := scopeIDs{projectID: projectID, harnessID: harnessID, sessionID: sessionID}
	relaySession := newSession(connectionConfig{scope: scope}, status)
	relaySession.project = projectState{scope: scope, participantID: participantID}

	var output, logs bytes.Buffer
	result := map[string]string{"private_result": privateMarker + strings.Repeat("x", maxRecordSize)}
	if err := relaySession.writeOutcome(&output, &logs, &requestID, "status", result, "", false, nil); err != nil {
		t.Fatalf("writeOutcome: %v", err)
	}

	wantResponse := `{"version":1,"id":"status_oversized","ok":false,"error":{"code":"internal_error","message":"Operation failed."}}` + "\n"
	if got := output.String(); got != wantResponse {
		t.Errorf("response = %q, want correlated internal_error %q", got, wantResponse)
	}

	snapshot, err := status.snapshot(time.Now())
	if err != nil {
		t.Fatalf("status snapshot: %v", err)
	}
	if snapshot.Rejected != 1 || snapshot.LastError == nil || snapshot.LastError.Code != "internal_error" || snapshot.LastError.Message != "Operation failed." {
		t.Errorf("status after oversized result = %+v, want one internal_error rejection", snapshot)
	}

	var outcome map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSuffix(logs.Bytes(), []byte{'\n'}), &outcome); err != nil {
		t.Fatalf("outcome log: %v", err)
	}
	if len(outcome) != 5 || string(outcome["level"]) != `"warn"` || string(outcome["msg"]) != `"request_rejected"` ||
		string(outcome["request_id"]) != `"status_oversized"` || string(outcome["code"]) != `"internal_error"` {
		t.Errorf("outcome log = %s, want correlated warn request_rejected/internal_error", logs.Bytes())
	}

	for name, data := range map[string][]byte{"response": output.Bytes(), "log": logs.Bytes()} {
		if bytes.Contains(data, []byte(privateMarker)) || bytes.Contains(data, []byte("private_result")) {
			t.Errorf("%s leaks private oversized result: %s", name, data)
		}
	}
}

func TestPublicationCleanupWarning(t *testing.T) {
	directory := t.TempDir()
	finalName := "event.json"
	contents := []byte("complete event")
	cleanupErr := errors.New("cleanup failed")
	var temporary string

	result, err := publishWithRemove(context.Background(), directory, finalName, contents, func(name string) error {
		temporary = name
		return cleanupErr
	})
	if err != nil {
		t.Fatalf("publishWithRemove: %v", err)
	}
	if result.state != publicationNew || result.cleanupWarning == nil {
		t.Fatalf("publish result = %+v, want new with cleanup warning", result)
	}

	final := filepath.Join(directory, finalName)
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, contents) {
		t.Fatalf("final content = %q, want %q", got, contents)
	}
	finalInfo, err := os.Stat(final)
	if err != nil {
		t.Fatal(err)
	}
	temporaryInfo, err := os.Stat(temporary)
	if err != nil {
		t.Fatalf("leftover temporary: %v", err)
	}
	if !os.SameFile(finalInfo, temporaryInfo) {
		t.Fatal("cleanup failure replaced the accepted final file")
	}

	if err := os.Remove(temporary); err != nil {
		t.Fatalf("clean leftover temporary: %v", err)
	}
	got, err = os.ReadFile(final)
	if err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("final after temporary cleanup = %q, %v; want %q", got, err, contents)
	}
}

// Forge frozen-test flag B18/B22/B23: standing continuation requires accepted cleanup warnings to propagate without changing success accounting or exposing raw errors.
func TestCleanupWarningPropagation(t *testing.T) {
	const (
		projectID     = "p-00000000000000000000000000000000"
		participantID = "u-11111111111111111111111111111111"
		harnessID     = "h-33333333333333333333333333333333"
		sessionID     = "s-44444444444444444444444444444444"
		rawError      = "RAW_CLEANUP_ERROR_7f3c"
	)

	if !publicationWarning(publicationResult{state: publicationNew, cleanupWarning: errors.New(rawError)}) {
		t.Fatal("publicationWarning = false, want true for a cleanup warning")
	}

	requestID := "status_cleanup"
	status := newProcessStatus(time.Now())
	scope := scopeIDs{projectID: projectID, harnessID: harnessID, sessionID: sessionID}
	relaySession := newSession(connectionConfig{scope: scope}, status)
	relaySession.project = projectState{scope: scope, participantID: participantID}
	result := map[string]string{"value": "accepted"}

	var output, logs bytes.Buffer
	if err := relaySession.writeOutcome(&output, &logs, &requestID, "status", result, "", true, nil); err != nil {
		t.Fatalf("writeOutcome: %v", err)
	}
	wantResponse := `{"id":"status_cleanup","ok":true,"result":{"value":"accepted"},"version":1}` + "\n"
	if got := output.String(); got != wantResponse {
		t.Fatalf("response = %q, want %q", got, wantResponse)
	}

	snapshot, err := status.snapshot(time.Now())
	if err != nil {
		t.Fatalf("status snapshot: %v", err)
	}
	if snapshot.Rejected != 0 || snapshot.LastError != nil {
		t.Fatalf("status after cleanup warning = %+v, want unchanged rejected and last_error", snapshot)
	}

	if len(logs.Bytes()) == 0 || logs.Bytes()[logs.Len()-1] != '\n' {
		t.Fatalf("logs are not LF-terminated: %q", logs.Bytes())
	}
	lines := bytes.Split(logs.Bytes()[:logs.Len()-1], []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("log records = %d, want ordinary outcome then one recovery warning: %s", len(lines), logs.Bytes())
	}
	for index, want := range []struct {
		level   string
		message string
	}{{"info", "status"}, {"warn", "recovery_warning"}} {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(lines[index], &record); err != nil {
			t.Fatalf("log %d: %v", index, err)
		}
		if string(record["level"]) != `"`+want.level+`"` || string(record["msg"]) != `"`+want.message+`"` {
			t.Fatalf("log %d = %s, want %s %s", index, lines[index], want.level, want.message)
		}
		for name := range record {
			switch name {
			case "time", "level", "msg", "project_id", "participant_id", "harness_id", "session_id", "request_id", "event_id", "code":
			default:
				t.Fatalf("log %d has unsanitized field %q: %s", index, name, lines[index])
			}
		}
	}
	if bytes.Contains(logs.Bytes(), []byte(rawError)) {
		t.Fatalf("recovery warning leaked raw cleanup error: %s", logs.Bytes())
	}
}

// Forge frozen-test flag B18: standing continuation adds fresh-verifier setup publication error classification.
func TestSetupPublicationErrorClassification(t *testing.T) {
	for _, input := range []error{
		errPublishConflict,
		fmt.Errorf("wrapped: %w", errPublishConflict),
	} {
		err := setupPublicationError(input)
		if !errors.Is(err, errInvalidProject) || errors.Is(err, errStorage) {
			t.Errorf("setupPublicationError(%v) = %v, want invalid_project only", input, err)
		}
	}

	err := setupPublicationError(errStorage)
	if !errors.Is(err, errStorage) || errors.Is(err, errInvalidProject) {
		t.Errorf("setupPublicationError(errStorage) = %v, want storage_error only", err)
	}
}

// Forge frozen-test flag B9/B19: standing continuation requires successful history responses to preserve canonical envelope bytes.
func TestHistoryResponsePreservesCanonicalEnvelope(t *testing.T) {
	canonicalEnvelope := json.RawMessage(`{"payload":{"body":"<>& "},"signature":"test"}`)
	canonical, err := canonicalizeJSON(canonicalEnvelope)
	if err != nil || !bytes.Equal(canonical, canonicalEnvelope) {
		t.Fatalf("envelope is not canonical: %s, %v", canonical, err)
	}
	digest := sha256.Sum256(canonicalEnvelope)
	eventID := "sha256:" + hex.EncodeToString(digest[:])
	result := historyResult{
		Events:            []historyItem{{EventID: eventID, Envelope: canonicalEnvelope}},
		NextAfterSequence: 1,
	}

	requestID := "history_escapes"
	var output bytes.Buffer
	if err := writeResponse(&output, &requestID, result, nil); err != nil {
		t.Fatalf("writeResponse: %v", err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var history struct {
		Events []historyItem `json:"events"`
	}
	if err := json.Unmarshal(response.Result, &history); err != nil || len(history.Events) != 1 {
		t.Fatalf("decode history result: %v; events = %d", err, len(history.Events))
	}
	if !bytes.Equal(history.Events[0].Envelope, canonicalEnvelope) {
		t.Errorf("response envelope bytes = %s, want exact stored bytes %s", history.Events[0].Envelope, canonicalEnvelope)
	}
	responseDigest := sha256.Sum256(history.Events[0].Envelope)
	if history.Events[0].EventID != eventID || responseDigest != digest {
		t.Errorf("response event ID/digest = %q/%x, want %q/%x", history.Events[0].EventID, responseDigest, eventID, digest)
	}
}

type inputFailureReader struct {
	data  []byte
	err   error
	calls int
}

func (reader *inputFailureReader) Read(buffer []byte) (int, error) {
	reader.calls++
	if reader.calls > 1 {
		panic("input reader retried")
	}
	return copy(buffer, reader.data), reader.err
}

// Forge frozen-test flag B6/B10/B23: standing continuation requires non-EOF input faults to be fatal sanitized internal errors.
func TestInputReadFailure(t *testing.T) {
	const privateText = "PRIVATE_INPUT_READ_FAILURE_7f3c"
	privateErr := errors.New(privateText)

	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "immediate"},
		{name: "after partial record", data: []byte(`{"id":"private`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &inputFailureReader{data: test.data, err: privateErr}
			var output, logs bytes.Buffer

			config := connectionConfig{
				root: filepath.Join(t.TempDir(), ".ihr"),
				scope: scopeIDs{
					projectID: "p-00000000000000000000000000000000",
					harnessID: "h-33333333333333333333333333333333",
					sessionID: "s-44444444444444444444444444444444",
				},
			}
			if exit := operateConnection(context.Background(), reader, &output, &logs, config); exit != 1 {
				t.Errorf("exit = %d, want 1", exit)
			}
			if reader.calls != 1 {
				t.Errorf("input reads = %d, want 1 without retry", reader.calls)
			}

			wantResponse := `{"version":1,"id":null,"ok":false,"error":{"code":"internal_error","message":"Operation failed."}}` + "\n"
			if got := output.String(); got != wantResponse {
				t.Errorf("response = %q, want exactly one null-ID internal_error %q", got, wantResponse)
			}

			if logs.Len() == 0 || logs.Bytes()[logs.Len()-1] != '\n' {
				t.Fatalf("log is not nonempty LF-terminated JSONL: %q", logs.Bytes())
			}
			lines := bytes.Split(logs.Bytes()[:logs.Len()-1], []byte{'\n'})
			if len(lines) != 1 {
				t.Fatalf("log records = %d, want 1: %s", len(lines), logs.Bytes())
			}
			var record map[string]json.RawMessage
			if err := json.Unmarshal(lines[0], &record); err != nil {
				t.Fatalf("decode log: %v", err)
			}
			if len(record) != 4 || string(record["level"]) != `"warn"` || string(record["msg"]) != `"request_rejected"` || string(record["code"]) != `"internal_error"` {
				t.Errorf("log = %s, want sanitized warn request_rejected/internal_error", lines[0])
			}
			if _, ok := record["time"]; !ok {
				t.Errorf("log lacks time: %s", lines[0])
			}

			if bytes.Contains(output.Bytes(), []byte(privateText)) || bytes.Contains(logs.Bytes(), []byte(privateText)) {
				t.Errorf("response or log leaks private input error: stdout=%s stderr=%s", output.Bytes(), logs.Bytes())
			}
		})
	}
}
