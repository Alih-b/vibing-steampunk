package adt

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Function modules are addressed by two names, not one: a module lives inside a
// group, and its ADT resource is nested under that group's URL. Everywhere else
// in vsp a caller names an object and gets on with it, and the difference has
// been pushed onto them — read and edit both refused a function module until the
// group was supplied, which is a thing the caller often does not know and never
// chose. These helpers resolve the group instead of demanding it, and give the
// module the same one-call edit that programs and classes already have.

// groupFromFunctionURI extracts the group from a module's ADT URI.
func groupFromFunctionURI(uri string) string {
	const marker = "/functions/groups/"
	i := strings.Index(uri, marker)
	if i < 0 {
		return ""
	}
	rest := uri[i+len(marker):]
	slash := strings.Index(rest, "/")
	if slash <= 0 {
		return ""
	}
	// The segment is escaped in the URI (a namespaced group reads
	// "%2fns%2fgroup"); callers escape it again when they build a URL.
	group := rest[:slash]
	if unescaped, err := url.PathUnescape(group); err == nil {
		group = unescaped
	}
	return strings.ToUpper(group)
}

// ResolveFunctionGroup finds the group that owns a function module.
//
// The repository search already returns each hit's ADT URI, and a module's URI
// spells out its group — so the answer is one search away and needs no table
// read and no extra privilege.
func (c *Client) ResolveFunctionGroup(ctx context.Context, functionName string) (string, error) {
	functionName = strings.ToUpper(functionName)
	results, err := c.SearchObject(ctx, functionName, 20)
	if err != nil {
		return "", fmt.Errorf("resolving the group of function module %s: %w", functionName, err)
	}
	for _, r := range results {
		if !strings.EqualFold(r.Name, functionName) {
			continue
		}
		// FUGR/FF is a function module; the group itself is FUGR/F.
		if !strings.EqualFold(r.Type, "FUGR/FF") {
			continue
		}
		if group := groupFromFunctionURI(r.URI); group != "" {
			return group, nil
		}
	}
	return "", fmt.Errorf("cannot find function module %s — give its group explicitly if it exists", functionName)
}

// functionGroupFor returns the group to use: the one the caller supplied, or the
// one the system knows.
func (c *Client) functionGroupFor(ctx context.Context, groupName, functionName string) (string, error) {
	if groupName != "" {
		return strings.ToUpper(groupName), nil
	}
	return c.ResolveFunctionGroup(ctx, functionName)
}

// WriteFunctionModuleResult reports what happened to a function module.
type WriteFunctionModuleResult struct {
	Success      bool                `json:"success"`
	GroupName    string              `json:"groupName"`
	FunctionName string              `json:"functionName"`
	ObjectURL    string              `json:"objectUrl"`
	SyntaxErrors []SyntaxCheckResult `json:"syntaxErrors,omitempty"`
	Activation   *ActivationResult   `json:"activation,omitempty"`
	Message      string              `json:"message,omitempty"`
}

// WriteFunctionModule updates a function module's source in one call, running
// the same sequence a person would: check the syntax, lock, write, unlock,
// activate.
//
// groupName may be empty, in which case the group is resolved from the module
// name. The unlock is not left to the happy path — an object still locked after
// a failed write is the sort of thing that outlives the session and blocks the
// next person, so it is released whatever happens.
func (c *Client) WriteFunctionModule(ctx context.Context, groupName, functionName, source, transport string) (*WriteFunctionModuleResult, error) {
	functionName = strings.ToUpper(functionName)

	group, err := c.functionGroupFor(ctx, groupName, functionName)
	if err != nil {
		return nil, err
	}
	objectURL := GetObjectURL(ObjectTypeFunctionMod, functionName, group)

	// Marking here means writeFunctionModule's own gate — which sits above its
	// lock — resolves nothing a second time (issue #91).
	ctx, err = c.gateAndMark(ctx, MutationContext{
		Op:        OpWorkflow,
		OpName:    "WriteFunctionModule",
		ObjectURL: objectURL,
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}

	result := &WriteFunctionModuleResult{
		GroupName:    group,
		FunctionName: functionName,
		ObjectURL:    objectURL,
	}

	// Step 1: syntax check before anything is locked or written.
	syntaxErrors, err := c.SyntaxCheck(ctx, objectURL, source)
	if err != nil {
		result.Message = fmt.Sprintf("Syntax check failed: %v", err)
		return result, nil
	}
	for _, se := range syntaxErrors {
		if se.Severity == "E" || se.Severity == "A" || se.Severity == "X" {
			result.SyntaxErrors = syntaxErrors
			result.Message = "Source has syntax errors - not saved"
			return result, nil
		}
	}
	result.SyntaxErrors = syntaxErrors

	// Step 2: lock, write, unlock. writeFunctionModule already owns that
	// sequence — it is what the create flow uses — so this borrows it rather
	// than keeping a second copy of the lock discipline in the package.
	if err := c.writeFunctionModule(ctx, group, functionName, "", source, transport); err != nil {
		result.Message = fmt.Sprintf("Failed to write source: %v", err)
		return result, nil
	}

	// Step 3: activate. The write path deliberately stops at unlock, because
	// creating a module activates it once at the end of a longer flow.
	activation, err := c.Activate(ctx, objectURL, functionName)
	if err != nil {
		result.Message = fmt.Sprintf("Failed to activate: %v", err)
		result.Activation = activation
		return result, nil
	}
	result.Activation = activation
	if activation.Success {
		result.Success = true
		result.Message = "Function module updated and activated successfully"
	} else {
		result.Message = "Activation failed - check activation messages"
	}
	return result, nil
}

// writeSourceFunctionModule adapts WriteFunctionModule to the WriteSource shape,
// so a caller editing a function module uses the same call as for a class.
//
// Creation is deliberately not folded in here. A new module needs an interface —
// parameters, their types, whether it is remote-enabled — and none of that can
// be inferred from a source string alone; the create action takes those and is
// the right door.
func (c *Client) writeSourceFunctionModule(ctx context.Context, name, source string, opts *WriteSourceOptions) (*WriteSourceResult, error) {
	result := &WriteSourceResult{
		ObjectType: "FUNC",
		ObjectName: strings.ToUpper(name),
		Mode:       "updated",
	}

	if opts.Mode == WriteModeCreate {
		result.Message = "Creating a function module needs its interface — use the create action with object_type FUGR/FF"
		return result, nil
	}

	written, err := c.WriteFunctionModule(ctx, opts.Parent, name, source, opts.Transport)
	if err != nil {
		return nil, err
	}

	result.ObjectURL = written.ObjectURL
	result.SyntaxErrors = written.SyntaxErrors
	result.Activation = written.Activation
	result.Success = written.Success
	result.Message = written.Message
	return result, nil
}
