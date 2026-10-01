// handlers_rfc.go adds classic RFC to the universal SAP tool: action="rfc" with
// an op in params. RFC is a second protocol to the same system (gateway instead
// of HTTP/ADT), served by the SDK-free open-rfc-go client. It is one action, not
// a family of tools, so the MCP tool space stays a single SAP tool.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	openrfc "github.com/oisee/open-rfc-go/rfc"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/config"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// routeRFCAction handles SAP(action="rfc", …).
//
//	SAP(action="rfc", params={"op":"info"})                       — RFC_SYSTEM_INFO
//	SAP(action="rfc", params={"op":"ping"})                       — RFC_PING
//	SAP(action="rfc", params={"op":"probe"})                      — system fingerprint:
//	    release, components, helper presence, and what this user may call
//	SAP(action="rfc", target="BAPI_USER_*", params={"op":"search"})
//	SAP(action="rfc", target="STFC_CONNECTION")                   — describe (default)
//	SAP(action="rfc", target="Z_DOUBLE", params={"op":"call","args":{"N":21}})
//	SAP(action="rfc", target="T000", params={"op":"read_table","fields":["MANDT"],"top":5})
//
// Destination overrides: params host / sysnr / port / user.
func (s *Server) routeRFCAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (result *mcp.CallToolResult, handled bool, rfcErr error) {
	if action != "rfc" {
		return nil, false, nil
	}
	// The target is a plain name (FM or table), so parseTarget puts it in objectType.
	name := strings.TrimSpace(objectName)
	if name == "" {
		name = strings.TrimSpace(objectType)
	}
	op := strings.ToLower(getStringParam(params, "op"))
	if op == "" {
		switch {
		case name == "":
			op = "info"
		case params["args"] != nil:
			op = "call"
		default:
			op = "describe"
		}
	}

	// Executing a function module does whatever the module does — post a
	// document, change a user, start a job — so "call" is gated like
	// ExecuteABAP, as a workflow operation, which --read-only refuses. It is
	// checked before logon, so a refused call sends nothing to the gateway.
	// The other ops only read: system info, ping, the interface metadata,
	// TFDIR/TADIR/CVERS and RFC_READ_TABLE rows, and RFC_SIMULATE_AUTH_CHECK.
	if op == "call" {
		if err := s.adtClient.Safety().CheckOperation(adt.OpWorkflow, "RFCCall"); err != nil {
			return nil, true, err
		}
	}

	c, release, err := s.rfcClientFor(ctx, params)
	if err != nil {
		return nil, true, err
	}
	defer release()

	// A dead shared connection must not poison every later call.
	defer func() {
		if rfcErr != nil && (errors.Is(rfcErr, openrfc.ErrTransport) || errors.Is(rfcErr, openrfc.ErrClosed)) {
			s.dropSharedRFC(ctx)
		}
	}()

	switch op {
	case "info":
		r, err := c.Call(ctx, "RFC_SYSTEM_INFO", nil)
		if err != nil {
			return nil, true, err
		}
		return rfcResult(r.Get("RFCSI_EXPORT"))
	case "probe":
		dest, derr := s.rfcDestination(params)
		if derr != nil {
			return nil, true, derr
		}
		probe, perr := saprfc.RunProbe(ctx, c, dest)
		if perr != nil {
			return nil, true, perr
		}
		return rfcResult(probe)
	case "ping":
		if _, err := c.Call(ctx, "RFC_PING", nil); err != nil {
			return nil, true, err
		}
		return mcp.NewToolResultText("ok"), true, nil
	case "describe":
		if name == "" {
			return nil, true, fmt.Errorf("describe needs a function module in target")
		}
		tool, err := c.DescribeTool(ctx, strings.ToUpper(name))
		if err != nil {
			return nil, true, err
		}
		return rfcResult(tool)
	case "call":
		if name == "" {
			return nil, true, fmt.Errorf("call needs a function module in target")
		}
		args, _ := params["args"].(map[string]any)
		r, err := c.Call(ctx, strings.ToUpper(name), openrfc.Params(args))
		if err != nil {
			return nil, true, err
		}
		return rfcResult(r)
	case "search":
		like := strings.ReplaceAll(strings.ToUpper(name), "*", "%")
		if like == "" {
			return nil, true, fmt.Errorf("search needs a name mask in target")
		}
		if !strings.Contains(like, "%") {
			like = "%" + like + "%"
		}
		where := "FUNCNAME LIKE '" + like + "'"
		if all, ok := getBoolParam(params, "all"); !ok || !all {
			// 'R' and 'X' are both remote-enabled; 'X' additionally marks the
			// interface basXML-capable, which SAP sets on every FM with
			// deep/nested parameters. See pkg/saprfc/adt.go.
			where += " AND FMODE IN ( 'R', 'X' )"
		}
		rows, err := saprfc.ReadTable(ctx, c, "TFDIR", where, []string{"FUNCNAME", "PNAME"}, intParam(params, "top", 100))
		if err != nil {
			return nil, true, err
		}
		return rfcResult(rows)
	case "read_table", "read-table", "table":
		if name == "" {
			return nil, true, fmt.Errorf("read_table needs a table name in target")
		}
		var fields []string
		if raw, ok := params["fields"].([]any); ok {
			for _, f := range raw {
				fields = append(fields, strings.ToUpper(fmt.Sprint(f)))
			}
		}
		rows, err := saprfc.ReadTable(ctx, c, strings.ToUpper(name), getStringParam(params, "where"), fields, intParam(params, "top", 0))
		if err != nil {
			return nil, true, err
		}
		return rfcResult(rows)
	}
	return nil, true, fmt.Errorf("unknown rfc op %q (info, ping, probe, describe, call, search, read_table)", op)
}

// rfcClientFor returns a client for this call and a release function. Calls
// without a destination override share one connection pool for the life of the
// server — an RFC logon per tool call is slow and needless — while a call that
// overrides host/sysnr/port/user gets its own client, closed on release.
func (s *Server) rfcClientFor(ctx context.Context, params map[string]any) (*openrfc.Client, func(), error) {
	overridden := getStringParam(params, "host") != "" || getStringParam(params, "sysnr") != "" ||
		getStringParam(params, "user") != "" || intParam(params, "port", 0) != 0
	if overridden {
		c, err := s.dialRFC(ctx, params)
		if err != nil {
			return nil, nil, err
		}
		return c, func() { _ = c.Close(ctx) }, nil
	}

	s.rfcMu.Lock()
	defer s.rfcMu.Unlock()
	if s.rfcShared == nil {
		c, err := s.dialRFC(ctx, params)
		if err != nil {
			return nil, nil, err
		}
		s.rfcShared = c
		s.startRFCKeepAlive()
	}
	s.rfcLastUsed = time.Now()
	return s.rfcShared, func() {}, nil
}

// rfcKeepAliveInterval is how often an idle shared connection is pinged. SAP
// gateways and work processes drop conversations that go quiet, and an MCP
// session can sit idle for a long time between a user's questions. One minute is
// deliberately conservative: an RFC_PING is a few hundred bytes, far cheaper than
// the logon a dropped connection would cost.
const rfcKeepAliveInterval = time.Minute

// startRFCKeepAlive pings the shared connection while it is idle, so the next
// tool call does not pay for a fresh logon (or fail outright). It exits once the
// shared client is gone. Must be called with rfcMu held.
func (s *Server) startRFCKeepAlive() {
	go func() {
		ticker := time.NewTicker(rfcKeepAliveInterval)
		defer ticker.Stop()
		for range ticker.C {
			s.rfcMu.Lock()
			c := s.rfcShared
			idle := time.Since(s.rfcLastUsed)
			s.rfcMu.Unlock()
			if c == nil {
				return // the client was dropped; nothing to keep alive
			}
			if idle < rfcKeepAliveInterval {
				continue // a real call kept it warm
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err := c.Call(ctx, "RFC_PING", nil)
			cancel()
			if err != nil {
				// The connection is gone: forget it so the next call redials.
				s.dropSharedRFC(context.Background())
				return
			}
		}
	}()
}

// dropSharedRFC forgets a shared client whose connection died, so the next call
// logs on again instead of failing forever.
func (s *Server) dropSharedRFC(ctx context.Context) {
	s.rfcMu.Lock()
	defer s.rfcMu.Unlock()
	if s.rfcShared != nil {
		_ = s.rfcShared.Close(ctx)
		s.rfcShared = nil
	}
}

// dialRFC resolves the destination for this server's system, honouring per-call
// overrides and the RFC settings of the default .vsp.json system.
func (s *Server) dialRFC(ctx context.Context, params map[string]any) (*openrfc.Client, error) {
	dest, err := s.rfcDestination(params)
	if err != nil {
		return nil, err
	}
	c, err := saprfc.Open(ctx, dest)
	if err != nil {
		return nil, fmt.Errorf("RFC logon to %s:%d failed: %w", dest.Host, dest.Port, err)
	}
	return c, nil
}

// rfcDestination resolves where an RFC call goes: this server's system, the RFC
// settings of the default .vsp.json system, and any per-call override.
func (s *Server) rfcDestination(params map[string]any) (saprfc.Params, error) {
	in := saprfc.Input{
		URL:      s.config.BaseURL,
		User:     s.config.Username,
		Password: s.config.Password,
		Client:   s.config.Client,
		Language: s.config.Language,
		RFCUser:  os.Getenv("SAP_USER"),
	}
	if pwd := os.Getenv("SAP_PASSWORD"); pwd != "" {
		in.RFCPassword = pwd
	}
	// Per-system RFC settings from the default .vsp.json system, when present.
	if cfg, _, err := config.LoadSystems(); err == nil && cfg != nil && cfg.Default != "" {
		if sys, err := cfg.GetSystem(cfg.Default); err == nil {
			in.RFCHost, in.RFCSysnr, in.RFCPort = sys.RFCHost, sys.RFCSysnr, sys.RFCPort
			if sys.RFCUser != "" {
				in.RFCUser = sys.RFCUser
			}
			if sys.RFCPassword != "" {
				in.RFCPassword = sys.RFCPassword
			}
		}
	}
	in.HostFlag = getStringParam(params, "host")
	in.SysnrFlag = getStringParam(params, "sysnr")
	in.UserFlag = getStringParam(params, "user")
	in.PortFlag = intParam(params, "port", 0)

	return saprfc.Resolve(in)
}

func rfcResult(v any) (*mcp.CallToolResult, bool, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, true, err
	}
	return mcp.NewToolResultText(string(b)), true, nil
}

func intParam(params map[string]any, key string, def int) int {
	if v, ok := getFloatParam(params, key); ok {
		return int(v)
	}
	return def
}
