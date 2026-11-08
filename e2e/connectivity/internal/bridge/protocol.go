// Package bridge implements the bounded JSON-RPC connectivity subprocess.
package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"time"

	"github.com/creachadair/jrpc2"
)

const (
	maxTimeout  = 300 * time.Second
	maxRequests = 10000
	maxNodes    = 64
	maxServices = 128

	appErrorCode jrpc2.Code = 1
)

// dnsNamePattern matches a lowercase multi-label hostname, without a trailing dot.
var dnsNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62})(\.[a-z0-9]([a-z0-9-]{0,62}))+$`)

var handlePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type timeout time.Duration

func (t *timeout) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > maxTimeout {
		return errors.New("timeout out of range")
	}
	*t = timeout(d)
	return nil
}

type protocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *protocolError) Error() string { return e.Message }

func failure(code string) *protocolError {
	messages := map[string]string{
		"invalid_command":         "Invalid command or arguments",
		"duplicate_handle":        "Handle is already reserved",
		"unknown_handle":          "Handle does not exist",
		"closed_node":             "Node is closed",
		"timeout":                 "Operation deadline exceeded",
		"unreachable":             "Network endpoint is unreachable",
		"refused":                 "Network connection was refused",
		"cancelled":               "Operation was cancelled",
		"identity_mismatch":       "Endpoint returned a different identity",
		"invalid_response":        "Endpoint returned an invalid response",
		"bridge_failure":          "Bridge operation or cleanup failed",
		"certificate_unavailable": "Node could not get an HTTPS certificate",
	}
	return &protocolError{Code: code, Message: messages[code]}
}

// toRPCError converts an application error into the *jrpc2.Error the
// transport sends over the wire, preserving the application error code in
// the response's Data field. Non-protocol errors (e.g. framing failures
// reported by the library itself) are returned unchanged.
func toRPCError(err error) error {
	var pe *protocolError
	if !errors.As(err, &pe) {
		return err
	}
	return jrpc2.Errorf(appErrorCode, "%s", pe.Message).WithData(pe)
}

// decode strictly decodes raw into target, rejecting unknown or duplicate
// JSON keys so RPC arguments can't silently carry unexpected fields. This is
// an application-level shape/security check, independent of the JSON-RPC
// envelope itself (which the transport library validates).
func decode(raw []byte, target any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return failure("invalid_command")
	}
	fields := make(map[string]bool)
	structure := reflect.TypeOf(target).Elem()
	for field := range structure.Fields() {
		fields[field.Tag.Get("json")] = true
	}
	keys := json.NewDecoder(bytes.NewReader(raw))
	if _, err := keys.Token(); err != nil {
		return failure("invalid_command")
	}
	seen := make(map[string]bool)
	for keys.More() {
		key, err := keys.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] || !fields[name] {
			return failure("invalid_command")
		}
		seen[name] = true
		var value json.RawMessage
		if err := keys.Decode(&value); err != nil {
			return failure("invalid_command")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return failure("invalid_command")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return failure("invalid_command")
	}
	return nil
}
