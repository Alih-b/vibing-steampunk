package scripting

import (
	"context"
	"fmt"
	"strings"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
	lua "github.com/yuin/gopher-lua"
)

// A debug session the Lua engine holds for the life of a script.
//
// The bindings next door drove the ordinary ADT client, which is stateless — and
// a debug session cannot survive a stateless client, because attach returns a
// reference living in an ABAP roll area and the next call lands somewhere else.
// That is the same defect that kept the MCP debugger tools disabled, and it made
// every scripted debug flow in this package quietly useless: the calls returned,
// they simply did not refer to the debuggee anybody thought they did.
//
// The fix is the one the MCP server got: hold one session — a pinned RFC
// conversation, or one stateful ADT session where there is no gateway — and put
// every debug binding on it. The functions registered here deliberately shadow
// the older ones of the same name, so existing scripts keep working and start
// meaning what they say.

// DebuggerFactory opens a debug session on demand. The command line supplies it,
// because only it knows how the system is reached.
type DebuggerFactory func(ctx context.Context) (*saprfc.Debugger, func(), error)

// SetDebuggerFactory gives the engine a way to open a debug session. Without
// one, the debug bindings fail with an explanation rather than pretending.
func (e *LuaEngine) SetDebuggerFactory(f DebuggerFactory) {
	e.dbgFactory = f
	e.registerSessionDebugBindings()
}

// debugger returns the held session, opening it on first use. A script that
// never debugs never pays for a connection.
func (e *LuaEngine) debugger() (*saprfc.Debugger, error) {
	if e.dbg != nil {
		return e.dbg, nil
	}
	if e.dbgFactory == nil {
		return nil, fmt.Errorf("this engine has no debug session: run scripts through 'vsp lua', which opens one")
	}
	dbg, release, err := e.dbgFactory(e.ctx)
	if err != nil {
		return nil, err
	}
	e.dbg, e.dbgRelease = dbg, release
	return e.dbg, nil
}

// CloseDebugger releases the debuggee and the connection. Leaving a debuggee
// attached suspends somebody's work process until their call times out, so this
// runs when the engine closes whether a script remembered to detach or not.
func (e *LuaEngine) CloseDebugger() {
	if e.dbg == nil {
		return
	}
	_ = e.dbg.ADTDetach(e.ctx)
	_ = e.dbg.Close(e.ctx)
	if e.dbgRelease != nil {
		e.dbgRelease()
	}
	e.dbg, e.dbgRelease = nil, nil
}

func (e *LuaEngine) registerSessionDebugBindings() {
	for name, fn := range map[string]lua.LGFunction{
		// Breakpoints, through SAP's own resource — no ZADT_VSP.
		"setBreakpoint":    e.sesSetBreakpoint,
		"setStatementBP":   e.sesStatementBP,
		"setExceptionBP":   e.sesExceptionBP,
		"setMessageBP":     e.sesMessageBP,
		"setBadiBP":        e.sesBadiBP,
		"getBreakpoints":   e.sesGetBreakpoints,
		"deleteBreakpoint": e.sesDeleteBreakpoint,
		"clearBreakpoints": e.sesClearBreakpoints,
		"systemDebugging":  e.sesSystemDebugging,

		// The session.
		"listen": e.sesListen,
		"attach": e.sesAttach,
		"detach": e.sesDetach,

		// Movement.
		"stepOver":   e.sesStep("stepOver"),
		"stepInto":   e.sesStep("stepInto"),
		"stepReturn": e.sesStep("stepReturn"),
		"continue_":  e.sesStep("stepContinue"),
		"runToLine":  e.sesRunToLine,

		// State.
		"getStack":     e.sesStack,
		"locals":       e.sesLocals,
		"getVariables": e.sesLocals,
		"getVariable":  e.sesGetVariable,
		"setVariable":  e.sesSetVariable,
		"expand":       e.sesExpand,
		"frame":        e.sesFrame,

		// The recorder.
		"record": e.sesRecord,
	} {
		e.L.SetGlobal(name, e.L.NewFunction(fn))
	}
}

// fail pushes nil plus a message, the convention the other bindings use.
func fail(L *lua.LState, err error) int {
	L.Push(lua.LNil)
	L.Push(lua.LString(err.Error()))
	return 2
}

func (e *LuaEngine) sesSetBreakpoint(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	object := getString(L, 1)
	line := getOptInt(L, 2, 1)
	condition := getOptString(L, 3, "")

	bps, err := dbg.ADTAddBreakpoint(e.ctx, object, line, condition)
	if err != nil {
		return fail(L, err)
	}
	for _, r := range dbg.Rejected() {
		fmt.Fprintf(e.output, "! %s:%d not placed — %s\n", strings.ToUpper(object), line, r.ErrorMessage)
	}
	L.Push(breakpointsToLua(L, bps))
	return 1
}

// sesStatementBP breaks on every occurrence of an ABAP statement — "CALL
// FUNCTION", "SELECT", "LOOP". Powerful and blunt: it stops the user's every
// session, so keep it short-lived.
func (e *LuaEngine) sesStatementBP(L *lua.LState) int {
	return e.addBreakpoint(L, adt.Breakpoint{
		Kind: adt.BreakpointKindStatement, Statement: getString(L, 1),
	})
}

// sesExceptionBP breaks when an exception class is raised.
func (e *LuaEngine) sesExceptionBP(L *lua.LState) int {
	return e.addBreakpoint(L, adt.Breakpoint{
		Kind: adt.BreakpointKindException, Exception: strings.ToUpper(getString(L, 1)),
	})
}

// sesMessageBP breaks when one message is issued. All three parts are required:
// class, number and type — SAP rejects the request naming the missing one.
func (e *LuaEngine) sesMessageBP(L *lua.LState) int {
	return e.addBreakpoint(L, adt.Breakpoint{
		Kind:          adt.BreakpointKindMessage,
		MessageID:     strings.ToUpper(getString(L, 1)),
		MessageNumber: getString(L, 2),
		MessageType:   strings.ToUpper(getOptString(L, 3, "E")),
	})
}

// sesBadiBP breaks when a BAdI is called.
//
// There is no "badi" breakpoint kind — SAP answers "Invalid breakpoint kind
// badi" — so this is a statement breakpoint on CALL BADI, which is what SAP's
// own debugger does. It stops at every BAdI call, not only the named one; the
// name is checked in the script, from the stack.
func (e *LuaEngine) sesBadiBP(L *lua.LState) int {
	return e.addBreakpoint(L, adt.Breakpoint{
		Kind: adt.BreakpointKindStatement, Statement: "CALL BADI",
	})
}

// addBreakpoint adds one breakpoint of any kind to the set this session holds.
func (e *LuaEngine) addBreakpoint(L *lua.LState, bp adt.Breakpoint) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	bps, err := dbg.ADTAdd(e.ctx, bp)
	if err != nil {
		return fail(L, err)
	}
	L.Push(breakpointsToLua(L, bps))
	return 1
}

func (e *LuaEngine) sesGetBreakpoints(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	bps, err := dbg.ADTBreakpoints(e.ctx)
	if err != nil {
		return fail(L, err)
	}
	L.Push(breakpointsToLua(L, bps))
	return 1
}

func (e *LuaEngine) sesDeleteBreakpoint(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	bps, err := dbg.ADTDropBreakpoint(e.ctx, getString(L, 1))
	if err != nil {
		return fail(L, err)
	}
	L.Push(breakpointsToLua(L, bps))
	return 1
}

func (e *LuaEngine) sesClearBreakpoints(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	if err := dbg.ADTClearBreakpoints(e.ctx); err != nil {
		return fail(L, err)
	}
	L.Push(lua.LBool(true))
	return 1
}

// sesSystemDebugging opens SAP's own code to breakpoints. Off by default,
// because SAP keeps it off: a breakpoint in a system program is accepted and
// then never fires.
func (e *LuaEngine) sesSystemDebugging(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	on := true
	if L.GetTop() >= 1 {
		on = L.ToBool(1)
	}
	dbg.SystemDebugging(on)
	L.Push(lua.LBool(on))
	return 1
}

// sesListen waits for a debuggee and attaches to it. Listening and attaching are
// one call because a debuggee is attachable only while it waits.
func (e *LuaEngine) sesListen(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	timeout := getOptInt(L, 1, 60)
	user := getOptString(L, 2, "")

	who, _, err := dbg.ADTCatch(e.ctx, user, saprfc.IDEID, saprfc.TerminalID, timeout)
	if err != nil {
		return fail(L, err)
	}
	if who == nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("timeout: nobody stopped"))
		return 2
	}
	tbl := L.NewTable()
	L.SetField(tbl, "id", lua.LString(who.ID))
	L.SetField(tbl, "user", lua.LString(who.User))
	L.SetField(tbl, "program", lua.LString(who.Program))
	L.SetField(tbl, "include", lua.LString(who.Include))
	L.SetField(tbl, "line", lua.LNumber(who.Line))
	L.SetField(tbl, "name", lua.LString(who.Name))
	L.SetField(tbl, "type", lua.LString(who.Type))
	L.Push(tbl)
	return 1
}

func (e *LuaEngine) sesAttach(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	if _, err := dbg.ADTAttach(e.ctx, getString(L, 1), getOptString(L, 2, "")); err != nil {
		return fail(L, err)
	}
	L.Push(lua.LBool(true))
	return 1
}

func (e *LuaEngine) sesDetach(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	if err := dbg.ADTDetach(e.ctx); err != nil {
		return fail(L, err)
	}
	L.Push(lua.LBool(true))
	return 1
}

// sesStep returns a binding for one step kind, reporting where it landed —
// which is the only part of a step worth having in a script.
func (e *LuaEngine) sesStep(kind string) lua.LGFunction {
	return func(L *lua.LState) int {
		dbg, err := e.debugger()
		if err != nil {
			return fail(L, err)
		}
		if _, err := dbg.ADTStep(e.ctx, kind); err != nil {
			return fail(L, err)
		}
		info, err := dbg.StackInfo(e.ctx)
		if err != nil || info == nil || len(info.Stack) == 0 {
			L.Push(lua.LBool(true))
			return 1
		}
		L.Push(frameToLua(L, info.Stack[0]))
		return 1
	}
}

// sesRunToLine runs to a line in the current unit without spending one of the
// thirty breakpoints a user is allowed.
func (e *LuaEngine) sesRunToLine(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	uri := getString(L, 1)
	res, err := dbg.ADT(e.ctx, "POST", "/sap/bc/adt/debugger?method=stepRunToLine&uri="+uri,
		[]saprfc.ADTHeader{{Name: "Accept", Value: "application/xml"}}, nil)
	if err != nil {
		return fail(L, err)
	}
	if res.Status != 200 {
		return fail(L, fmt.Errorf("runToLine: ADT %d %s", res.Status, res.ReasonPhrase))
	}
	L.Push(lua.LBool(true))
	return 1
}

func (e *LuaEngine) sesStack(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	info, err := dbg.StackInfo(e.ctx)
	if err != nil {
		return fail(L, err)
	}
	tbl := L.NewTable()
	if info != nil {
		for i, f := range info.Stack {
			L.RawSetInt(tbl, i+1, frameToLua(L, f))
		}
	}
	L.Push(tbl)
	return 1
}

// sesLocals reads the stopped frame's own variables, walking @ROOT to @LOCALS so
// a script does not have to know the debugger's id scheme.
func (e *LuaEngine) sesLocals(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	vars, err := dbg.Locals(e.ctx)
	if err != nil {
		return fail(L, err)
	}
	L.Push(variablesToLua(L, vars))
	return 1
}

func (e *LuaEngine) sesGetVariable(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	var names []string
	for i := 1; i <= L.GetTop(); i++ {
		names = append(names, strings.ToUpper(L.ToString(i)))
	}
	vars, err := dbg.Vars(e.ctx, names)
	if err != nil {
		return fail(L, err)
	}
	L.Push(variablesToLua(L, vars))
	return 1
}

// sesSetVariable overwrites a value in the stopped frame. The next statement
// computes with it — this changes real execution, database writes included.
func (e *LuaEngine) sesSetVariable(L *lua.LState) int {
	// Before the session is opened: a refused write sends nothing.
	if err := e.checkVariableWrite("setVariable"); err != nil {
		return fail(L, err)
	}
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	name := strings.ToUpper(getString(L, 1))
	value := L.ToString(2)
	if err := dbg.SetVariable(e.ctx, name, value); err != nil {
		return fail(L, err)
	}
	L.Push(lua.LBool(true))
	return 1
}

func (e *LuaEngine) sesExpand(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	info, err := dbg.Expand(e.ctx, getString(L, 1))
	if err != nil {
		return fail(L, err)
	}
	if info == nil {
		L.Push(L.NewTable())
		return 1
	}
	L.Push(variablesToLua(L, info.Variables))
	return 1
}

// sesFrame moves the cursor to another stack entry, so the variables read next
// are that frame's — the caller's half of a call boundary.
func (e *LuaEngine) sesFrame(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	if err := dbg.GoToFrame(e.ctx, getString(L, 1)); err != nil {
		return fail(L, err)
	}
	L.Push(lua.LBool(true))
	return 1
}

// sesRecord walks the current unit and returns one table per stop: the whole
// history of a unit, with values, in one call.
func (e *LuaEngine) sesRecord(L *lua.LState) int {
	dbg, err := e.debugger()
	if err != nil {
		return fail(L, err)
	}
	max := getOptInt(L, 1, 200)
	withValues := false
	if L.GetTop() >= 2 {
		withValues = L.ToBool(2)
	}

	out := L.NewTable()
	n := 0
	_, err = dbg.Record(e.ctx, saprfc.RecordOptions{MaxStops: max, Redact: !withValues},
		func(r saprfc.StopRecord) error {
			n++
			rec := L.NewTable()
			L.SetField(rec, "seq", lua.LNumber(r.Seq))
			L.SetField(rec, "kind", lua.LString(r.Kind))
			L.SetField(rec, "program", lua.LString(r.Program))
			L.SetField(rec, "include", lua.LString(r.Include))
			L.SetField(rec, "line", lua.LNumber(r.Line))
			L.SetField(rec, "event", lua.LString(r.Event))
			L.SetField(rec, "depth", lua.LNumber(r.Depth))
			vars := L.NewTable()
			for k, v := range r.Vars {
				L.SetField(vars, k, lua.LString(v))
			}
			L.SetField(rec, "vars", vars)
			L.RawSetInt(out, n, rec)
			return nil
		})
	if err != nil {
		return fail(L, err)
	}
	L.Push(out)
	return 1
}

// --- shaping Go values for Lua ---

func frameToLua(L *lua.LState, f adt.DebugStackEntry) *lua.LTable {
	tbl := L.NewTable()
	L.SetField(tbl, "position", lua.LNumber(f.StackPosition))
	L.SetField(tbl, "program", lua.LString(f.ProgramName))
	L.SetField(tbl, "include", lua.LString(f.IncludeName))
	L.SetField(tbl, "line", lua.LNumber(f.Line))
	L.SetField(tbl, "event", lua.LString(f.EventName))
	L.SetField(tbl, "eventType", lua.LString(f.EventType))
	L.SetField(tbl, "uri", lua.LString(f.StackURI))
	L.SetField(tbl, "systemProgram", lua.LBool(f.SystemProgram))
	return tbl
}

// variablesToLua indexes by name, and keeps the parallel list so a script can
// iterate in the order SAP returned.
func variablesToLua(L *lua.LState, vars []adt.DebugVariable) *lua.LTable {
	tbl := L.NewTable()
	for i, v := range vars {
		name := v.Name
		if name == "" {
			name = v.ID
		}
		one := L.NewTable()
		L.SetField(one, "name", lua.LString(name))
		L.SetField(one, "value", lua.LString(strings.TrimSpace(v.Value)))
		L.SetField(one, "type", lua.LString(v.DeclaredTypeName))
		L.SetField(one, "metaType", lua.LString(string(v.MetaType)))
		L.SetField(one, "id", lua.LString(v.ID))
		L.SetField(one, "complex", lua.LBool(v.IsComplexType()))
		L.SetField(one, "lines", lua.LNumber(v.TableLines))
		L.RawSetInt(tbl, i+1, one)
		L.SetField(tbl, name, one)
	}
	return tbl
}

func breakpointsToLua(L *lua.LState, bps []adt.Breakpoint) *lua.LTable {
	tbl := L.NewTable()
	for i, bp := range bps {
		one := L.NewTable()
		L.SetField(one, "id", lua.LString(bp.ID))
		L.SetField(one, "kind", lua.LString(string(bp.Kind)))
		L.SetField(one, "uri", lua.LString(bp.URI))
		L.SetField(one, "line", lua.LNumber(bp.Line))
		L.SetField(one, "object", lua.LString(bp.ObjectName))
		L.RawSetInt(tbl, i+1, one)
	}
	return tbl
}
