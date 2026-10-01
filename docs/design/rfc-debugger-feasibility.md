# Driving the ABAP Debugger over Classic RFC — Feasibility Study

Status: **research / recommendation** — nothing implemented.
Date: 2026-08-20
Live system probed: **A4H**, NW **7.58**, kernel **793** (`RFC_SYSTEM_INFO`), read-only, service user `CLAUDE`.
Tooling: `vsp rfc {ping,info,search,describe,read-table}` (open-rfc-go client), plus a source survey of `vibing-steampunk`.

---

## 0. Verdict summary

| Capability | Verdict over classic RFC | Needs Z code? |
|---|---|---|
| **External breakpoints** (set / list / delete for a user) | **Feasible** | Yes — thin facade for set/delete. *Listing* needs none (`ABDBG_EXTDBPS` is a transparent table). |
| **Listen for a debuggee** | **Feasible** — a blocking RFC call is a perfectly good long-poll, and there is already an RFC-enabled SAP FM that does exactly this (`TPDA_ADT_START_LISTENER`) | No, for listen. Yes, to get the debuggee payload back usefully. |
| **Attach + step** | **Feasible, and structurally *better* than HTTP** — but only from a **pinned, stateful RFC connection** held open for the whole session | Yes — thin facade over `IF_TPDAPI_SESSION` |
| **Call stack + variables** | **Feasible** (same session), with one caveat: the rich ADT variable model would have to be re-implemented in the facade | Yes |
| **Asynchronous "breakpoint hit" events** | **Not needed, and not the right shape.** The debugger protocol is strict request/response plus a blocking listen. Where async *is* wanted, **polling `ABDBG_ACTIVATION` over `RFC_READ_TABLE`** works today with zero ABAP code. RFC callbacks are a viable but unnecessary third option; a registered RFC server is the wrong shape. | No |

**One-line recommendation:** build a **`vsp-debugd` session-holder daemon** that keeps one pinned stateful RFC conversation open per debug session and speaks to a small `Z_VSP_DBG_*` RFC function group wrapping TPDAPI. This is a *strictly better* transport than the ZADT_VSP WebSocket — the WebSocket exists **only** to provide a stateful ABAP roll area, which a stateful RFC connection provides natively and more robustly.

---

## 1. Why this question has a clean answer

The single most useful discovery in this study is not on the SAP side — it is in vsp's own ADR.

`docs/adr/001-websocket-stateful-debugging.md` (status *PROPOSAL / PARKED*) already diagnoses the failure precisely:

> "Each MCP tool call may spawn a separate process / HTTP sessions are not shared between tool calls / **The debugger listener catches the debuggee, but attach/step operations fail due to session mismatch** / Go's HTTP client is stateless by design"

and `embedded/abap/README.md:16-30` states the WebSocket's whole purpose:

> "The WebSocket handler enables **stateful operations** not available through standard ADT REST APIs … HTTP REST: Cannot maintain debug context / **No TPDAPI access**. WebSocket: Persistent debug session / Full debugger integration."

So the requirement is **not** push messaging. It is *"keep one ABAP roll area alive across many client operations, and be able to block a call for up to 240 s."* Two independent confirmations that push is not needed:

- The Go WS client defines `DebugEvent` and an `Events chan` (`pkg/adt/websocket.go:22-34`) — **nothing ever writes to it**. Every WS message is strict request/response keyed by `id` (`pkg/adt/websocket_base.go:219-227`).
- The ABAP handler's listen is itself a blocking call: `start_listener_for_user( i_timeout )` then `get_waiting_debuggees( )` (`src/zcl_vsp_debug_service.clas.abap:263-305`).

A classic **stateful RFC connection** satisfies both requirements natively: the same CPIC conversation reuses the same ABAP user session, so function-group globals and object references survive between calls, and an RFC call can block for as long as the gateway/`rdisp` timeouts allow.

There is also a structural bug worth naming, because it explains the "unreliable" label better than any timeout theory. The shipped debug loop is a **hybrid**: breakpoints go over the WebSocket, but attach/step/stack go over ADT HTTP (`cmd/vsp/debug.go` — `wsClient.SetLineBreakpoint` at `:150` vs `s.client.DebuggerAttach` at `:367`), and the HTTP client sends `X-sap-adt-sessiontype: stateless` because `SessionStateless` is the default (`pkg/adt/config.go:192`) and no debugger call passes `Stateful: true`. The breakpoint is registered in the WebSocket roll area; the attach happens in a different one. Notably, the WS client's own `listen/attach/step/getStack/getVariables` methods (`pkg/adt/websocket_debug.go:128-362`) **have no callers anywhere in the repo**. The all-RFC design removes the split by construction.

---

## 2. Live probe results — RFC-enabled surface

`vsp rfc search` filters on `TFDIR-FMODE = 'R'`; `--all` drops the filter. Cross-checked directly against `TFDIR`.

### 2.1 The find: SAP's own ADT debugger integration is RFC-enabled

`TFDIR` where `PNAME = 'SAPLTPDA_ADT_DEBUGGER'` — **all three FMs are `FMODE = 'R'`**:

| FM | RFC | Interface (from `rfc describe`) |
|---|---|---|
| `TPDA_ADT_START_LISTENER` | **R** | IN `I_IDE_ID`(C32) `I_TERMINAL_ID`(C32) `I_REQUEST_USER`(C12) `I_TIMEOUT`(int) `I_FLG_CHECK`(C1) → OUT `E_FLG_OK`(C1); exception `FAILED` |
| `TPDA_ADT_INTEGRATION_DEBUGGER` | **R** | IN `UNIT_TEST` — *"Serializable Unit Test Class inherited from CL_TPDA_ADT_TEST_DEBUGGER"* |
| `TPDA_ADT_INTEGRATION_DEBUGGEE` | **R** | IN `CLSNAME`(C30) `DESTINATION`(C32) → OUT `E_FLG_ENDED` |

> Note on lengths: this CLI's JSON-Schema `maxLength` is half the DDIC char length (`I_REQUEST_USER` maxLength 6 = `XUBNAME` CHAR12; `E_FLG_OK` maxLength 0 = CHAR1). So `I_IDE_ID`/`I_TERMINAL_ID` are **CHAR32**, matching `SYSUUID_C32` in `ABDBG_LISTENER`.

Two things follow. First, `TPDA_ADT_START_LISTENER` is a **ready-made, SAP-supported, RFC-callable blocking listener** — the exact equivalent of `POST /sap/bc/adt/debugger/listeners`, with the same `ideId`/`terminalId`/`requestUser`/`timeout`/`checkConflict` parameter set vsp already builds for the HTTP call (`pkg/adt/debugger.go:567-634`). Second, the `INTEGRATION_DEBUGGER` / `INTEGRATION_DEBUGGEE` pair is **SAP's own harness for driving the full ADT debugger stack from a separate session over RFC**, with the debuggee side taking a `DESTINATION`. SAP tests this path; it is not an accident that it is `FMODE = 'R'`.

### 2.2 The TPDAPI test harness — also RFC-enabled

| FM | RFC | Interface |
|---|---|---|
| `STPDAPI_TEST_ATTACHER` | **R** | IN `I_CASEID`(C30) `I_ID`(C32) **`I_DEBUGGEE_ID`(C32)** |
| `STPDAPI_TEST_ATTACH_DEBUGGER` | **R** | IN `I_CASEID` `I_ID` |
| `STPDAPI_TEST_ATTACH_DEBUGGEE` | **R** | IN `I_CASEID` `I_ID` |
| `STPDAPI_TEST_ATTACH_PING` | **R** | — |
| `TPDAPI_TEST_START_LISTENER` | **R** | IN `I_IDE_ID` `I_REQUEST_USER` `I_TERMINAL_ID` `I_TIMEOUT` `I_STR_DB` |
| `TPDAPI_TEST_DEBUGGER` | **R** | IN `I_METHOD` `I_TERMINAL_ID` → OUT `E_TAB_MSG` |
| `TPDAPI_TEST_RFC` | **R** | IN `I_FLG_DELETE_MODE` |
| `TPDAPI_TEST_DELETE_MODE` | **R** | — |

`STPDAPI_TEST_ATTACHER` taking a **`I_DEBUGGEE_ID`** is direct evidence that *attach-by-debuggee-id from a foreign RFC session* is a supported operation — that is exactly the "hard part" the brief flagged.

### 2.3 Kernel / remote-debugging FMs

| FM | RFC | Interface | Use |
|---|---|---|---|
| `SYSTEM_DEBUG_ATTACH_TPDA` | **R** | IN `DBGKEY` | kernel-level attach; `DBGKEY` is a column of `ABDBG_ACTIVATION` |
| `SRDEBUG_FRAMEWORK_ACTIVATE` | **R** | IN `DEBUG_USER` `GUI_RFCSI` `LOGON_GROUP` `SAPGUI_HOST` `ALL_SERVERS` → OUT `DEBUG_ID` | SM59-style remote debugging (SAP GUI oriented) |
| `SRDEBUG_START` / `SRDEBUG_CONTINUE` / `SRDEBUG_STOP` / `SRDEBUG_FRAMEWORK_DEACTIVATE` | **R** | `SRDEBUG_CONTINUE`: `DEBUG_ID` `DEBUG_USER` `GUI_RFCSI` | ditto |
| `DEBUGGEE_STOP` | **R** | IN `DEBUGGEE_SESSION_ID` `KIND` → OUT `RC` | terminate a debuggee |
| `RFC_EXT_DEBUGGING_IP` | **R** | IN `IP` | register the frontend IP for external debugging of *this* RFC session |
| `TH_GET_DEBUG_INFO` | **R** | → `DEBUGGING_COUNT` | *called live: returned `0`* |
| `TH_RESET_DEBUGGING` | **R** | — | (write — not called) |
| `BGRFC_PREPARE_EXT_DEBUGGING` | **R** | — | bgRFC unit debugging |

The `SRDEBUG_*` family is the classic SAP-GUI remote-debugging framework (`SM59`-ish, `GUI_RFCSI`/`SAPGUI_HOST` parameters). It is RFC-enabled but assumes a SAP GUI front end to pop the debugger into. **Not the right path for vsp** — noted for completeness.

### 2.4 What is *not* RFC-enabled — every breakpoint FM

`TFDIR-FMODE = ''` (blank) for **all** of these:

`RS_SET_BREAKPOINT`, `RS_DELETE_BREAKPOINT`, `RS_DELETE_BREAKPOINTS_ALL`, `RS_GET_BREAKPOINTS`, `RS_GET_ALL_BREAKPOINTS`, `RS_SHOW_BREAKPOINTS`, `RS_SHOW_BREAKPOINT_LISTE` (+ all `_MA` variants in `SAPLMA_BREA`), `SYSTEM_DEBUG_BREAKPOINTS`, `SYSTEM_DEBUG_SET_BREAKPOINTS`, `SYSTEM_DEBUG_UPDT_BREAKPOINTS`, `SYSTEM_DEBUG_GET_BP_POSITION`, `SYSTEM_DEBUG_AUTHORITY_CHECK`, `SYSTEM_DBG_ST_{SET,GET,DEL,CHK}_BREAKPOINT`, `ECATT_DEBUG_BREAKPOINT_MAINT`.

`vsp rfc search '*BREAKPOINT*'` (RFC-filtered) returns **`null`** — there is no RFC-enabled breakpoint FM on this system, in any namespace.

Also absent entirely on A4H: **`RFC_ABAP_INSTALL_AND_RUN`** (`TFDIR LIKE 'RFC_ABAP%'` → empty). The "compile-and-run arbitrary ABAP over RFC" escape hatch is not available here. (ZADT_VSP references it, but over its own channel.)

### 2.5 The ABAP debugger API: `IF_TPDAPI_*`, package `STPDA_API`

`TADIR` puts all of `CL_TPDAPI_*` / `IF_TPDAPI_*` / `CE_TPDAPI_*` / `CX_TPDAPI_*` in a dedicated package **`STPDA_API`** — a deliberate API package, not internals. The interface surface (from `SEOCOMPO`) is a complete debugger:

**`IF_TPDAPI_SERVICE`** — `START_LISTENER_FOR_USER`, `START_LISTENER_FOR_TERMINAL_ID`, `STOP_LISTENER_FOR_USER`, `STOP_LISTENER_FOR_TERMINAL_ID`, `CHECK_LISTENER_CONFLICT_USER`, `CHECK_LISTENER_CONFLICT_TERMID`, **`GET_WAITING_DEBUGGEES`**, `GET_ACTIVE_LISTENERS`, **`ATTACH_DEBUGGEE`**, `GET_ATTACHED_SESSION`, `ACTIVATE_SESSION_FOR_EXT_DEBUG`, `NOTIFY_DEBUGGEE_SESSION_2_STOP`, `GET_STATIC_BP_SERVICES`, `CHECK_USER`, `GET_STATEMENTS`; constants `C_DEBUG_MODE_USER` / `C_DEBUG_MODE_TERMINAL`; events `ATTACHED` / `DETACHED`.

**`IF_TPDAPI_SESSION`** — `GET_CONTROL_SERVICES`, `GET_DATA_SERVICES`, `GET_BP_SERVICES`, `GET_WP_SERVICES`, `GET_STACK_HANDLER`, `GET_SOURCE`, `GET_SYSTEM_AREA`, `GET_LOADED_PROGRAMS`, `GET_DEBUGGER_STATUS`, `GET_DEBUGGEE_SESSION_ID`, `GET_SESSION_ID`, `DEBUGGEE_EXISTS`, `IS_RFC`, **`IS_POST_MORTEM`**, `IS_NON_EXCLUSIVE`, `START_KERNEL_DEBUGGER`, `SET_SETTINGS`, event `DEBUGGER_EVENT`.

**`IF_TPDAPI_CONTROL_SERVICES`** — `DEBUG_STEP`, `RUN_TO_LINE`, `JUMP_TO_LINE`, `END_DEBUGGER`, `END_DEBUGGEE`, `DO_COMMIT_WORK`, `DO_ROLLBACK_WORK`, `DO_GARBAGE_COLLECTOR`, `DO_MEMORY_SNAPSHOT`, `GET_MEMORY_SIZES`, event `DEBUGGER_EVENT`.

**`CE_TPDAPI_STEPTYPE`** — `INTO`, `OVER`, `OUT`, `CONTINUE`, `INTO_EXPRESSION`, `OVER_EXPRESSION`, `CONTINUE_DEBUGSTEP`, `CONTINUE_ANDSTOPAPP`, `CONTINUE_ANDRESTARTAPP`, `STOP`. This is a superset of vsp's `stepInto/stepOver/stepReturn/stepContinue`.

**`IF_TPDAPI_STATIC_BP_SERVICES`** (external breakpoints, *no attached debuggee required*) — `SET_EXTERNAL_BP_CONTEXT_USER`, `SET_EXTERNAL_BP_CONTEXT_TERMID`, `GET_EXTERNAL_BP_CONTEXT`, `CREATE_LINE_BREAKPOINT`, `CREATE_STATEMENT_BREAKPOINT`, `CREATE_EXCEPTION_BREAKPOINT`, `CREATE_MESSAGE_BREAKPOINT`, `CREATE_BREAKPOINT_FROM_STRING`, `GET_BREAKPOINTS`, `GET_BREAKPOINT_FROM_ID`, `DELETE_BREAKPOINT`, `NOTIFY_DBG_SESS_IDS`, `GET_ABDBG_INTERFACE`.

**`IF_TPDAPI_DATA_SERVICES`** — `GET_DATA`, `GET_LOCALS`, `GET_GLOBALS`, `GET_PARAMETERS`, `GET_SYMBQUICK`, `GET_ME_OBJREF`, `GET_SYSTEM_INTERNALS`, `DATA_CHANGED`, `IS_AUTHORIZED_FOR_DATA_CHANGE`; plus `IF_TPDAPI_DATA_{SIMPLE,STRUC,TABLE,OBJREF,DATREF,STRING,ENUM,SET_VALUE}` for the typed value model.

**`IF_TPDAPI_EVENT`** — `C_ID_BREAKPOINTS`, `C_ID_WATCHPOINTS`, `C_ID_EXC_OCCURRED`, `C_ID_NEW_BREAKPOINTS`, `C_ID_ROLLAREA`, `C_ID_SLASHH_ACTIVATION`, `C_ID_LAYER_ENTRY/EXIT`, `C_ID_WP_EXPIRED`, … with `IF_TPDAPI_EVENT_BREAKPOINT~GET_BP_IDS`. **These are pull events**: `GET_EVENTINFOS` on the session, read *after* the debuggee stops. They are not a push channel.

This matches exactly what ZADT_VSP already calls — `cl_tpdapi_service=>s_get_instance( )`, `activate_session_for_ext_debug`, `start_listener_for_user`, `get_waiting_debuggees`, `attach_debuggee`, `get_control_services( )->debug_step( ce_tpdapi_steptype=>… )`, `get_stack_handler( )->get_stack( )`, `get_static_bp_services( )->create_line_breakpoint( )` (`src/zcl_vsp_debug_service.clas.abap`). **The ABAP logic for an RFC facade already exists and is proven; only the transport would change.**

### 2.6 The `ABDBG_*` registry — server-side state, readable over plain RFC

This is the answer to "is the state in the session or on the server?", and it is a **transparent-table** answer:

| Table | Class | Key fields | Meaning |
|---|---|---|---|
| **`ABDBG_ACTIVATION`** | TRANSP | `CLIENT`, **`DEBUGGEE_ID`** (C32) | *waiting debuggee registry* — also `TERMINAL_ID`, `IDE_ID`, `DEBUGGEE_USER`, `PRG_CURR`, `INCL_CURR`, `LINE_CURR`, `RFCDEST`, `APPLSERVER`, `SYSID`, `SYSNR`, **`DBGKEY`**, `DBGEE_KIND`, `DUMPID`/`DUMPDATE`/`DUMPTIME`/`DUMPHOST`/`DUMPMODNO`, `LISTENER_CTX_ID`, `TSTMP` |
| **`ABDBG_LISTENER`** | TRANSP | `CLIENT`, `TERMINAL_ID`, `IDE_ID` | *active listener registry* — `SERVER` (MSNAME2), **`CONTEXT_ID`**, `EVENT`, `TSTMP`, `FLAGACT_PMORT_{RFC,HTTP,DIALOG}` |
| **`ABDBG_EXTDBPS`** | TRANSP | `CLIENT`, `USERNAME`, `BP_INDEX` | *external breakpoints* — `RQ_USER`, `RQ_TERMID`, `RQ_IDEID`, `TIMESTAMP`, `ATTRIBUTES` (STRG), `BREAKPOINT` (STRG) |
| `ABDBG_EXTDBPS_V` | VIEW | | view over the above |
| `ABDBG_IDEID_USR` | TRANSP | `IDEID` | IDE-id → user mapping |
| `ABDBG_BPS`, `ABDBG_INFO`, `ABDBG_ACTIVATION`, `ABDBG_LISTENERXT`, `ABDBG_EXTDBP_LCK`, `ABDBG_TRACE` | TRANSP | | supporting state |

Live contents on A4H at probe time: **all empty**, and `TH_GET_DEBUG_INFO → DEBUGGING_COUNT = 0` — a consistent, clean baseline (nobody was debugging).

Three consequences:

1. **`GET_WAITING_DEBUGGEES` has a DB-table twin.** A Go daemon can discover waiting debuggees with a plain `RFC_READ_TABLE ABDBG_ACTIVATION` — *no ABAP code at all*. Fields `PRG_CURR`/`INCL_CURR`/`LINE_CURR` even give the stop location, and `DUMPID`/`DUMPDATE` expose **post-mortem (short-dump) debuggees**.
2. **Breakpoint *listing* needs no ABAP code either** — but `BREAKPOINT` and `ATTRIBUTES` are `STRG` (LOB) columns, which `RFC_READ_TABLE` cannot return. You get `USERNAME`, `BP_INDEX`, `RQ_USER`, `RQ_IDEID`, `RQ_TERMID`, `TIMESTAMP` for free; the *payload* (program/line/condition) requires `IF_TPDAPI_STATIC_BP_SERVICES~GET_BREAKPOINTS`.
3. **`ABDBG_LISTENER.CONTEXT_ID` + `SERVER` prove the listener is bound to one specific ABAP session on one specific app server.** The debuggee is routed to that context. This is the crux of the whole study — see §3.

---

## 3. Is the state in the RFC conversation, or server-side? (the daemon question)

Asked directly by the coordinator, and the evidence answers it in two parts.

**Discovery is server-side and stateless.** `ABDBG_ACTIVATION` / `ABDBG_LISTENER` / `ABDBG_EXTDBPS` are ordinary client-dependent transparent tables. Any fresh RFC logon can read them. Enumerating waiting debuggees, listing breakpoint keys, and checking "is anything debugging right now" are all stateless operations.

**Attachment and control are session-bound.** Three independent pieces of evidence:

- `ABDBG_LISTENER` stores `SERVER` + `CONTEXT_ID` per listener. A listener *is* a registered ABAP session context, not a row of intent.
- `IF_TPDAPI_SERVICE~ATTACH_DEBUGGEE` returns a `REF TO if_tpdapi_session`; everything else (`GET_CONTROL_SERVICES`, `GET_DATA_SERVICES`, `GET_STACK_HANDLER`) hangs off that object reference. Object references live in a roll area. There is no "re-materialise session from an id" API in `IF_TPDAPI_SERVICE` — the closest is `GET_ATTACHED_SESSION`, which returns the session attached *to the calling session*.
- ZADT_VSP holds precisely these as instance attributes for the socket's lifetime — `mo_dbg_session TYPE REF TO if_tpdapi_session`, `mo_static_bp_services`, `mt_bp_mappings TYPE … REF TO if_tpdapi_bp` (`src/zcl_vsp_debug_service.clas.abap:30-44`) — and tears them all down in `on_disconnect` (`:173-202`). SAP's own ADT resources do the same via a **stateful ICF session**; vsp's ADT client even models this (`SessionStateful` / `sap-contextid`, `pkg/adt/http.go:255`, `:412-445`) but never turns it on for debugger calls.

Breakpoints sit in between and deserve their own note: **external** breakpoints persist server-side in `ABDBG_EXTDBPS` keyed by user, so they outlive any session — but the *handle* used to delete one (`IF_TPDAPI_BP` object reference) does not. ZADT_VSP works around this with its own `mt_bp_mappings` UUID→ref table, which is why breakpoint deletion breaks when the socket drops. A facade should key deletion off `BP_INDEX` / `GET_BREAKPOINT_FROM_ID` instead of a live reference, so breakpoint management becomes genuinely stateless.

**Therefore: yes, the daemon is necessary, and it is necessary for exactly one reason** — to own the ABAP roll area that holds the `if_tpdapi_session` reference between "attach" and "step" and "getStack". That is the same reason the WebSocket exists. Keeping the RFC connection open is not an optimisation; it *is* the mechanism.

**Caveat that must be verified (E2 below): does `open-rfc-go` currently give you a pinned session?** No. `rfc.Client` is pool-backed (`rfc/client.go` — `pool.Pool[*lifecycle.Managed]`, `Destination.Pool`), and successive `Call`s may land on different pooled sessions, i.e. different ABAP roll areas. A debug session needs either `PoolConfig{MaxSize: 1}` plus serialized access, or — better — a new explicit API such as `Client.Pin(ctx) (*Conversation, error)` that leases one `lifecycle.Managed` for the caller's exclusive use until `Close()`. **This is the one library change the design depends on.**

---

## 4. Async events: callbacks vs. registered server vs. polling

Three candidate shapes were evaluated.

**(a) Polling `ABDBG_ACTIVATION` — recommended, works today, zero ABAP.** A daemon side-connection issues `RFC_READ_TABLE ABDBG_ACTIVATION` every 1–2 s and sees new debuggees appear with their `DEBUGGEE_ID`, user, program, include, line, and `DBGKEY`. Cost is trivial (an empty-table read against a tiny table). This covers "somebody hit my breakpoint" and, uniquely, "a short dump was captured" (`DUMPID`) — post-mortem debugging is a genuinely new capability vsp does not have today. **Verified read-only on the live system.**

**(b) A blocking listen call with an RFC callback — viable, and elegant, but unnecessary.** `open-rfc-go` services server-initiated callbacks *while awaiting an outstanding call's response* (`internal/client/session.go:565 CallWithCallbacks`, wired through `rfc/call.go:90` and `Destination.Callbacks`). So `Z_VSP_DBG_LISTEN` could block for 240 s and, on catching a debuggee, `CALL FUNCTION 'Z_VSP_DBG_EVENT' DESTINATION 'BACK'` to hand the event to Go mid-call. SAP's own `TPDA_ADT_INTEGRATION_DEBUGGEE( CLSNAME, DESTINATION )` is shaped exactly like this. But note the callback only fires *inside* an outstanding call — it buys you nothing over simply returning the debuggee in the `LISTEN` export parameters, and it adds a whole failure mode. **Use it only if you later want progress/log streaming during a long step.**

**(c) A registered RFC server (SM59 type T / `internal/rfcserver`) — wrong shape.** It would require the ABAP side to hold an SM59 destination pointing at the developer's laptop, would need gateway ACLs (`reginfo`), and would deliver the event on a *different* conversation than the one holding the debugger session — reintroducing the exact session-mismatch bug the ADR already documents. It also would not help: the debuggee is *already* parked and waiting; nothing needs to be pushed urgently. Additionally `internal/rfcserver` is unexported and early-stage (M8: SM59 type-3 Connection Test green). **Not recommended for the debugger.**

---

## 5. Recommended architecture — `vsp-debugd`

```
  MCP tool call            (short-lived, stateless)
  vsp CLI invocation       (short-lived, stateless)
        │  unix socket  /  local HTTP  /  MCP-over-stdio
        ▼
┌──────────────────────────────────────────────────────────────┐
│  vsp-debugd  (long-lived, one per developer)                 │
│                                                              │
│  session registry:  sid → { pinned RFC conversation,         │
│                             debuggeeID, ideID, terminalID,   │
│                             lastSeen, idleTimeout }          │
│                                                              │
│   ├── PINNED conversation ──── stateful RFC ────────────┐    │
│   │    LISTEN / ATTACH / STEP / STACK / VARS / DETACH   │    │
│   └── side connection (pooled) ── stateless RFC ────────┤    │
│        RFC_READ_TABLE ABDBG_ACTIVATION  (poll)          │    │
│        RFC_READ_TABLE ABDBG_EXTDBPS     (list BPs)      │    │
│        Z_VSP_DBG_BP_*                   (set/delete BP) │    │
└─────────────────────────────────────────────────────────┼────┘
                                                          ▼
                                            A4H  ──  Z_VSP_DBG_* (FG)
                                                     └─ IF_TPDAPI_*
```

### 5.1 Daemon lifecycle

1. **Start** — lazily, on the first debug tool call (`vsp debugd` also startable by hand). Binds `$XDG_RUNTIME_DIR/vsp-debugd-<system>.sock`; second instance detects the socket and exits.
2. **`session.open(system)`** — pins one RFC conversation (`Client.Pin`), calls `Z_VSP_DBG_ACTIVATE` (→ `activate_session_for_ext_debug`), returns `sid`.
3. **`session.listen(sid, timeout≤240)`** — one blocking RFC call on the pinned conversation. The daemon holds it; the *client* call returns immediately with a ticket and polls, so no MCP tool ever blocks for 240 s. Meanwhile the side connection polls `ABDBG_ACTIVATION` so a debuggee is visible even if the blocking call is late.
4. **`session.attach(sid, debuggeeId)`** — same pinned conversation. From here the roll area holds `mo_session`.
5. **`step / stack / variables / setVariable / gotoStack`** — same pinned conversation, serialized by a per-session mutex.
6. **`session.close(sid)`** — `Z_VSP_DBG_END` (→ `end_debugger`, `stop_listener_for_user`), unpin, drop registry entry.
7. **Timeouts** — per-session idle timeout (default 10 min) fires `session.close`; daemon self-exits after N minutes with zero sessions. A `defer`-style teardown on conversation loss must also run, because a dropped conversation leaves a `ABDBG_LISTENER` row behind.
8. **Crash safety** — on start, the daemon reads `ABDBG_LISTENER` for its own `IDE_ID` and reaps stale listeners via `Z_VSP_DBG_STOP_LISTENER`.

### 5.2 `Z_VSP_DBG_*` facade sketch (function group `ZVSP_DBG`)

Global state in the function group's `TOP` include — this is what the pinned conversation buys:

```abap
DATA: go_svc     TYPE REF TO if_tpdapi_service,
      go_session TYPE REF TO if_tpdapi_session,
      go_static  TYPE REF TO if_tpdapi_static_bp_services.
```

| FM | RFC | Wraps | Maps to vsp tool |
|---|---|---|---|
| `Z_VSP_DBG_ACTIVATE` | R | `cl_tpdapi_service=>s_get_instance`, `activate_session_for_ext_debug` | (implicit in `DebuggerListen`) |
| `Z_VSP_DBG_LISTEN` | R | `start_listener_for_user` + `get_waiting_debuggees`; exports a table of debuggees (id, user, prog, incl, line, kind, isAttachImpossible, dump*) | **`DebuggerListen`** |
| `Z_VSP_DBG_STOP_LISTENER` | R | `stop_listener_for_user` / `_terminal_id` | — |
| `Z_VSP_DBG_ATTACH` | R | `attach_debuggee( i_debuggee_id )` → `go_session`; exports session id, debuggee session id, `is_post_mortem`, `is_rfc` | **`DebuggerAttach`** |
| `Z_VSP_DBG_STEP` | R | `go_session->get_control_services( )->debug_step( ce_tpdapi_steptype=>… )`, plus `run_to_line` / `jump_to_line` | **`DebuggerStep`** |
| `Z_VSP_DBG_STACK` | R | `get_stack_handler( )->get_stack( )` | **`DebuggerGetStack`** |
| `Z_VSP_DBG_VARS` | R | `get_data_services( )->get_locals/get_globals/get_parameters/get_data`, walked via `IF_TPDAPI_DATA_{SIMPLE,STRUC,TABLE,OBJREF}` into a flat parent-id/child rows table | **`DebuggerGetVariables`** |
| `Z_VSP_DBG_SET_VAR` | R | `IF_TPDAPI_DATA_SET_VALUE` | (Lua `setVariable`, force-replay) |
| `Z_VSP_DBG_END` | R | `end_debugger` / `end_debuggee` | **`DebuggerDetach`** |
| `Z_VSP_DBG_BP_SET` | R | `get_static_bp_services( )`, `set_external_bp_context_user`, `create_{line,statement,exception,message}_breakpoint`; returns `BP_INDEX` | **`SetBreakpoint`** |
| `Z_VSP_DBG_BP_GET` | R | `get_breakpoints( )` (full payload; the DB read only gives keys) | **`GetBreakpoints`** |
| `Z_VSP_DBG_BP_DEL` | R | `get_breakpoint_from_id( )` + `delete_breakpoint( )` | **`DeleteBreakpoint`** |
| `Z_VSP_DBG_EVENTS` | R | `IF_TPDAPI_EVENT~GET_EVENTINFOS` after a stop | (new: *why* did we stop) |

Notes on the facade:

- Exceptions: map `cx_tpdapi_failure`, `cx_tpdapi_not_authorized`, `cx_tpdapi_debuggee_ended`, `cx_tpdapi_invalid_param`, `cx_abdbg_actext_conflict_lis`, `cx_abdbg_actext_lis_timeout` to **typed RFC exceptions** — `open-rfc-go` surfaces these as typed ABAP exceptions, which is materially better than the WS layer's JSON error strings.
- Everything is **flat tables of scalars** — no ABAP JSON serialization at all. This eliminates the ZADT_VSP handler's most fragile component, its regex JSON parser (`FIND REGEX '"params"\s*:\s*(\{[^}]*\})'`, which cannot parse nested objects).
- Two known ZADT_VSP gaps to fix while porting: `SetMethodBreakpoint`'s `method` parameter is **silently ignored**, and `condition` is stored but never passed to TPDAPI; `handle_get_variables` returns only 8 hard-coded `SY-*` fields with `scope:"locals"` marked "(planned)".
- Deliberately *not* ported from the ADT HTTP layer: the `STPDA_ADT_VARIABLE` XML metaType model and the `/sap/bc/adt/debugger/batch` multipart batching. The first must be re-implemented in the facade (see §7); the second is unnecessary once one conversation serves all calls.

### 5.3 Daemon local interface

Keep it boring: newline-delimited JSON-RPC over a unix socket.

```
→ {"m":"session.open","p":{"system":"a4h"}}                  ← {"sid":"s1"}
→ {"m":"bp.set","p":{"sid":"s1","program":"ZFOO","line":42}} ← {"bpIndex":1}
→ {"m":"listen.start","p":{"sid":"s1","timeout":240}}        ← {"ticket":"t1"}
→ {"m":"listen.poll","p":{"ticket":"t1"}}                    ← {"debuggees":[…]}
→ {"m":"attach","p":{"sid":"s1","debuggeeId":"…"}}           ← {"ok":true,"postMortem":false}
→ {"m":"step","p":{"sid":"s1","kind":"into"}}                ← {"program":…,"line":…}
→ {"m":"stack","p":{"sid":"s1"}}                             ← {"frames":[…]}
```

vsp's currently-disabled tools then become thin RPC forwarders and can be **re-enabled by removing them from `DefaultDisabledTools()`** (`pkg/config/systems.go:273-286`) once the daemon exists. The Lua scripting bindings (`pkg/scripting/bindings.go:19-95` — record/replay, checkpoints, `forceReplay`) point at `adt.Client` today; they would be repointed at the daemon client, and `saveCheckpoint` (currently a stub returning `"Checkpoint saved - variable capture requires active debug session"`) becomes implementable because a session genuinely persists.

### 5.4 Is this better than fixing the ZADT_VSP WebSocket? — honest assessment

Both approaches need custom ABAP on the server, so the "vanilla ADT" purity argument (`docs/adr/001` warns *"NOT VANILLA ADT COMPATIBLE"*) is a wash — except that an RFC function group is a far smaller, more conventional, more transportable object than an APC application plus SICF node plus stateful handler class.

RFC wins on:
- **Typed interface.** DDIC-typed parameters and typed ABAP exceptions vs. hand-rolled JSON with a regex parser.
- **No CSRF, no ICF, no cookies, no upgrade handshake, no TLS-to-self-signed workarounds** (`CHANGELOG.md:76, 317, 350` are all WS plumbing fixes).
- **Reconnect semantics are explicit** rather than a silent socket drop that destroys `mt_bp_mappings`.
- **The transport already works.** `open-rfc-go` reached live M5/M8 against this exact system.
- **It composes with the rest of the RFC leg** — `vsp rfc call`, `SAP(action="rfc")`, `RFC_READ_TABLE`, and the `ABDBG_*` polling all use one connection story.

WebSocket wins on:
- Genuine bidirectional push — **which the debugger does not use.**
- Already installed on this landscape.

Honest caveat: *neither* transport fixes the design if vsp keeps making each tool call independently. **The daemon is the actual fix; RFC is the better transport for it.** A WebSocket-based daemon would also work — but it would inherit the JSON layer, the APC dependency, and the ICF surface for no compensating benefit.

---

## 6. What could not be tested (and why)

| Not tested | Reason |
|---|---|
| Calling `TPDA_ADT_START_LISTENER` (even with `I_FLG_CHECK = 'X'`, `I_TIMEOUT = 1`) | Attempted as the decisive read-only experiment; **blocked by the local permission classifier**. Not retried, not worked around. This is the single highest-value gap in the evidence. |
| Whether an ABAP roll area actually survives between two `open-rfc-go` calls on one pinned conversation | Requires a Z function module with global state to observe — a write. **This is assumption #1 of the whole design.** |
| `attach_debuggee` from a foreign RFC session against a real debuggee | Requires a live debuggee (a write, and explicitly out of scope) |
| Any step / stack / variable read | Same — needs an attached debuggee |
| Setting or deleting an external breakpoint | Write |
| Post-mortem attach to a short dump | Needs a dump and an attach |
| `RFC_ABAP_INSTALL_AND_RUN` as an evaluation escape hatch | **Does not exist on A4H** (`TFDIR LIKE 'RFC_ABAP%'` → empty) |
| Reading short dumps over RFC | `SNAP` → `TABLE_NOT_AVAILABLE` via `RFC_READ_TABLE`; all `RS_ST22_*` FMs are `FMODE = ''`. Dump *metadata* is reachable indirectly via `ABDBG_ACTIVATION.DUMPID/DUMPDATE/DUMPTIME/DUMPHOST` for post-mortem debuggees only. |
| ICF node inspection for `/sap/bc/adt/debugger` | `ICFSERVICE` / `ICFSERVLOC` return `TABLE_WITHOUT_DATA` over `RFC_READ_TABLE` |

Authorization context: the probing user `CLAUDE` is `USTYP = 'A'` (dialog), `CLASS = SUPER`, with **`SAP_ALL` + `S_A.SYSTEM`**. So *nothing here demonstrates that a least-privilege user can do it.* A real deployment needs `S_DEVELOP` with `ACTVT = 03`/debug and — for `DEBUG_MODE_USER` against another user — the corresponding external-debugging authority. Worth a dedicated authorization trace before promising this to anyone.

---

## 7. Fallback plan, if attach-over-RFC turns out not to hold

If E1/E2 below fail — i.e. if the roll area does not persist, or `attach_debuggee` refuses a non-ADT session — the following still delivers most of the practical value and needs **little or no ABAP**:

1. **Breakpoint management, standalone.** `Z_VSP_DBG_BP_*` needs no attached session (`IF_TPDAPI_STATIC_BP_SERVICES` is explicitly the *static/external* API). Even the listing degrades gracefully to `RFC_READ_TABLE ABDBG_EXTDBPS`. This alone re-enables `SetBreakpoint` / `GetBreakpoints` / `DeleteBreakpoint`.
2. **Debuggee radar.** Poll `ABDBG_ACTIVATION` and report "your breakpoint in `ZFOO` line 42 was hit by user X on server Y at 14:03" — with zero ABAP. Then hand off to real ADT/Eclipse to actually step. Surprisingly useful on its own.
3. **Post-mortem triage.** `ABDBG_ACTIVATION.DUMPID/DUMPDATE/DUMPHOST` + `IF_TPDAPI_SESSION~IS_POST_MORTEM` give a dump-debugging path that vsp does not have at all today.
4. **Work-process introspection** — all RFC-enabled and verified present: `TH_WPINFO`, `TH_WP_DETAIL_INFO`, `TH_WP_DETAIL_INFO64`, `TH_USER_LIST`, `THUSRINFO`, `TH_SERVER_LIST`, `TH_GET_DEBUG_INFO`, `TH_READ_USER_TRACE`, `TH_GET_SYSTEMWIDE_TRC`, `TH_DOWNLOAD_TRACE_FILES`. "Which WP is stuck and in what" answers a large share of the questions people actually reach for a debugger to answer.
5. **Keep the WebSocket for the session part only.** Breakpoints move to RFC (where they are typed and CSRF-free); attach/step stay on ZADT_VSP — but *behind the same daemon*, so the session-mismatch bug is fixed regardless of transport. This is the low-risk hybrid.
6. **Trace-based alternatives.** SAT/SQL trace and checkpoint groups (SAAB) were surveyed; note `TFDIR LIKE '%CHECKPOINT%'` returns only Oracle-internal FMs, so checkpoint-group control is **not** RFC-reachable without a facade.

---

## 8. Concrete next experiments, in order

**E1 — the free one, do it first (read-only, no ABAP, ~10 min).**
Call `TPDA_ADT_START_LISTENER` with `I_FLG_CHECK = 'X'`, `I_IDE_ID`/`I_TERMINAL_ID` = throwaway C32 values, `I_REQUEST_USER = 'CLAUDE'`, `I_TIMEOUT = 1`. Expected: returns `E_FLG_OK` or raises `FAILED`, with no listener registered. Then, in a second shell, run it **without** `I_FLG_CHECK` and `I_TIMEOUT = 10`, and while it blocks, read `ABDBG_LISTENER` from a separate connection.
This answers, with no code and no writes worth the name: *does a blocking listen work over RFC, does it register a `SERVER`/`CONTEXT_ID` row, and does it clean up on timeout?*
*(This is the call that the permission classifier blocked during this study — it needs an explicit go-ahead.)*

**E2 — the assumption the whole design rests on (~30 min, one throwaway Z object).**
Create `Z_VSP_PROBE_STATE` (RFC-enabled) in a scratch package: a function group global counter, incremented and returned each call. From `open-rfc-go` with `PoolConfig{MaxSize: 1}`, call it three times on one `rfc.Client`. If it returns 1, 2, 3 → **roll area persists, the daemon design is sound**. If it returns 1, 1, 1 → the pool is not reusing the ABAP session and `Client.Pin` must be implemented before anything else.

**E3 — end-to-end attach, the real test (needs a debuggee; do on a scratch report).**
Implement `Z_VSP_DBG_{ACTIVATE,LISTEN,ATTACH,STACK,STEP,END}` only — a few hundred lines, transliterating `zcl_vsp_debug_service.clas.abap`. Set an external breakpoint on a harmless `Z` test report via SE80, run it in a second session, and drive listen→attach→stack→step→end from a single pinned Go conversation. Success criterion: **`DebuggerDetach` no longer fails with `cx_tpda_sys_comm_dbgsessionend`** (the exact symptom recorded in `reports/2025-12-05-016-debugger-session-timeout-analysis.md:9-14`).

**E4 — the variable model.**
Only after E3. Walk `IF_TPDAPI_DATA_{SIMPLE,STRUC,TABLE,OBJREF,DATREF}` into a flat `(parentId, id, name, kind, type, value)` table and check it reproduces what `getChildVariables` / `STPDA_ADT_VARIABLE` gave over HTTP. This is the largest single piece of new ABAP and the one place where the RFC path starts from behind rather than ahead.

---

## Appendix — reproducing the probes

```bash
cd /Users/alice/dev/vibing-steampunk && source ~/.zprofile
V=./build/vsp-darwin-arm64

$V rfc info
$V rfc search 'TPDA*'; $V rfc search '*DEBUG*'; $V rfc search '*BREAKPOINT*'   # last → null
$V rfc describe TPDA_ADT_START_LISTENER
$V rfc describe STPDAPI_TEST_ATTACHER
$V rfc describe SYSTEM_DEBUG_ATTACH_TPDA

$V rfc read-table TFDIR --fields FUNCNAME,PNAME,FMODE --where "PNAME = 'SAPLTPDA_ADT_DEBUGGER'"
$V rfc read-table TFDIR --fields FUNCNAME,FMODE      --where "FUNCNAME LIKE 'RS_%BREAKPOINT%'"
$V rfc read-table SEOCOMPO --fields CMPNAME --where "CLSNAME = 'IF_TPDAPI_SERVICE'"
$V rfc read-table SEOCOMPO --fields CMPNAME --where "CLSNAME = 'IF_TPDAPI_CONTROL_SERVICES'"
$V rfc read-table SEOCOMPO --fields CMPNAME --where "CLSNAME = 'IF_TPDAPI_STATIC_BP_SERVICES'"
$V rfc read-table DD02L    --fields TABNAME,TABCLASS --where "TABNAME LIKE 'ABDBG%'"
$V rfc read-table DD03L    --fields FIELDNAME,POSITION,ROLLNAME,KEYFLAG \
                           --where "TABNAME = 'ABDBG_ACTIVATION' AND AS4LOCAL = 'A'"
$V rfc read-table ABDBG_ACTIVATION --fields DEBUGGEE_ID,DEBUGGEE_USER,PRG_CURR,LINE_CURR --top 10
$V rfc call TH_GET_DEBUG_INFO '{}'
```

## Appendix — key source references

| Claim | Evidence |
|---|---|
| Debugger tools disabled by default | `/Users/alice/dev/vibing-steampunk/pkg/config/systems.go:273-286` |
| WebSocket exists for statefulness, not push | `embedded/abap/README.md:16-30`; `reports/2025-12-19-001-websocket-debugging-deep-dive.md:78` |
| No server-initiated events on the WS | `/Users/alice/dev/vibing-steampunk/pkg/adt/websocket.go:22-34`; `pkg/adt/websocket_base.go:219-227` |
| Session-mismatch is the root cause | `/Users/alice/dev/vibing-steampunk/docs/adr/001-websocket-stateful-debugging.md` |
| Debugger HTTP calls go out stateless | `/Users/alice/dev/vibing-steampunk/pkg/adt/config.go:192`; `pkg/adt/http.go:406-409` |
| ADT endpoints being replaced | `/Users/alice/dev/vibing-steampunk/pkg/adt/debugger.go:567-634, 980-1111` |
| ABAP side already calls TPDAPI | `/Users/alice/dev/vibing-steampunk/src/zcl_vsp_debug_service.clas.abap:206, 294-305, 451, 507-520, 826-840` |
| TPDAPI refs held for socket lifetime | `src/zcl_vsp_debug_service.clas.abap:30-44, 173-202` |
| Detach failure symptom | `/Users/alice/dev/vibing-steampunk/reports/2025-12-05-016-debugger-session-timeout-analysis.md:9-14` |
| Breakpoint REST returns 403 CSRF | `/Users/alice/dev/vibing-steampunk/pkg/adt/debugger.go:145-146` |
| RFC callbacks are serviced mid-call | `/Users/alice/dev/open-rfc-go/internal/client/session.go:565`; `/Users/alice/dev/open-rfc-go/rfc/call.go:90`; `rfc/client.go:52-55` |
| RFC client is pool-backed (no pinned session yet) | `/Users/alice/dev/open-rfc-go/rfc/client.go` (`pool.Pool[*lifecycle.Managed]`, `Destination.Pool`) |
| RFC server side is internal / early | `/Users/alice/dev/open-rfc-go/internal/rfcserver/` |

---

## Update — session persistence proven live (2026-08-20)

The prerequisite this document called for (`Z_VSP_PROBE_STATE`, to show that
server-side state survives across RFC calls) turned out to need **no ABAP at all**.
Two library additions and one lock experiment settle it.

### Library: a pinned session

`open-rfc-go` gained `rfc.Client.Pin(ctx) → *rfc.Session`: one connection taken out
of the pool and bound to a session, so consecutive calls reach the same work
process; `Session.Close()` returns it. `Session.Ping()` keeps an idle session alive
(a conversation carries one call at a time, so a session busy in a blocking call
must not be pinged — probe the system through the pool instead).

### Proof: locks, not a Z function group

`ENQUEUE_E_RSADMIN` / `DEQUEUE_E_RSADMIN` are RFC-enabled. Locking an unused key
with `_SCOPE='1'` (the lock belongs to the session) gives a clean session-identity
test:

| # | Where | Action | Result |
|---|---|---|---|
| 1 | pinned session A | enqueue key | OK |
| 2 | pinned session A | enqueue the same key again | **OK** — the session owns it, so A is the same session as in step 1 |
| 3 | another pooled connection | enqueue the same key | **FOREIGN_LOCK** — a different session |
| 4 | pinned session A | dequeue | OK |
| 5 | pool, after A was closed | enqueue | OK — session state was released with the session |
| 6 | pool | dequeue | OK — nothing left behind |

So a pinned RFC connection *is* a stable ABAP session across calls, a pooled call is
not, and closing the session releases its state. That is exactly the roll area the
debugger needs, and it is available today with no ABAP-side deployment.

### Blocking listener, measured

`TPDA_ADT_START_LISTENER` (RFC-enabled) with `I_FLG_CHECK='X'` raises `FAILED`
(nothing waiting). Called for real with `I_TIMEOUT=1` it **blocks the conversation
past a 100-second client timeout** — `I_TIMEOUT` is not seconds, and the listener
owns its connection while it waits. This confirms the daemon shape: the listener
must hold a pinned session of its own while all other traffic goes through the pool,
and `Destination.OperationTimeout` must be raised for that connection.

### What this changes

The `Z_VSP_PROBE_STATE` experiment is no longer needed. The remaining ABAP-side work
is only the `Z_VSP_DBG_*` facade over the TPDA API (attach/step/stack/variables) —
the session mechanics underneath it are proven.

---

## Update 2 — the debugger runs over RFC, proven end to end (2026-08-20)

The verdicts above were "feasible, with a Z facade". The facade turned out to be
unnecessary for the proof: SAP ships an RFC-enabled entry point that drives its own
debugger test harness, and it works from Go today.

### The entry point

`TPDAPI_TEST_DEBUGGER` (`TFDIR-FMODE='R'`, function group `SAPLSTPDAPI_TEST`) is a
**dynamic dispatcher**:

```abap
AUTHORITY-CHECK OBJECT 'S_DEVELOP' ID 'OBJTYPE' FIELD 'DEBUG' ID 'ACTVT' FIELD '03'.
l_ref_main = tcl_main=>s_create( i_terminal_id ).
CALL METHOD l_ref_main->(i_method) RECEIVING r_tab_msg = e_tab_msg.
```

`tcl_main` (local class, include `LSTPDAPI_TESTD00`) declares ~90 methods through the
macro `mac_main_def`, which expands to `test_&1`. So the callable names are
`TEST_PING`, `TEST_ATTACH_BASIC`, `TEST_SIMPLE_STEP`, … — **uppercase**, as a dynamic
`CALL METHOD` requires (lowercase raises "the method … does not exist"). The harness
returns messages only on failure: an empty `E_TAB_MSG` means the scenario passed.

### Measured, live, from the Go client

| Method | Result | Time |
|---|---|---|
| `TEST_PING` | OK (no messages) | <1s |
| `TEST_DEBUGGEE_EXISTS` | OK | <1s |
| `TEST_STATIC_BREAKPOINTS` | OK — sets and removes external breakpoints, self-cleaning (`ABDBG_EXTDBPS` empty afterwards) | 1s |
| `TEST_ATTACH_BASIC` | **OK — spawns a debuggee, attaches, detaches** | 4s |
| `TEST_SIMPLE_STEP` | OK — stepping | 4s |
| `TEST_LINE_BREAKPOINT` | OK | 3s |
| `TEST_RUN_TO_LINE` | OK | 4s |
| `TEST_SIMPLE_DATA` | OK — reads variables | 4s |
| `TEST_GET_LOCALS` | OK — reads locals | 4s |
| `TEST_RFC` | OK — the RFC-debugging scenario | <1s |

Authorization was satisfied by the probing user (`S_DEVELOP` with `OBJTYPE=DEBUG`,
`ACTVT=03`) — a least-privilege check is still outstanding.

### What this changes

- **Attach, step, breakpoints, stack and variable reads all execute over classic
  RFC**, with no ZADT_VSP, no WebSocket and no deployed Z code. The earlier
  session-mismatch problem does not appear, because each scenario runs inside one
  RFC-served ABAP session.
- The harness is a *test* harness: each method is a fixed scenario, not a general
  API. It is therefore the **reference implementation** for a `Z_VSP_DBG_*` facade —
  and simultaneously a live conformance suite proving the underlying TPDA API is
  reachable from an RFC session.
- The remaining work for an interactive debugger is a facade that exposes the same
  TPDA calls as parameterised operations (attach to *this* debuggee, step *now*, read
  *these* variables), driven from a pinned `rfc.Session` — the daemon shape.

### Client changes this required

Reaching the dispatcher exposed two real defects in the Go client, both fixed:
`RFC_FIELDS POSITION` is not a dense 1..n sequence (structures with `.INCLUDE`), and
parameters with nested `STRU`/`TTYP` components could not be modelled at all — the
recursive metadata path (`RFC_METADATA_GET` → `metadata.Graph` → recursive xRFC codec)
is now wired, which is what makes `E_TAB_MSG` decodable. The CLI also gained
`--timeout` for calls that block server-side.
