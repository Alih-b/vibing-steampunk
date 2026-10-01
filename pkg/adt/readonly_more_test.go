package adt

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// readOnlyMockClient is a client whose transport records every request; with
// readOnly set, it refuses writes.
func readOnlyMockClient(readOnly bool) (*Client, *mockTransportClient) {
	mock := &mockTransportClient{responses: map[string]*http.Response{}}
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	cfg.Safety.ReadOnly = readOnly
	return NewClientWithTransport(cfg, NewTransportWithClient(cfg, mock)), mock
}

// assertReadOnlyRefusal runs op on a read-only client and on a writable one:
// the first must be refused before any request, the second must send.
func assertReadOnlyRefusal(t *testing.T, op func(*Client) error) {
	t.Helper()
	c, mock := readOnlyMockClient(true)
	err := op(c)
	if err == nil || !strings.Contains(err.Error(), "is blocked") {
		t.Fatalf("want a safety refusal, got %v", err)
	}
	if len(mock.requests) != 0 {
		t.Errorf("a refused operation sent %d request(s)", len(mock.requests))
	}
	c, mock = readOnlyMockClient(false)
	if err := op(c); err != nil && strings.Contains(err.Error(), "blocked") {
		t.Fatalf("refused without --read-only: %v", err)
	}
	if len(mock.requests) == 0 {
		t.Error("never reached SAP without --read-only")
	}
}

func TestServiceBindingPublish_RefusedUnderReadOnly(t *testing.T) {
	ctx := context.Background()
	t.Run("publish", func(t *testing.T) {
		assertReadOnlyRefusal(t, func(c *Client) error {
			_, err := c.PublishServiceBinding(ctx, "ZDEMO_SB", "0001")
			return err
		})
	})
	t.Run("unpublish", func(t *testing.T) {
		assertReadOnlyRefusal(t, func(c *Client) error {
			_, err := c.UnpublishServiceBinding(ctx, "ZDEMO_SB", "0001")
			return err
		})
	})
}

func TestSetPrettyPrinterSettings_RefusedUnderReadOnly(t *testing.T) {
	assertReadOnlyRefusal(t, func(c *Client) error {
		return c.SetPrettyPrinterSettings(context.Background(), &PrettyPrinterSettings{Indentation: true, Style: "keywordUpper"})
	})
}

// Under --read-only a unit test run is still allowed, but not one that
// includes tests declared RISK LEVEL DANGEROUS or CRITICAL.
func TestRunUnitTests_DangerousRefusedUnderReadOnly(t *testing.T) {
	ctx := context.Background()
	for name, flags := range map[string]UnitTestRunFlags{
		"dangerous": {Harmless: true, Dangerous: true, Short: true},
		"critical":  {Harmless: true, Critical: true, Short: true},
	} {
		t.Run(name, func(t *testing.T) {
			c, mock := readOnlyMockClient(true)
			_, err := c.RunUnitTests(ctx, "/sap/bc/adt/oo/classes/zcl_demo", &flags)
			if err == nil || !strings.Contains(err.Error(), "is blocked: read-only mode enabled") {
				t.Fatalf("want a read-only refusal, got %v", err)
			}
			if len(mock.requests) != 0 {
				t.Errorf("a refused test run sent %d request(s)", len(mock.requests))
			}
		})
	}
	t.Run("harmless still runs", func(t *testing.T) {
		c, mock := readOnlyMockClient(true)
		_, err := c.RunUnitTests(ctx, "/sap/bc/adt/oo/classes/zcl_demo", nil)
		if err != nil && strings.Contains(err.Error(), "blocked") {
			t.Fatalf("an ordinary run was refused: %v", err)
		}
		if len(mock.requests) == 0 {
			t.Error("an ordinary run never reached SAP")
		}
	})
	t.Run("dangerous without read-only", func(t *testing.T) {
		c, mock := readOnlyMockClient(false)
		flags := UnitTestRunFlags{Harmless: true, Dangerous: true, Short: true}
		if _, err := c.RunUnitTests(ctx, "/sap/bc/adt/oo/classes/zcl_demo", &flags); err != nil && strings.Contains(err.Error(), "blocked") {
			t.Fatalf("refused without --read-only: %v", err)
		}
		if len(mock.requests) == 0 {
			t.Error("never reached SAP")
		}
	})
}

// Under --read-only only a READ lock is allowed. A MODIFY lock (or any other
// mode, known or not) serves no write and strands an SM12 entry.
func TestLockObject_ModifyRefusedUnderReadOnly(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"MODIFY", "", "modify", "EXCLUSIVE", "SOMETHING_NEW"} {
		t.Run("mode "+mode, func(t *testing.T) {
			assertReadOnlyRefusal(t, func(c *Client) error {
				_, err := c.LockObject(ctx, "/sap/bc/adt/programs/programs/zdemo", mode)
				return err
			})
		})
	}
	t.Run("READ", func(t *testing.T) {
		c, mock := readOnlyMockClient(true)
		_, err := c.LockObject(ctx, "/sap/bc/adt/programs/programs/zdemo", "READ")
		if err != nil && strings.Contains(err.Error(), "blocked") {
			t.Fatalf("a READ lock was refused: %v", err)
		}
		if len(mock.requests) == 0 {
			t.Error("a READ lock never reached SAP")
		}
	})
}

// Overwriting a variable in a live program is refused under --read-only. This
// is the ADT-client helper behind the Lua bindings forceReplay,
// injectCheckpoint and replayFromStep. Lua's setVariable uses the debug
// session (saprfc.Debugger.SetVariable) instead, and is gated in the engine.
func TestDebuggerSetVariableValue_RefusedUnderReadOnly(t *testing.T) {
	assertReadOnlyRefusal(t, func(c *Client) error {
		_, err := c.DebuggerSetVariableValue(context.Background(), "LV_COUNT", "42")
		return err
	})
}

// GetCodeCoverage posts the same ABAP Unit run as RunUnitTests, so it takes
// the same rule: no dangerous or critical tests under --read-only.
func TestGetCodeCoverage_DangerousRefusedUnderReadOnly(t *testing.T) {
	ctx := context.Background()
	for name, flags := range map[string]UnitTestRunFlags{
		"dangerous": {Harmless: true, Dangerous: true, Short: true},
		"critical":  {Harmless: true, Critical: true, Short: true},
	} {
		t.Run(name, func(t *testing.T) {
			c, mock := readOnlyMockClient(true)
			_, err := c.GetCodeCoverage(ctx, "/sap/bc/adt/oo/classes/zcl_demo", &flags)
			if err == nil || !strings.Contains(err.Error(), "is blocked: read-only mode enabled") {
				t.Fatalf("want a read-only refusal, got %v", err)
			}
			if len(mock.requests) != 0 {
				t.Errorf("a refused coverage run sent %d request(s)", len(mock.requests))
			}
		})
	}
	t.Run("harmless still runs", func(t *testing.T) {
		c, mock := readOnlyMockClient(true)
		if _, err := c.GetCodeCoverage(ctx, "/sap/bc/adt/oo/classes/zcl_demo", nil); err != nil && strings.Contains(err.Error(), "blocked") {
			t.Fatalf("an ordinary coverage run was refused: %v", err)
		}
		if len(mock.requests) == 0 {
			t.Error("an ordinary coverage run never reached SAP")
		}
	})
}
