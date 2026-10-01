package adt

import (
	"context"
	"fmt"
	"strings"
)

// Adding an entry to a request and taking one out is SE09's object list
// editing, and ADT has neither: its organizer adds an object only as a side
// effect of editing it. That leaves the entries nobody edits -- a LIMU REPT
// for a report's texts, a TABU with the keys of the customizing rows it
// carries, an object that was saved into the wrong request -- to the GUI.
// The function modules behind SE09 are not remote-enabled, so these go through
// ZADT_VSP's CALL FUNCTION bridge, like MoveTransportObject.

// organizerBridge is the part of ZADT_VSP's WebSocket client these need.
type organizerBridge interface {
	CallRFC(ctx context.Context, function string, params map[string]any) (*RFCResult, error)
}

// TransportEntry is one entry to add: an E071 object and, for a TABU, the
// E071K table keys it carries -- one TABKEY per row, the key fields laid end
// to end at their full length, as SE09 shows them, '*' as a generic tail.
type TransportEntry struct {
	TransportObjectKey
	Keys []string `json:"keys,omitempty"`
}

// TransportEntriesResult is what adding entries did.
type TransportEntriesResult struct {
	Request string `json:"request"`
	Task    string `json:"task"` // where the entries went
	// Classified is set when the task was unclassified and had to become a
	// development/correction task (S) before it could take objects.
	Classified string               `json:"classified,omitempty"`
	Added      []TransportObjectKey `json:"added,omitempty"`
	Keys       int                  `json:"keys,omitempty"`
	Message    string               `json:"message,omitempty"`
}

// AddTransportObjects adds entries to a request: to the caller's modifiable
// task in it, to the request itself when there is none, or to the task when a
// task is named. TR_APPEND_TO_COMM_OBJS_KEYS checks each entry as SE09 does --
// the object must exist, not be local, not be locked in another request -- and
// refuses the whole list if one fails.
func (c *Client) AddTransportObjects(ctx context.Context, ws *DebugWebSocketClient, request string, entries []TransportEntry) (*TransportEntriesResult, error) {
	request = strings.ToUpper(strings.TrimSpace(request))
	if request == "" {
		return nil, fmt.Errorf("the request to add to is required")
	}
	if err := c.config.Safety.CheckTransport(request, "AddTransportObjects", true); err != nil {
		return nil, err
	}
	if ws == nil || !ws.IsConnected() {
		return nil, fmt.Errorf("adding entries to a request needs ZADT_VSP's function bridge (a WebSocket to the system)")
	}
	details, err := c.transportTree(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", request, err)
	}
	return addTransportObjects(ctx, ws, c.requestHeader, details, request, strings.ToUpper(c.config.Username), entries)
}

// headerReader reads a request's or task's E070 row as a TRWBO_REQUEST_HEADER.
type headerReader func(ctx context.Context, number string) (map[string]any, error)

// requestHeader reads E070 for one request or task. ZADT_VSP's bridge returns
// no CHANGING parameters, so TRINT_READ_REQUEST_HEADER cannot hand one back.
func (c *Client) requestHeader(ctx context.Context, number string) (map[string]any, error) {
	fields := []string{"TRKORR", "TRFUNCTION", "TRSTATUS", "TARSYSTEM", "KORRDEV", "AS4USER", "AS4DATE", "AS4TIME", "STRKORR"}
	sql := fmt.Sprintf("SELECT %s FROM E070 WHERE TRKORR = '%s'", strings.Join(fields, ", "), strings.ReplaceAll(number, "'", "''"))
	res, err := c.GetTableContents(ctx, "E070", 1, sql)
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Rows) == 0 {
		return nil, fmt.Errorf("%s is not in E070", number)
	}
	header := map[string]any{}
	for _, f := range fields {
		header[f] = strings.TrimSpace(fmt.Sprint(res.Rows[0][f]))
	}
	// The CTS checks refuse a header whose client part was never read
	// (TK 514 "internal error when calling a CTS interface"): CLIENTS_FILLED
	// says E070C was looked at, whether or not it has a row.
	sql = fmt.Sprintf("SELECT CLIENT FROM E070C WHERE TRKORR = '%s'", strings.ReplaceAll(number, "'", "''"))
	if cres, err := c.GetTableContents(ctx, "E070C", 1, sql); err == nil && cres != nil && len(cres.Rows) > 0 {
		header["CLIENT"] = strings.TrimSpace(fmt.Sprint(cres.Rows[0]["CLIENT"]))
	}
	header["CLIENTS_FILLED"] = "X"
	return header, nil
}

func addTransportObjects(ctx context.Context, bridge organizerBridge, readHeader headerReader, details *TransportDetails, named, user string, entries []TransportEntry) (*TransportEntriesResult, error) {
	e071, e071k, err := entryRows(entries)
	if err != nil {
		return nil, err
	}
	out := &TransportEntriesResult{Request: details.Number, Task: targetTask(details, named, user)}
	classified, err := classifyTask(ctx, bridge, readHeader, details, out.Task)
	if err != nil {
		return out, err
	}
	out.Classified = classified
	params := map[string]any{
		"WI_TRKORR": out.Task,
		"WT_E071":   e071,
		"IV_DIALOG": "",
	}
	if len(e071k) > 0 {
		params["WT_E071K"] = e071k
	}
	res, err := bridge.CallRFC(ctx, "TR_APPEND_TO_COMM_OBJS_KEYS", params)
	if err != nil {
		return out, fmt.Errorf("TR_APPEND_TO_COMM_OBJS_KEYS: %w", err)
	}
	out.Message = res.Message
	if res.Subrc != 0 {
		// ZADT_VSP binds every exception to OTHERS, so the number says nothing;
		// the message the function left behind is what names the problem.
		return out, fmt.Errorf("adding to %s: TR_APPEND_TO_COMM_OBJS_KEYS failed (sy-subrc %d)%s", out.Task, res.Subrc, withMessage(res.Message))
	}
	for _, e := range entries {
		out.Added = append(out.Added, normalizedKey(e.TransportObjectKey))
	}
	out.Keys = len(e071k)
	return out, nil
}

// classifyTask makes an unclassified task a development/correction task (S)
// and says so; any other task, or the request itself, is left alone. A task
// the ADT organizer creates is unclassified, and an unclassified task refuses
// objects ("Changes to objects are only allowed in correction/repair"). SE09
// classifies it the moment an object goes in; this does the same first.
func classifyTask(ctx context.Context, bridge organizerBridge, readHeader headerReader, details *TransportDetails, task string) (string, error) {
	if !unclassified(details, task) {
		return "", nil
	}
	// TR_CHANGE_TRFUNCTION checks the header it is given, not the database:
	// a header with only the number reads as "not a workbench task".
	header, err := readHeader(ctx, task)
	if err != nil {
		return "", fmt.Errorf("reading the header of %s: %w", task, err)
	}
	res, err := bridge.CallRFC(ctx, "TR_CHANGE_TRFUNCTION", map[string]any{
		"CS_REQUEST_HEADER": header,
		"IV_NEW_TRFUNCTION": "S",
	})
	if err != nil {
		return "", fmt.Errorf("classifying %s: TR_CHANGE_TRFUNCTION: %w", task, err)
	}
	if res.Subrc != 0 {
		return "", fmt.Errorf("classifying %s as a development/correction task: sy-subrc %d%s", task, res.Subrc, withMessage(res.Message))
	}
	return "S", nil
}

// unclassified reports whether number is a task of the request that has no
// type yet. ADT spells the type out; E070 says X.
func unclassified(details *TransportDetails, number string) bool {
	for _, t := range details.Tasks {
		if t.Number == number {
			return strings.EqualFold(t.Type, "Unclassified") || strings.EqualFold(t.Type, "X")
		}
	}
	return false
}

func normalizedKey(k TransportObjectKey) TransportObjectKey {
	return TransportObjectKey{
		PgmID:  strings.ToUpper(strings.TrimSpace(k.PgmID)),
		Object: strings.ToUpper(strings.TrimSpace(k.Object)),
		Name:   strings.ToUpper(strings.TrimSpace(k.Name)),
	}
}

// entryRows turns entries into E071 and E071K rows. Keys belong to a TABU
// only; anything else with keys is refused rather than sent half-understood.
func entryRows(entries []TransportEntry) ([]any, []any, error) {
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("at least one object to add is required")
	}
	var e071, e071k []any
	for _, e := range entries {
		k := normalizedKey(e.TransportObjectKey)
		if k.PgmID == "" || k.Object == "" || k.Name == "" {
			return nil, nil, fmt.Errorf("entry %q: PGMID, object type and name are required", k.String())
		}
		row := map[string]any{"PGMID": k.PgmID, "OBJECT": k.Object, "OBJ_NAME": k.Name}
		if len(e.Keys) > 0 {
			if k.PgmID != "R3TR" || k.Object != "TABU" {
				return nil, nil, fmt.Errorf("entry %s: table keys belong to an R3TR TABU entry only", k)
			}
			row["OBJFUNC"] = "K" // the entry stands for the listed keys, not the whole table
			for _, key := range e.Keys {
				if strings.TrimSpace(key) == "" {
					return nil, nil, fmt.Errorf("entry %s: an empty table key", k)
				}
				e071k = append(e071k, map[string]any{
					"PGMID": "R3TR", "OBJECT": "TABU", "OBJNAME": k.Name,
					"MASTERTYPE": "TABU", "MASTERNAME": k.Name, "TABKEY": key,
				})
			}
		}
		e071 = append(e071, row)
	}
	return e071, e071k, nil
}

// TransportRemoveResult is what taking an entry out did.
type TransportRemoveResult struct {
	Object  TransportObjectKey `json:"object"`
	Request string             `json:"request"`
	Task    string             `json:"task"` // the task (or request) the entry was in
	Removed bool               `json:"removed"`
	Message string             `json:"message,omitempty"`
}

// RemoveTransportObject takes one entry out of a request, from whichever of
// its tasks holds it, together with the table keys it carries. An entry that
// is not in the request is refused before anything is written.
func (c *Client) RemoveTransportObject(ctx context.Context, ws *DebugWebSocketClient, request string, key TransportObjectKey) (*TransportRemoveResult, error) {
	request = strings.ToUpper(strings.TrimSpace(request))
	if request == "" {
		return nil, fmt.Errorf("the request to remove from is required")
	}
	if err := c.config.Safety.CheckTransport(request, "RemoveTransportObject", true); err != nil {
		return nil, err
	}
	if ws == nil || !ws.IsConnected() {
		return nil, fmt.Errorf("removing an entry from a request needs ZADT_VSP's function bridge (a WebSocket to the system)")
	}
	details, err := c.transportTree(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", request, err)
	}
	return removeTransportObject(ctx, ws, details, request, key)
}

func removeTransportObject(ctx context.Context, bridge organizerBridge, details *TransportDetails, named string, key TransportObjectKey) (*TransportRemoveResult, error) {
	key = normalizedKey(key)
	out := &TransportRemoveResult{Object: key, Request: details.Number, Task: holderIn(details, named, key)}
	if out.Task == "" {
		where := details.Number
		if t := namedTask(details, named); t != "" {
			where = t
		}
		return out, fmt.Errorf("%s is not in %s", key, where)
	}
	del := func() (*RFCResult, error) {
		return bridge.CallRFC(ctx, "TRINT_DELETE_COMM_OBJECT_KEYS", map[string]any{
			"CS_REQUEST":     map[string]any{"H": map[string]any{"TRKORR": out.Task}},
			"IS_E071_DELETE": map[string]any{"PGMID": key.PgmID, "OBJECT": key.Object, "OBJ_NAME": key.Name},
			"IV_DIALOG_FLAG": "",
		})
	}
	res, err := del()
	if err != nil {
		return out, fmt.Errorf("TRINT_DELETE_COMM_OBJECT_KEYS: %w", err)
	}
	// An entry recorded twice is refused with "Object entry exists more than
	// once; sort and compress first". That is what SE09's Sort and Compress
	// is for; do it and retry.
	if recordedTwice(res) {
		if _, cerr := bridge.CallRFC(ctx, "TR_SORT_AND_COMPRESS_COMM", map[string]any{"IV_TRKORR": out.Task}); cerr == nil {
			if res, err = del(); err != nil {
				return out, fmt.Errorf("TRINT_DELETE_COMM_OBJECT_KEYS after sort and compress: %w", err)
			}
		}
	}
	// Deleting a DDIC object records it a second time, as a deletion
	// (OBJFUNC D), and Sort and Compress keeps both rows.
	if recordedTwice(res) {
		if res, err = removeRecordedTwice(ctx, bridge, out.Task, key, del); err != nil {
			return out, err
		}
	}
	out.Message = res.Message
	if res.Subrc != 0 {
		return out, fmt.Errorf("removing %s from %s: sy-subrc %d%s", key, out.Task, res.Subrc, withMessage(res.Message))
	}
	out.Removed = true
	return out, nil
}

func recordedTwice(res *RFCResult) bool {
	return res.Subrc != 0 && strings.Contains(strings.ToLower(res.Message), "more than once")
}

// removeRecordedTwice takes out an entry whose task holds it twice -- as the
// object and as its deletion -- which TRINT_DELETE_COMM_OBJECT_KEYS refuses
// because it counts the entry in the object list it is handed. Handed a list
// with the entry once, it deletes every row of it from E071 and E071K, as its
// DELETE does not look at OBJFUNC.
//
// That row goes without a lock flag. With one, the function first moves the
// object's lock onto the other row, which is about to be deleted too, and
// through ZADT_VSP's bridge that call never answered. The lock the task still
// holds is then given back the way it is for any entry: the object is
// appended to the task again, where it finds its own lock, and taken out.
func removeRecordedTwice(ctx context.Context, bridge organizerBridge, task string, key TransportObjectKey, del func() (*RFCResult, error)) (*RFCResult, error) {
	res, err := bridge.CallRFC(ctx, "TRINT_DELETE_COMM_OBJECT_KEYS", map[string]any{
		"CS_REQUEST": map[string]any{
			"H":              map[string]any{"TRKORR": task},
			"OBJECTS_FILLED": "X",
			"OBJECTS": []map[string]any{{
				"TRKORR": task, "PGMID": key.PgmID, "OBJECT": key.Object, "OBJ_NAME": key.Name,
			}},
		},
		"IS_E071_DELETE": map[string]any{"TRKORR": task, "PGMID": key.PgmID, "OBJECT": key.Object, "OBJ_NAME": key.Name},
		"IV_DIALOG_FLAG": "",
	})
	if err != nil {
		return nil, fmt.Errorf("TRINT_DELETE_COMM_OBJECT_KEYS for an entry recorded twice: %w", err)
	}
	if res.Subrc != 0 {
		return res, nil
	}
	app, err := bridge.CallRFC(ctx, "TR_APPEND_TO_COMM_OBJS_KEYS", map[string]any{
		"WI_TRKORR": task,
		"WT_E071":   []map[string]any{{"PGMID": key.PgmID, "OBJECT": key.Object, "OBJ_NAME": key.Name}},
		"IV_DIALOG": "",
	})
	if err != nil || app.Subrc != 0 {
		msg := ""
		if err != nil {
			msg = err.Error()
		} else {
			msg = app.Message
		}
		return res, fmt.Errorf("both entries of %s are out of %s, but the task may still hold its lock: appending it again to give the lock back failed%s", key, task, withMessage(msg))
	}
	return del()
}

// transportTree reads a request with its tasks and their entries. Asked for a
// task, ADT answers with the header of the request the task belongs to and
// neither tasks nor entries -- so a task number, which SE09 accepts anywhere
// a request goes, found nothing. The request is then read under its own number.
func (c *Client) transportTree(ctx context.Context, number string) (*TransportDetails, error) {
	details, err := c.GetTransport(ctx, number)
	if err != nil {
		return nil, err
	}
	if details.Number != "" && !strings.EqualFold(details.Number, strings.TrimSpace(number)) {
		return c.GetTransport(ctx, details.Number)
	}
	return details, nil
}
