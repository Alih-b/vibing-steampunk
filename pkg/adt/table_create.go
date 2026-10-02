package adt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// --- DDIC Table/Structure Operations ---

// CreateTableOptions defines options for creating a DDIC table.
type CreateTableOptions struct {
	Name          string       `json:"name"`                    // Table name (uppercase, max 30 chars, must start with Z/Y)
	Description   string       `json:"description"`             // Short description
	Package       string       `json:"package"`                 // Target package
	Fields        []TableField `json:"fields"`                  // Field definitions
	Transport     string       `json:"transport,omitempty"`     // Transport request (optional for $TMP)
	DeliveryClass string       `json:"deliveryClass,omitempty"` // A=Application, C=Customizing, L=Temp, etc. (default: A)
	TableCategory string       `json:"tableCategory,omitempty"` // TRANSPARENT (default), STRUCTURE, etc.
	// ClientDependent decides the client field (issue #254). nil, the default,
	// keeps the old behaviour for a field list without a client field: a
	// `key client : abap.clnt` is put in front. When the list already starts
	// with a client-typed key field (MANDT, CLIENT, CLNT, abap.clnt), that field
	// is the client field and none is added. true asks for a client-dependent
	// table explicitly (same rules), and also makes a first key field *named*
	// MANDT or CLIENT the client field whatever its type; without it, such a
	// field is refused, since vsp cannot tell whether it is the client field.
	// false asks for a client-independent one: nothing is added, and a
	// client-typed first key field is refused as contradictory. See
	// tableClientPlan for the full rules.
	ClientDependent *bool `json:"clientDependent,omitempty"`

	// FieldsJSON and ClientDependentArg carry a caller's raw arguments (the
	// MCP handler's "fields" and "client_dependent"). CreateTable parses them
	// after its mutation gate, so a read-only or package refusal is what a
	// blocked caller hears, not a complaint about the spec. FieldsJSON
	// replaces Fields, ClientDependentArg replaces ClientDependent.
	FieldsJSON         string  `json:"-"`
	ClientDependentArg *string `json:"-"`
}

// ResolveCreateTableSpec parses FieldsJSON and ClientDependentArg into Fields
// and ClientDependent, and checks the field list against the client rules.
// CreateTable calls it after the mutation gate; it talks to nobody.
func ResolveCreateTableSpec(opts CreateTableOptions) (CreateTableOptions, error) {
	if opts.FieldsJSON != "" {
		if len(opts.Fields) > 0 {
			return opts, fmt.Errorf("give the fields either as Fields or as FieldsJSON, not both")
		}
		fields, err := ParseTableFields(opts.FieldsJSON)
		if err != nil {
			return opts, fmt.Errorf("invalid fields: %w", err)
		}
		opts.Fields = fields
		opts.FieldsJSON = ""
	}
	if opts.ClientDependentArg != nil {
		b, err := strconv.ParseBool(strings.TrimSpace(*opts.ClientDependentArg))
		if err != nil {
			return opts, fmt.Errorf("client_dependent must be true or false, got %q", *opts.ClientDependentArg)
		}
		opts.ClientDependent = &b
		opts.ClientDependentArg = nil
	}
	if len(opts.Fields) == 0 {
		return opts, fmt.Errorf("at least one field is required")
	}
	if _, _, err := tableClientPlan(opts); err != nil {
		return opts, err
	}
	return opts, nil
}

// CreateTable creates a new DDIC transparent table from JSON-like options.
// This is a high-level tool that handles the full workflow: create → set source → activate.
func (c *Client) CreateTable(ctx context.Context, opts CreateTableOptions) error {
	// Validate input first: the package default below is part of what the gate
	// has to see.
	opts.Name = strings.ToUpper(opts.Name)
	if opts.Name == "" || len(opts.Name) > 30 {
		return fmt.Errorf("table name must be 1-30 characters")
	}
	if opts.Package == "" {
		opts.Package = "$TMP"
	}
	if opts.DeliveryClass == "" {
		opts.DeliveryClass = "A"
	}
	if opts.TableCategory == "" {
		opts.TableCategory = "TRANSPARENT"
	}

	// Full mutation gate, not just the op-type half. CreateTable used to run
	// checkSafety alone, so it created tables in any package the user could
	// reach — SAP_ALLOWED_PACKAGES did not apply to it at all, and the source
	// PUT below carried no gate either.
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpCreate,
		OpName:    "CreateTable",
		Package:   opts.Package,
		Transport: opts.Transport,
	}); err != nil {
		return err
	}

	// The spec is checked only now, behind the gate: a caller the gate turns
	// away hears the gate's reason, not a complaint about a typo in a request
	// it could not have made anyway. Nothing has reached SAP yet.
	opts, err := ResolveCreateTableSpec(opts)
	if err != nil {
		return err
	}
	ddlSource, err := generateTableDDL(opts)
	if err != nil {
		return err
	}

	// Step 1: Create table object
	createBody := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<blue:blueSource xmlns:blue="http://www.sap.com/wbobj/blue"
                 xmlns:adtcore="http://www.sap.com/adt/core"
                 adtcore:name="%s"
                 adtcore:type="TABL/DT"
                 adtcore:description="%s">
  <adtcore:packageRef adtcore:name="%s"/>
</blue:blueSource>`, opts.Name, escapeXML(opts.Description), opts.Package)

	params := url.Values{}
	if opts.Transport != "" {
		params.Set("corrNr", opts.Transport)
	}

	_, err = c.transport.Request(ctx, "/sap/bc/adt/ddic/tables", &RequestOptions{
		Method:      http.MethodPost,
		Query:       params,
		Body:        []byte(createBody),
		ContentType: "application/vnd.sap.adt.tables.v2+xml",
		Accept:      "application/vnd.sap.adt.tables.v2+xml",
	})
	if err != nil {
		return fmt.Errorf("creating table object: %w", err)
	}

	// Step 2: Lock, update source, unlock
	tableURL := fmt.Sprintf("/sap/bc/adt/ddic/tables/%s", strings.ToLower(opts.Name))
	sourceURL := tableURL + "/source/main"

	// The table was just created in opts.Package, which the gate above
	// accepted, so UpdateSource does not have to resolve it again from inside
	// the lock (issue #91).
	ctx = withMutationPackageChecked(ctx, tableURL)

	lock, err := c.LockObject(ctx, tableURL, "MODIFY", opts.Transport)
	if err != nil {
		return fmt.Errorf("locking table: %w", err)
	}

	// This used to be a hand-rolled transport.Request with no Stateful field,
	// which meant the PUT that consumes the lock handle went out explicitly
	// stateless — it retired the very session the handle was issued in, and
	// creating a table failed with 423 InvalidLockHandle on every attempt, on
	// any configuration. UpdateSource is the same request with Stateful: true
	// and the mutation gate attached.
	if err := c.UpdateSource(ctx, sourceURL, ddlSource, lock.LockHandle, opts.Transport); err != nil {
		if unlockErr := c.releaseLockAfterFailure(ctx, tableURL, lock.LockHandle); unlockErr != nil {
			return fmt.Errorf("updating table source: %w — %s", err, strandedLockAdvice(tableURL, unlockErr))
		}
		return fmt.Errorf("updating table source: %w", err)
	}

	// Unlock BEFORE activation
	if err := c.UnlockObject(ctx, tableURL, lock.LockHandle); err != nil {
		return fmt.Errorf("unlocking table before activation: %w — %s", err, strandedLockAdvice(tableURL, err))
	}

	// Step 3: Activate
	activation, err := c.Activate(ctx, tableURL, opts.Name)
	if err != nil {
		return fmt.Errorf("activating table: %w", err)
	}
	// The refusal is a 200 with the reason in the body, and a table that did not
	// activate does not exist as far as anything that reads it is concerned —
	// returning nil here promised a table that was never there.
	if !activation.Success {
		return fmt.Errorf("table %s was created but did not activate: %s", opts.Name, strings.Join(activation.ProblemLines(), "; "))
	}

	return nil
}

// isClientFieldType reports whether a field spec types the field as an SAP
// client (data element MANDT, built-in CLNT).
func isClientFieldType(f TableField) bool {
	switch strings.ToUpper(strings.TrimSpace(f.Type)) {
	case "MANDT", "CLIENT", "CLNT", "ABAP.CLNT":
		return true
	}
	return false
}

// isClientFieldName reports whether a field is named like a client field.
func isClientFieldName(name string) bool {
	n := strings.ToUpper(strings.TrimSpace(name))
	return n == "MANDT" || n == "CLIENT"
}

// tableClientPlan decides the client field for CreateTable (issue #254). It
// used to put a `key client` in front of every field list, so one that
// already began with MANDT came out with two client key fields.
//
// It returns add=true when vsp puts its own `key client : abap.clnt` in front,
// and own=0 when the caller's first field is the client field (own=-1
// otherwise). The rules, fail-closed wherever a second client field could
// slip in:
//
//   - client_dependent false: nothing is added; a client-typed first key
//     field contradicts it and is refused.
//   - A client-typed (MANDT, CLIENT, CLNT, abap.clnt) first key field is the
//     client field.
//   - A first key field named MANDT or CLIENT whose type is some other data
//     element (SYMANDT, ZMANDT) may or may not be a client field; vsp cannot
//     tell. It is refused unless client_dependent is given: true makes it the
//     client field as is, false is refused too (SAP would see a client field
//     there if the data element is one, so the table may well not be
//     client-independent).
//   - A first key field named MANDT or CLIENT with a built-in type (CHAR3,
//     NUMC3, INT4, STRING, abap.char(3)) is never a client field to SAP: the
//     table is client-independent. client_dependent false accepts it as a
//     plain field; left out or true, it is refused, since the name says
//     client field and the table would not be client-dependent.
//   - Otherwise (nil or true) CLIENT is added in front.
//   - A later client-typed key field is refused: only the first key field can
//     be the client field.
//   - When CLIENT is added: a field named CLIENT collides and is refused, and
//     so is a non-key client-typed field named MANDT (a missing "key": true,
//     most likely). Other non-key client-typed columns (SRC_CLIENT) are plain
//     data columns and are allowed.
func tableClientPlan(opts CreateTableOptions) (add bool, own int, err error) {
	own = -1
	if len(opts.Fields) == 0 {
		return false, own, fmt.Errorf("at least one field is required")
	}
	first := opts.Fields[0]
	firstTyped := first.IsKey && isClientFieldType(first)
	firstNamed := first.IsKey && !isClientFieldType(first) && isClientFieldName(first.Name)

	// A built-in type is never a client field to SAP; a data element might be.
	firstBuiltin := firstNamed && strings.HasPrefix(mapFieldType(first), "abap.")
	firstName := strings.ToUpper(strings.TrimSpace(first.Name))

	if opts.ClientDependent != nil && !*opts.ClientDependent {
		if firstTyped {
			return false, own, fmt.Errorf("client_dependent is false, but the first key field %s has client type %s, which makes the table client-dependent; drop the field or leave client_dependent out",
				firstName, first.Type)
		}
		if firstNamed && !firstBuiltin {
			return false, own, fmt.Errorf("client_dependent is false, but %s looks like a client field; rename it or pass client_dependent:true (it is the first key field, and if its type %s is a client data element, SAP makes the table client-dependent)",
				firstName, first.Type)
		}
		return false, own, nil
	}

	if firstBuiltin {
		return false, own, fmt.Errorf("field 1 is a key field named %s with built-in type %s, which SAP never treats as a client field, so the table would not be client-dependent; is %s your client field? give it type MANDT if so, or rename it, or pass client_dependent:false for a client-independent table",
			firstName, first.Type, firstName)
	}
	if firstNamed && opts.ClientDependent == nil {
		return false, own, fmt.Errorf("field 1 is a key field named %s with type %s, which vsp does not know as a client type; is %s your client field? pass client_dependent:true if so (and the field will be used as is), or rename it",
			firstName, first.Type, firstName)
	}
	if firstTyped || firstNamed {
		own = 0
	}

	for i, f := range opts.Fields {
		if i == own {
			continue
		}
		if i > 0 && f.IsKey && isClientFieldType(f) {
			return false, own, fmt.Errorf("field %d (%s) is a key field of client type %s, but only the first key field can be the client field; put your own client key field first, once, or set client_dependent to false",
				i+1, strings.ToUpper(f.Name), f.Type)
		}
		if own >= 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(f.Name), "CLIENT") {
			return false, own, fmt.Errorf("field %d is named CLIENT, which collides with the client key field vsp adds; make it the first key field with type MANDT, rename it, or set client_dependent to false",
				i+1)
		}
		if !f.IsKey && isClientFieldType(f) && strings.EqualFold(strings.TrimSpace(f.Name), "MANDT") {
			return false, own, fmt.Errorf("field %d (MANDT) has client type %s but no \"key\": true, so vsp would add a client key field of its own; to make it the client field, mark it key and put it first, or rename it",
				i+1, f.Type)
		}
	}
	return own < 0, own, nil
}

// TableClientField says which field CreateTable makes the client field for
// these options: "CLIENT" with added=true when it puts one in front, the
// caller's first field when that is the client field, "" for a
// client-independent table. The error is the one CreateTable would refuse the
// options with. Give it resolved options (ResolveCreateTableSpec).
func TableClientField(opts CreateTableOptions) (name string, added bool, err error) {
	add, own, err := tableClientPlan(opts)
	switch {
	case err != nil:
		return "", false, err
	case add:
		return "CLIENT", true, nil
	case own >= 0:
		return strings.ToUpper(opts.Fields[own].Name), false, nil
	}
	return "", false, nil
}

// generateTableDDL converts CreateTableOptions to CDS-style DDL source.
func generateTableDDL(opts CreateTableOptions) (string, error) {
	addClient, _, err := tableClientPlan(opts)
	if err != nil {
		return "", err
	}

	var sb strings.Builder

	// Annotations - must match SAP's expected format
	sb.WriteString(fmt.Sprintf("@EndUserText.label : '%s'\n", escapeQuote(opts.Description)))
	sb.WriteString("@AbapCatalog.enhancement.category : #NOT_EXTENSIBLE\n")
	sb.WriteString(fmt.Sprintf("@AbapCatalog.tableCategory : #%s\n", opts.TableCategory))
	sb.WriteString(fmt.Sprintf("@AbapCatalog.deliveryClass : #%s\n", opts.DeliveryClass))
	sb.WriteString("@AbapCatalog.dataMaintenance : #ALLOWED\n")
	sb.WriteString(fmt.Sprintf("define table %s {\n\n", strings.ToLower(opts.Name)))

	// The client key field, only when the caller's list has none of its own
	// and did not ask for a client-independent table.
	if addClient {
		sb.WriteString("  key client : abap.clnt not null;\n")
	}

	// User-defined fields
	for _, f := range opts.Fields {
		fieldName := strings.ToLower(f.Name)
		fieldType := mapFieldType(f)

		if f.IsKey {
			sb.WriteString(fmt.Sprintf("  key %s : %s not null;\n", fieldName, fieldType))
		} else if f.NotNull {
			sb.WriteString(fmt.Sprintf("  %s : %s not null;\n", fieldName, fieldType))
		} else {
			sb.WriteString(fmt.Sprintf("  %s : %s;\n", fieldName, fieldType))
		}
	}

	sb.WriteString("\n}\n")
	return sb.String(), nil
}

// mapFieldType converts a simple type spec to ABAP DDL type.
func mapFieldType(f TableField) string {
	t := strings.ToUpper(strings.TrimSpace(f.Type))

	// Handle built-in types with length
	switch t {
	case "CHAR":
		if f.Length > 0 {
			return fmt.Sprintf("abap.char(%d)", f.Length)
		}
		return "abap.char(1)"
	case "NUMC":
		if f.Length > 0 {
			return fmt.Sprintf("abap.numc(%d)", f.Length)
		}
		return "abap.numc(10)"
	case "RAW":
		if f.Length > 0 {
			return fmt.Sprintf("abap.raw(%d)", f.Length)
		}
		return "abap.raw(16)"
	case "DEC", "CURR", "QUAN":
		l := f.Length
		if l == 0 {
			l = 15
		}
		d := f.Decimals
		return fmt.Sprintf("abap.dec(%d,%d)", l, d)
	case "INT1":
		return "abap.int1"
	case "INT2":
		return "abap.int2"
	case "INT4":
		return "abap.int4"
	case "INT8":
		return "abap.int8"
	case "FLTP":
		return "abap.fltp"
	case "STRING":
		return "abap.string(0)"
	case "RAWSTRING":
		return "abap.rawstring(0)"
	case "DATS", "DATE":
		return "abap.dats"
	case "TIMS", "TIME":
		return "abap.tims"
	case "TIMESTAMPL":
		return "timestampl"
	case "UTCLONG":
		return "abap.utclong"
	case "SYSUUID_X16", "UUID":
		return "sysuuid_x16"
	case "MANDT", "CLIENT":
		return "mandt"
	case "CLNT":
		return "abap.clnt"
	}

	// Check for CHARnn, NUMCnn shorthand (e.g., CHAR32, NUMC10)
	if strings.HasPrefix(t, "CHAR") && len(t) > 4 {
		return fmt.Sprintf("abap.char(%s)", t[4:])
	}
	if strings.HasPrefix(t, "NUMC") && len(t) > 4 {
		return fmt.Sprintf("abap.numc(%s)", t[4:])
	}

	// Assume it's a data element name
	return strings.ToLower(t)
}

func escapeQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// tableFieldJSONKeys are the attributes a CreateTable field spec takes, as
// spelled in JSON. ParseTableFields refuses every other key.
var tableFieldJSONKeys = []string{"name", "type", "length", "decimals", "description", "key", "notNull"}

// ParseTableFields decodes a CreateTable field list from JSON. An attribute
// it does not know is an error, not a silently different table (issue #254:
// "not_null" was dropped by json.Unmarshal and the field came out nullable).
func ParseTableFields(fieldsJSON string) ([]TableField, error) {
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fieldsJSON), &raw); err != nil {
		return nil, fmt.Errorf("fields must be a JSON array of objects: %w", err)
	}

	// Attribute names match case-insensitively, as encoding/json always
	// matched them ("Key", "NotNull" keep working); only names that are not
	// attributes at all are refused.
	known := make(map[string]string, len(tableFieldJSONKeys))
	for _, k := range tableFieldJSONKeys {
		known[strings.ToLower(k)] = k
	}

	fields := make([]TableField, 0, len(raw))
	for i, obj := range raw {
		var unknown []string
		norm := make(map[string]json.RawMessage, len(obj))
		given := make(map[string]string, len(obj))
		for k, v := range obj {
			canon, ok := known[strings.ToLower(k)]
			if !ok {
				unknown = append(unknown, k)
				continue
			}
			if prev, dup := given[canon]; dup {
				return nil, fmt.Errorf("field %d: attribute %s is given twice (%q and %q)", i+1, canon, prev, k)
			}
			given[canon] = k
			norm[canon] = v
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			label := fmt.Sprintf("field %d", i+1)
			var name string
			if json.Unmarshal(norm["name"], &name) == nil && name != "" {
				label += " (" + strings.ToUpper(name) + ")"
			}
			parts := make([]string, len(unknown))
			for j, k := range unknown {
				parts[j] = fmt.Sprintf("%q", k)
				if s := suggestTableFieldKey(k); s != "" {
					parts[j] += fmt.Sprintf(" (did you mean %q?)", s)
				}
			}
			return nil, fmt.Errorf("%s: unknown attribute %s; a field takes only %s",
				label, strings.Join(parts, ", "), strings.Join(tableFieldJSONKeys, ", "))
		}

		var f TableField
		buf, _ := json.Marshal(norm)
		if err := json.Unmarshal(buf, &f); err != nil {
			return nil, fmt.Errorf("field %d: %w", i+1, err)
		}
		if f.Name == "" || f.Type == "" {
			return nil, fmt.Errorf("field %d: \"name\" and \"type\" are required", i+1)
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// suggestTableFieldKey maps a near miss (not_null, notnull, NOTNULL, is_key)
// to the attribute it most likely meant.
func suggestTableFieldKey(k string) string {
	norm := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(k))
	if norm == "iskey" {
		return "key"
	}
	for _, known := range tableFieldJSONKeys {
		if norm == strings.ToLower(known) {
			return known
		}
	}
	return ""
}
