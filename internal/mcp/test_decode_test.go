package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// #379: decodeRPCPayload and rpcID are pure with surprising corners. Every
// expected output here was checked against the real functions. Assert on
// err != nil plus a substring for the two stdlib *json.UnmarshalTypeError
// strings, which are not ours to pin exactly.
func TestDecodeRPCPayload(t *testing.T) {
	t.Run("a plain object with no recognised fields reports an array type error", func(t *testing.T) {
		// The object path unmarshals fine but fails the field test, then falls
		// through to the batch path, which surfaces ITS error instead —
		// confusing but worth pinning.
		for _, raw := range []string{`{}`, `{"method":"notifications/x"}`} {
			_, err := decodeRPCPayload([]byte(raw))
			if err == nil || !strings.Contains(err.Error(), "[]mcp.rpcMessage") {
				t.Errorf("%s: got %v, want the batch type error naming []mcp.rpcMessage", raw, err)
			}
		}
	})

	t.Run("jsonrpc alone succeeds", func(t *testing.T) {
		msg, err := decodeRPCPayload([]byte(`{"jsonrpc":"2.0"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg.JSONRPC != "2.0" {
			t.Errorf("JSONRPC %q", msg.JSONRPC)
		}
	})

	t.Run("id 0 succeeds — ID is any and 0 is non-nil", func(t *testing.T) {
		msg, err := decodeRPCPayload([]byte(`{"id":0}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg.ID == nil {
			t.Error("ID must be non-nil")
		}
	})

	t.Run("a batch silently drops everything after the first response", func(t *testing.T) {
		msg, err := decodeRPCPayload([]byte(`[{"id":1},{"id":2}]`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := rpcID(msg.ID); got != 1 {
			t.Errorf("got id %d, want only the first response's id 1", got)
		}
		// Notifications are skipped over.
		msg, err = decodeRPCPayload([]byte(`[{"method":"n"},{"id":7}]`))
		if err != nil || rpcID(msg.ID) != 7 {
			t.Errorf("notifications skipped: got id %d err %v", rpcID(msg.ID), err)
		}
		// A null element is skipped too.
		msg, err = decodeRPCPayload([]byte(`[null, {"id":3}]`))
		if err != nil || rpcID(msg.ID) != 3 {
			t.Errorf("null skipped: got id %d err %v", rpcID(msg.ID), err)
		}
	})

	t.Run("null and an empty array give the no-response error", func(t *testing.T) {
		for _, raw := range []string{`null`, `[]`} {
			_, err := decodeRPCPayload([]byte(raw))
			if err == nil || err.Error() != "no JSON-RPC response in payload" {
				t.Errorf("%s: got %v, want the no-response error", raw, err)
			}
		}
	})
}

func TestRPCID(t *testing.T) {
	t.Run("two failure sentinels, and one of them is a legitimate id", func(t *testing.T) {
		if got := rpcID(json.Number("8.5")); got != 0 {
			t.Errorf("json.Number with a fraction: got %d, want 0 (Int64 fails silently)", got)
		}
		if got := rpcID("5"); got != -1 {
			t.Errorf("string: got %d, want -1 (the not-found sentinel)", got)
		}
	})

	t.Run("int64 is not covered by the type switch", func(t *testing.T) {
		if got := rpcID(int64(5)); got != -1 {
			t.Errorf("int64(5): got %d, want -1 (the switch misses int64)", got)
		}
		if got := rpcID(int(5)); got != 5 {
			t.Errorf("int(5): got %d, want 5", got)
		}
	})

	t.Run("float truncates toward zero, not floor", func(t *testing.T) {
		if got := rpcID(float64(-2.9)); got != -2 {
			t.Errorf("float64(-2.9): got %d, want -2 (truncation toward zero)", got)
		}
	})

	t.Run("nil gives the not-found sentinel", func(t *testing.T) {
		if got := rpcID(nil); got != -1 {
			t.Errorf("nil: got %d, want -1", got)
		}
	})
}
