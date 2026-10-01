package adt

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// A DDIC structure is, to ADT, a source object like a table: a
// blue:blueSource of type TABL/DS whose source is DDL. The same object type
// carries append structures -- their source is "extend type <base> with
// <append> { ... }" -- so one function creates both.

// StructureOptions describes a structure or append structure to create.
type StructureOptions struct {
	Name        string
	Description string
	Package     string // defaults to $TMP
	Transport   string
	// Source is the complete DDL: "define structure <name> { ... }" for a
	// structure, "extend type <base> with <name> { ... }" for an append.
	Source         string
	MasterLanguage string // ISO code; the session language when empty
}

// StructureResult is what CreateStructure did.
type StructureResult struct {
	Name       string            `json:"name"`
	ObjectURL  string            `json:"objectUrl"`
	Append     bool              `json:"append,omitempty"`
	Extends    string            `json:"extends,omitempty"`
	Active     bool              `json:"active"`
	Activation *ActivationResult `json:"activation,omitempty"`
}

var (
	reDefineStructure = regexp.MustCompile(`(?is)\bdefine\s+structure\s+([a-z0-9_/]+)`)
	reExtendType      = regexp.MustCompile(`(?is)\bextend\s+type\s+([a-z0-9_/]+)\s+with\s+([a-z0-9_/]+)`)
)

// StructureURL is the ADT URL of a structure.
func StructureURL(name string) string {
	return "/sap/bc/adt/ddic/structures/" + url.PathEscape(strings.ToLower(name))
}

// structureSourceName reads the name the DDL declares, and the base type of
// an append. The name in the source has to be the object's name: SAP refuses
// a mismatch only at activation, after the object exists.
//
// Comments and string literals are blanked out first, so a declaration quoted
// in an annotation or commented out does not count; the first declaration left
// is the one.
func structureSourceName(source string) (name, extends string, err error) {
	code := ddlCode(source)
	ext := reExtendType.FindStringSubmatchIndex(code)
	def := reDefineStructure.FindStringSubmatchIndex(code)
	switch {
	case ext != nil && (def == nil || ext[0] < def[0]):
		return strings.ToUpper(code[ext[4]:ext[5]]), strings.ToUpper(code[ext[2]:ext[3]]), nil
	case def != nil:
		return strings.ToUpper(code[def[2]:def[3]]), "", nil
	}
	return "", "", fmt.Errorf(`the source must be "define structure <name> { ... }" or "extend type <base> with <append> { ... }"`)
}

// ddlCode replaces the comments (//, --, /* */) and single-quoted string
// literals of DDL source with spaces, keeping every other byte in place.
func ddlCode(source string) string {
	b := []byte(source)
	for i := 0; i < len(b); {
		j := i + 1
		switch {
		case b[i] == '\'':
			for j < len(b) && b[j] != '\'' && b[j] != '\n' {
				j++
			}
			if j < len(b) && b[j] == '\'' {
				j++
			}
		case i+1 < len(b) && (b[i] == '/' && b[i+1] == '/' || b[i] == '-' && b[i+1] == '-'):
			for j < len(b) && b[j] != '\n' {
				j++
			}
		case i+1 < len(b) && b[i] == '/' && b[i+1] == '*':
			j = i + 2
			for j+1 < len(b) && (b[j] != '*' || b[j+1] != '/') {
				j++
			}
			j = min(j+2, len(b))
		default:
			i++
			continue
		}
		blank(b, i, j)
		i = j
	}
	return string(b)
}

// blank overwrites b[from:to] with spaces, line breaks excepted.
func blank(b []byte, from, to int) {
	for k := from; k < to; k++ {
		if b[k] != '\n' {
			b[k] = ' '
		}
	}
}

// CreateStructure creates a structure or an append structure, writes its DDL
// source and activates it. Activation is checked against the inactive list
// afterwards: SAP has answered an append's activation with success while
// leaving only the inactive version in DD02L.
func (c *Client) CreateStructure(ctx context.Context, opts StructureOptions) (*StructureResult, error) {
	opts.Name = strings.ToUpper(strings.TrimSpace(opts.Name))
	if opts.Package == "" {
		opts.Package = "$TMP"
	}
	opts.Package = strings.ToUpper(opts.Package)
	name, extends, err := structureSourceName(opts.Source)
	if err != nil {
		return nil, err
	}
	if opts.Name == "" {
		opts.Name = name
	}
	if name != opts.Name {
		return nil, fmt.Errorf("the source declares %s, not %s", name, opts.Name)
	}
	if len(opts.Name) > 30 {
		return nil, fmt.Errorf("structure name %s is longer than 30 characters", opts.Name)
	}
	if opts.Description == "" {
		return nil, fmt.Errorf("a description is required")
	}

	if err = c.checkMutation(ctx, MutationContext{
		Op:        OpCreate,
		OpName:    "CreateStructure",
		Package:   opts.Package,
		Transport: opts.Transport,
	}); err != nil {
		return nil, err
	}

	lang := strings.ToUpper(opts.MasterLanguage)
	if lang == "" {
		lang = strings.ToUpper(c.config.Language)
	}
	langAttr := ""
	if lang != "" {
		langAttr = fmt.Sprintf(` adtcore:masterLanguage="%s"`, escapeXML(lang))
	}
	body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<blue:blueSource xmlns:blue="http://www.sap.com/wbobj/blue" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="%s" adtcore:type="TABL/DS" adtcore:description="%s"%s>
  <adtcore:packageRef adtcore:name="%s"/>
</blue:blueSource>`, escapeXML(opts.Name), escapeXML(opts.Description), langAttr, escapeXML(opts.Package))

	params := url.Values{}
	if opts.Transport != "" {
		params.Set("corrNr", opts.Transport)
	}
	if _, err = c.transport.Request(ctx, "/sap/bc/adt/ddic/structures", &RequestOptions{
		Method:      http.MethodPost,
		Query:       params,
		Body:        []byte(body),
		ContentType: "application/vnd.sap.adt.structures.v2+xml",
		Accept:      "application/vnd.sap.adt.structures.v2+xml",
	}); err != nil {
		return nil, fmt.Errorf("creating structure %s: %w", opts.Name, err)
	}

	objectURL := StructureURL(opts.Name)
	res := &StructureResult{Name: opts.Name, ObjectURL: objectURL, Append: extends != "", Extends: extends}

	// The structure was just created in the package the gate accepted, so the
	// write below does not resolve it again inside the lock (#91).
	ctx = withMutationPackageChecked(ctx, objectURL)
	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		return res, fmt.Errorf("structure %s created, but locking it failed: %w", opts.Name, err)
	}
	if err = c.UpdateSource(ctx, objectURL+"/source/main", opts.Source, lock.LockHandle, opts.Transport); err != nil {
		if uerr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); uerr != nil {
			return res, fmt.Errorf("structure %s created, but writing its source failed: %w — %s", opts.Name, err, strandedLockAdvice(objectURL, uerr))
		}
		return res, fmt.Errorf("structure %s created, but writing its source failed: %w", opts.Name, err)
	}
	if err = c.UnlockObject(ctx, objectURL, lock.LockHandle); err != nil {
		return res, fmt.Errorf("unlocking structure %s: %w — %s", opts.Name, err, strandedLockAdvice(objectURL, err))
	}

	activation, err := c.Activate(ctx, objectURL, opts.Name)
	if err != nil {
		return res, fmt.Errorf("activating structure %s: %w", opts.Name, err)
	}
	res.Activation = activation
	if !activation.Success {
		return res, fmt.Errorf("structure %s was created but did not activate: %s", opts.Name, strings.Join(activation.ProblemLines(), "; "))
	}
	// Trust the inactive list, not the answer to the activation. Without the
	// list there is nothing to confirm the activation with.
	records, err := c.GetInactiveObjects(ctx)
	if err != nil {
		return res, fmt.Errorf("structure %s: SAP reported the activation as successful, but the inactive list could not be read to confirm it: %w", opts.Name, err)
	}
	if objectInactive(objectURL, records) {
		return res, fmt.Errorf("structure %s: SAP reported the activation as successful, but the structure is still inactive", opts.Name)
	}
	res.Active = true
	return res, nil
}
