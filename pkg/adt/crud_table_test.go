package adt

import (
	"strings"
	"testing"
)

// --- create TABL: the client field and the field attributes (issue #254) ---

func boolPtr(b bool) *bool { return &b }

func tableOpts(fields ...TableField) CreateTableOptions {
	return CreateTableOptions{
		Name:          "ZDEMO_T",
		Description:   "demo",
		Package:       "$TMP",
		DeliveryClass: "A",
		TableCategory: "TRANSPARENT",
		Fields:        fields,
	}
}

func clientLines(ddl string) []string {
	var out []string
	for _, line := range strings.Split(ddl, "\n") {
		if strings.Contains(line, "abap.clnt") || strings.Contains(line, ": mandt") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func TestGenerateTableDDL_ClientField(t *testing.T) {
	mandt := TableField{Name: "MANDT", Type: "MANDT", IsKey: true}
	id := TableField{Name: "ID", Type: "CHAR10", IsKey: true}
	val := TableField{Name: "VAL", Type: "INT4"}

	tests := []struct {
		name       string
		opts       CreateTableOptions
		wantClient []string // the client-typed lines, in order
		wantErr    string
	}{
		{
			// The issue's call: the caller's own MANDT is the client field,
			// and no second one is put in front of it.
			name:       "first key field MANDT is the client field",
			opts:       tableOpts(mandt, id, val),
			wantClient: []string{"key mandt : mandt not null;"},
		},
		{
			name:       "built-in CLNT is a client field too",
			opts:       tableOpts(TableField{Name: "MANDT", Type: "CLNT", IsKey: true}, id),
			wantClient: []string{"key mandt : abap.clnt not null;"},
		},
		{
			// Backward compatible default: no client field in the list, so
			// vsp adds one as it always has.
			name:       "no client field: CLIENT is added in front",
			opts:       tableOpts(id, val),
			wantClient: []string{"key client : abap.clnt not null;"},
		},
		{
			name: "client_dependent true without a client field adds one",
			opts: func() CreateTableOptions {
				o := tableOpts(id, val)
				o.ClientDependent = boolPtr(true)
				return o
			}(),
			wantClient: []string{"key client : abap.clnt not null;"},
		},
		{
			name: "client_dependent false: no client field",
			opts: func() CreateTableOptions {
				o := tableOpts(id, val)
				o.ClientDependent = boolPtr(false)
				return o
			}(),
			wantClient: nil,
		},
		{
			name: "client_dependent false with a MANDT first key is contradictory",
			opts: func() CreateTableOptions {
				o := tableOpts(mandt, id)
				o.ClientDependent = boolPtr(false)
				return o
			}(),
			wantErr: "client_dependent is false",
		},
		{
			name:    "a client-typed key field that is not the first key is refused",
			opts:    tableOpts(id, mandt),
			wantErr: "only the first key field can be the client field",
		},
		{
			name:    "a second client-typed key after the client field is refused",
			opts:    tableOpts(mandt, TableField{Name: "MANDT2", Type: "CLNT", IsKey: true}),
			wantErr: "only the first key field can be the client field",
		},
		{
			name:    "a non-key MANDT field is refused rather than doubled",
			opts:    tableOpts(TableField{Name: "MANDT", Type: "MANDT"}, id),
			wantErr: `no "key": true`,
		},
		{
			// A data column that holds a client (the source client of a
			// copy, say) is not the client field and is allowed.
			name:       "a non-key client-typed data column is allowed",
			opts:       tableOpts(mandt, id, TableField{Name: "SRC_CLIENT", Type: "MANDT"}),
			wantClient: []string{"key mandt : mandt not null;", "src_client : mandt;"},
		},
		{
			name:       "a non-key client-typed data column next to the added CLIENT",
			opts:       tableOpts(id, TableField{Name: "SRC_CLIENT", Type: "CLNT"}),
			wantClient: []string{"key client : abap.clnt not null;", "src_client : abap.clnt;"},
		},
		{
			name:       "type is trimmed before it is recognised and mapped",
			opts:       tableOpts(TableField{Name: "MANDT", Type: " clnt ", IsKey: true}, id),
			wantClient: []string{"key mandt : abap.clnt not null;"},
		},
		{
			// Named like a client field, typed with something vsp cannot
			// place: guessing either way can double the client field.
			name:    "first key named MANDT with an unknown type needs client_dependent",
			opts:    tableOpts(TableField{Name: "MANDT", Type: "SYMANDT", IsKey: true}, id),
			wantErr: "is MANDT your client field? pass client_dependent:true",
		},
		{
			name:    "first key named CLIENT with data element ZCLIENT needs client_dependent",
			opts:    tableOpts(TableField{Name: "client", Type: "ZCLIENT", IsKey: true}, id),
			wantErr: "is CLIENT your client field? pass client_dependent:true",
		},
		{
			name:    "first key named CLIENT with built-in CHAR3 is refused by default",
			opts:    tableOpts(TableField{Name: "client", Type: "CHAR3", IsKey: true}, id),
			wantErr: "give it type MANDT",
		},
		{
			name: "client_dependent true makes a named MANDT first key the client field as is",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "MANDT", Type: "/ABC/MANDT", IsKey: true}, id)
				o.ClientDependent = boolPtr(true)
				return o
			}(),
			wantClient: nil, // no abap.clnt line added; /abc/mandt is used as is
		},

		// Under client_dependent:true, SAP would make the table
		// client-independent while vsp reported client_dependent:true.
		{
			name: "client_dependent true refuses a named MANDT of built-in type CHAR3",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "MANDT", Type: "CHAR3", IsKey: true}, id)
				o.ClientDependent = boolPtr(true)
				return o
			}(),
			wantErr: "SAP never treats as a client field",
		},
		{
			name: "client_dependent true refuses a named MANDT of built-in type NUMC3",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "MANDT", Type: "NUMC3", IsKey: true}, id)
				o.ClientDependent = boolPtr(true)
				return o
			}(),
			wantErr: "SAP never treats as a client field",
		},
		{
			name: "client_dependent true refuses a named MANDT of built-in type INT4",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "MANDT", Type: "INT4", IsKey: true}, id)
				o.ClientDependent = boolPtr(true)
				return o
			}(),
			wantErr: "SAP never treats as a client field",
		},
		{
			name: "client_dependent true refuses a named MANDT of built-in type STRING",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "MANDT", Type: "STRING", IsKey: true}, id)
				o.ClientDependent = boolPtr(true)
				return o
			}(),
			wantErr: "SAP never treats as a client field",
		},
		{
			name: "client_dependent true refuses a named MANDT of built-in type abap.char(3)",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "MANDT", Type: "abap.char(3)", IsKey: true}, id)
				o.ClientDependent = boolPtr(true)
				return o
			}(),
			wantErr: "SAP never treats as a client field",
		},
		{
			// Under client_dependent:false, SAP may well make it client-dependent.
			name: "client_dependent false refuses a named MANDT data element",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "MANDT", Type: "ZMANDT", IsKey: true}, id)
				o.ClientDependent = boolPtr(false)
				return o
			}(),
			wantErr: "looks like a client field; rename it or pass client_dependent:true",
		},
		{
			name: "client_dependent false refuses a named CLIENT data element",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "CLIENT", Type: "SYMANDT", IsKey: true}, id)
				o.ClientDependent = boolPtr(false)
				return o
			}(),
			wantErr: "looks like a client field",
		},
		{
			name: "client_dependent false keeps a named MANDT of built-in type as a plain field",
			opts: func() CreateTableOptions {
				o := tableOpts(TableField{Name: "MANDT", Type: "CHAR3", IsKey: true}, id)
				o.ClientDependent = boolPtr(false)
				return o
			}(),
			wantClient: nil,
		},
		{
			name:    "a field named CLIENT would collide with the added one",
			opts:    tableOpts(id, TableField{Name: "client", Type: "CHAR3"}),
			wantErr: "collides",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ddl, err := generateTableDDL(tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want an error containing %q, got DDL:\n%s", tt.wantErr, ddl)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("generateTableDDL: %v", err)
			}
			got := clientLines(ddl)
			if strings.Join(got, "|") != strings.Join(tt.wantClient, "|") {
				t.Errorf("client lines = %q, want %q; DDL:\n%s", got, tt.wantClient, ddl)
			}
		})
	}
}

func TestParseTableFields_RefusesUnknownAttributes(t *testing.T) {
	_, err := ParseTableFields(`[{"name":"MANDT","type":"MANDT","key":true,"not_null":true}]`)
	if err == nil {
		t.Fatal(`"not_null" was accepted; it used to be dropped without a word`)
	}
	for _, want := range []string{`"not_null"`, `did you mean "notNull"`, "MANDT", "field 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	for _, js := range []string{
		`[{"name":"A","type":"INT4","not-null":true}]`,
		`[{"name":"A","type":"INT4","is_key":true}]`,
		`[{"name":"A","type":"INT4","nullable":false}]`,
	} {
		if _, err := ParseTableFields(js); err == nil {
			t.Errorf("%s: unknown attribute accepted", js)
		}
	}
}

func TestParseTableFields_AcceptsKnownAttributes(t *testing.T) {
	fields, err := ParseTableFields(`[
		{"name":"MANDT","type":"MANDT","key":true},
		{"name":"AMT","type":"DEC","length":15,"decimals":2,"description":"amount","notNull":true}
	]`)
	if err != nil {
		t.Fatalf("ParseTableFields: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("got %d fields, want 2", len(fields))
	}
	if !fields[0].IsKey || fields[0].Name != "MANDT" {
		t.Errorf("field 1 = %+v", fields[0])
	}
	f := fields[1]
	if !f.NotNull || f.Length != 15 || f.Decimals != 2 || f.Description != "amount" {
		t.Errorf("field 2 = %+v", f)
	}
}

func TestParseTableFields_RequiresNameAndType(t *testing.T) {
	for _, js := range []string{`[{"type":"INT4"}]`, `[{"name":"A"}]`, `[null]`, `{"name":"A"}`} {
		if _, err := ParseTableFields(js); err == nil {
			t.Errorf("%s: accepted", js)
		}
	}
}

// encoding/json matched attribute names case-insensitively before #254, so
// "Key" and "NotNull" worked; the strict parser keeps that.
func TestParseTableFields_AttributeNamesAreCaseInsensitive(t *testing.T) {
	fields, err := ParseTableFields(`[{"Name":"ID","TYPE":"CHAR10","Key":true},{"name":"VAL","type":"INT4","NotNull":true,"notnull":false}]`)
	if err == nil {
		t.Fatalf("NotNull and notnull on one field were both accepted: %+v", fields)
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("error %q does not say the attribute is given twice", err)
	}

	fields, err = ParseTableFields(`[{"Name":"ID","TYPE":"CHAR10","Key":true},{"name":"VAL","type":"INT4","NotNull":true}]`)
	if err != nil {
		t.Fatalf("ParseTableFields: %v", err)
	}
	if fields[0].Name != "ID" || fields[0].Type != "CHAR10" || !fields[0].IsKey {
		t.Errorf("field 1 = %+v", fields[0])
	}
	if !fields[1].NotNull {
		t.Errorf("NotNull was dropped: %+v", fields[1])
	}
}

func TestResolveCreateTableSpec_ParsesRawArguments(t *testing.T) {
	no := "false"
	opts, err := ResolveCreateTableSpec(CreateTableOptions{
		FieldsJSON:         `[{"name":"ID","type":"CHAR10","key":true}]`,
		ClientDependentArg: &no,
	})
	if err != nil {
		t.Fatalf("ResolveCreateTableSpec: %v", err)
	}
	if len(opts.Fields) != 1 || opts.ClientDependent == nil || *opts.ClientDependent {
		t.Errorf("resolved = %+v", opts)
	}

	bad := "maybe"
	if _, err := ResolveCreateTableSpec(CreateTableOptions{FieldsJSON: `[{"name":"ID","type":"CHAR10"}]`, ClientDependentArg: &bad}); err == nil ||
		!strings.Contains(err.Error(), "client_dependent must be true or false") {
		t.Errorf("client_dependent=maybe: %v", err)
	}
	if _, err := ResolveCreateTableSpec(CreateTableOptions{FieldsJSON: `[]`}); err == nil {
		t.Error("an empty field list was accepted")
	}
}
