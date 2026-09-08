// Package bridge implements the bounded JSON-lines connectivity subprocess.
package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
)

const (
	version       = 1
	maxFrameBytes = 65536
	maxTimeoutMs  = 300000
	maxRequests   = 10000
	maxNodes      = 64
	maxServices   = 128
)

var handlePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type request struct {
	Version   int             `json:"version"`
	ID        string          `json:"id"`
	Op        string          `json:"op"`
	TimeoutMs int             `json:"timeoutMs"`
	Args      json.RawMessage `json:"args"`
}

type response struct {
	Version int            `json:"version"`
	ID      string         `json:"id"`
	Result  any            `json:"result,omitempty"`
	Error   *protocolError `json:"error,omitempty"`
}

type protocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *protocolError) Error() string { return e.Message }

func failure(code string) *protocolError {
	messages := map[string]string{
		"invalid_command":     "Invalid command, arguments, framing, or request ID",
		"unsupported_version": "Unsupported protocol version",
		"duplicate_handle":    "Handle is already reserved",
		"unknown_handle":      "Handle does not exist",
		"closed_node":         "Node is closed",
		"timeout":             "Operation deadline exceeded",
		"unreachable":         "Network endpoint is unreachable",
		"refused":             "Network connection was refused",
		"cancelled":           "Operation was cancelled",
		"identity_mismatch":   "Endpoint returned a different identity",
		"invalid_response":    "Endpoint returned an invalid response",
		"bridge_failure":      "Bridge operation or cleanup failed",
	}
	return &protocolError{Code: code, Message: messages[code]}
}

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
