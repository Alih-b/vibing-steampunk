package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Creating a source code plug-in -- an ENHO of tool type HOOK_IMPL, the
// implementation of an enhancement option such as the start or the end of a
// function module -- is what the editor does on "Enhance". ADT has had the
// resource for it all along (/sap/bc/adt/enhancements/enhoxhh); vsp could only
// read and edit an existing one.

const (
	enhoxhhCollection  = "/sap/bc/adt/enhancements/enhoxhh"
	enhoxhhContentType = "application/vnd.sap.adt.enh.enhoxhh.v3+xml"
	enhOptionsAccept   = "application/vnd.sap.adt.enhancementoptions.v2+xml"
)

// EnhancementOption is one place in an object that can be enhanced.
type EnhancementOption struct {
	// FullName identifies the option, e.g. \FU:BAPI_X\SE:BEGIN\EI.
	FullName    string `json:"fullName"`
	Description string `json:"description"`
	// Mode is "static", "dynamic" or "any".
	Mode string `json:"mode,omitempty"`
	// Source points at the line the option sits on.
	Source string `json:"source,omitempty"`
}

// EnhancementOptions lists the enhancement options of an object -- a function
// group, a program or a class, given by its ADT URL.
func (c *Client) EnhancementOptions(ctx context.Context, objectURL string) ([]EnhancementOption, error) {
	objectURL = strings.TrimRight(strings.TrimSpace(objectURL), "/")
	if objectURL == "" {
		return nil, fmt.Errorf("the object whose enhancement options to list is required")
	}
	resp, err := c.transport.Request(ctx, objectURL+"/enhancements/options", &RequestOptions{
		Method: http.MethodGet,
		Accept: enhOptionsAccept,
	})
	if err != nil {
		return nil, fmt.Errorf("reading the enhancement options of %s: %w", objectURL, err)
	}
	return parseEnhancementOptions(resp.Body)
}

func parseEnhancementOptions(data []byte) ([]EnhancementOption, error) {
	var doc struct {
		Options []struct {
			FullName    string `xml:"full_name,attr"`
			Description string `xml:"fullDescription,attr"`
			Mode        string `xml:"mode,attr"`
			Links       []struct {
				Href string `xml:"href,attr"`
				Rel  string `xml:"rel,attr"`
			} `xml:"link"`
		} `xml:"option"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing enhancement options: %w", err)
	}
	out := make([]EnhancementOption, 0, len(doc.Options))
	for _, o := range doc.Options {
		opt := EnhancementOption{FullName: o.FullName, Description: o.Description, Mode: o.Mode}
		for _, l := range o.Links {
			if strings.HasSuffix(l.Rel, "/source") {
				opt.Source = l.Href
			}
		}
		out = append(out, opt)
	}
	return out, nil
}

// SourceCodePluginOptions describes a source code plug-in to create.
type SourceCodePluginOptions struct {
	Name        string
	Description string
	Package     string
	Transport   string
	// ObjectURL is the enhanced main object: a function group
	// (/sap/bc/adt/functions/groups/<g>), a program or a class.
	ObjectURL string
	// Option is the enhancement option's full name, as EnhancementOptions
	// lists it: \FU:BAPI_X\SE:BEGIN\EI.
	Option string
	// Mode is the option's mode from EnhancementOptions; "static" makes a
	// static plug-in, anything else a dynamic one, as the editor does.
	Mode string
	// Source, when given, is written into the new plug-in: syntax-checked
	// first, then under a lock, with the creation's transport.
	Source string
}

// enhancedObject is what an ENHO records about the object it enhances.
type enhancedObject struct {
	URI, Type, Name, Program string
}

// enhancedObjectFor derives the enhanced object from its ADT URL: the type,
// the name and the main program the option lives in.
func enhancedObjectFor(objectURL string) (enhancedObject, error) {
	u := strings.TrimRight(strings.TrimSpace(objectURL), "/")
	// leaf is the object name right after prefix: there must be one, and
	// nothing after it -- a collection URL or a subresource such as
	// .../source/main is not the object.
	leaf := func(prefix string) (string, bool) {
		rest, ok := strings.CutPrefix(u, prefix)
		if !ok || rest == "" || strings.Contains(rest, "/") {
			return "", false
		}
		if n, err := url.PathUnescape(rest); err == nil {
			rest = n
		}
		return strings.ToUpper(rest), true
	}
	if g, ok := leaf("/sap/bc/adt/functions/groups/"); ok {
		return enhancedObject{URI: u, Type: "FUGR/F", Name: g, Program: functionPool(g)}, nil
	}
	if p, ok := leaf("/sap/bc/adt/programs/programs/"); ok {
		return enhancedObject{URI: u, Type: "PROG/P", Name: p, Program: p}, nil
	}
	if cl, ok := leaf("/sap/bc/adt/oo/classes/"); ok {
		return enhancedObject{URI: u, Type: "CLAS/OC", Name: cl, Program: classPool(cl)}, nil
	}
	return enhancedObject{}, fmt.Errorf("%s: the enhanced object must be a function group, a program or a class (its ADT URL)", objectURL)
}

// functionPool is a function group's main program: SAPL<group>, with a
// namespace kept in front (/NS/SAPLGROUP).
func functionPool(group string) string {
	if strings.HasPrefix(group, "/") {
		if i := strings.Index(group[1:], "/"); i >= 0 {
			return group[:i+2] + "SAPL" + group[i+2:]
		}
	}
	return "SAPL" + group
}

// classPool is a class's main program: the name padded with '=' to 30, then CP.
func classPool(class string) string {
	if len(class) < 30 {
		class += strings.Repeat("=", 30-len(class))
	}
	return class + "CP"
}

// CreateSourceCodePlugin creates an ENHO that implements one enhancement
// option, inactive and with an empty ENHANCEMENT 1 ... ENDENHANCEMENT block,
// the way the editor's "Enhance" does. It returns the new object's URL; the
// code goes in with an ordinary source write.
func (c *Client) CreateSourceCodePlugin(ctx context.Context, opts SourceCodePluginOptions) (string, error) {
	opts.Name = strings.ToUpper(strings.TrimSpace(opts.Name))
	opts.Package = strings.ToUpper(strings.TrimSpace(opts.Package))
	opts.Option = strings.TrimSpace(opts.Option)
	if opts.Name == "" || opts.Package == "" || opts.Option == "" {
		return "", fmt.Errorf("name, package and the enhancement option are required")
	}
	if opts.Description == "" {
		return "", fmt.Errorf("a description is required")
	}
	target, err := enhancedObjectFor(opts.ObjectURL)
	if err != nil {
		return "", err
	}
	objectURL := enhoxhhCollection + "/" + url.PathEscape(strings.ToLower(opts.Name))

	transport, err := c.enhancementCreateGates(ctx, "CreateSourceCodePlugin", opts.Package, opts.Transport, objectURL)
	if err != nil {
		return "", err
	}
	opts.Transport = transport
	if opts.Source != "" {
		if err = c.checkSafety(OpUpdate, "CreateSourceCodePlugin"); err != nil {
			return "", err
		}
	}

	params := url.Values{}
	if opts.Transport != "" {
		params.Set("corrNr", opts.Transport)
	}
	_, err = c.transport.Request(ctx, enhoxhhCollection, &RequestOptions{
		Method:      http.MethodPost,
		Query:       params,
		Body:        []byte(sourceCodePluginBody(opts, target, c.config.Language)),
		ContentType: enhoxhhContentType,
		Accept:      enhoxhhContentType,
	})
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", opts.Name, err)
	}
	if opts.Source != "" {
		if err = c.writeEnhancementSource(ctx, objectURL, opts.Source, opts.Transport); err != nil {
			return objectURL, fmt.Errorf("created %s with an empty ENHANCEMENT block, but %w", opts.Name, err)
		}
	}
	return objectURL, nil
}

// writeEnhancementSource writes the code of an ENHO this client just created.
// It is syntax-checked before the lock is taken, so broken code is refused
// rather than saved. The package was checked by the creation and the
// transport chosen there, so the write neither resolves the package again
// inside the lock window -- which would retire the session the lock belongs
// to (#91) -- nor goes without the request.
func (c *Client) writeEnhancementSource(ctx context.Context, enhoURL, source, transport string) error {
	if checks, err := c.SyntaxCheck(ctx, enhoURL, source); err == nil {
		var problems []string
		for _, r := range checks {
			if r.Severity == "E" || r.Severity == "A" {
				problems = append(problems, fmt.Sprintf("line %d: %s", r.Line, r.Text))
			}
		}
		if len(problems) > 0 {
			return fmt.Errorf("the code has syntax errors and was not written: %s", strings.Join(problems, "; "))
		}
	}
	ctx = withMutationPackageChecked(ctx, enhoURL)
	lock, err := c.LockObject(ctx, enhoURL, "MODIFY", transport)
	if err != nil {
		return fmt.Errorf("locking it to write the code failed: %w", err)
	}
	// With no request chosen at creation, the write goes with the one the
	// lock names, as every other source write does.
	if transport, err = c.resolveWriteTransport(transport, lock.CorrNr, "CreateSourceCodePlugin"); err != nil {
		if uerr := c.releaseLockAfterFailure(ctx, enhoURL, lock.LockHandle); uerr != nil {
			return fmt.Errorf("%w; %s", err, strandedLockAdvice(enhoURL, uerr))
		}
		return err
	}
	if err = c.UpdateSource(ctx, enhoURL+"/source/main", source, lock.LockHandle, transport); err != nil {
		if uerr := c.releaseLockAfterFailure(ctx, enhoURL, lock.LockHandle); uerr != nil {
			return fmt.Errorf("writing the code failed: %w; %s", err, strandedLockAdvice(enhoURL, uerr))
		}
		return fmt.Errorf("writing the code failed: %w", err)
	}
	if err = c.UnlockObject(ctx, enhoURL, lock.LockHandle); err != nil {
		return fmt.Errorf("the code is written, but %s", strandedLockAdvice(enhoURL, err))
	}
	return nil
}

// enhancementCreateGates runs the checks every ENHO creation shares with
// CreateObject: the mutation policy, the transport choice for a transportable
// package, and the package check that keeps a missing package from leaving an
// orphan enqueue behind. It returns the transport to create with.
func (c *Client) enhancementCreateGates(ctx context.Context, opName, pkg, transport, objectURL string) (string, error) {
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpCreate,
		OpName:    opName,
		Package:   pkg,
		Transport: transport,
	}); err != nil {
		return "", err
	}
	if transport == "" && !strings.HasPrefix(pkg, "$") && c.config.Safety.TransportChoice != "off" {
		choice := c.planTransport(ctx, "", objectURL, pkg)
		if choice.Err != nil {
			return "", choice.Err
		}
		if choice.Transport != "" {
			if err := c.checkTransportableEdit(choice.Transport, opName); err != nil {
				return "", err
			}
			transport = choice.Transport
		}
	}
	if !c.packageExists(ctx, pkg) {
		return "", fmt.Errorf("package %s does not exist - create it first to avoid orphan locks", pkg)
	}
	return transport, nil
}

// sourceCodePluginBody is the enhancement document ADT takes to create a
// HOOK_IMPL. The attributes it lists are the ones the server insists on.
func sourceCodePluginBody(opts SourceCodePluginOptions, target enhancedObject, language string) string {
	mode := "D"
	if strings.EqualFold(opts.Mode, "static") {
		mode = "S"
	}
	lang := ""
	if language != "" {
		lang = fmt.Sprintf(` adtcore:masterLanguage=%q adtcore:language=%q`, xmlAttr(language), xmlAttr(language))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<enho:enhancement xmlns:enho="http://www.sap.com/adt/enhancements/enho" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="%s" adtcore:type="ENHO/XHH" adtcore:description="%s"%s>
  <adtcore:packageRef adtcore:name="%s"/>
  <enho:contentCommon enho:toolType="HOOK_IMPL" enho:adjustmentStatus="" enho:upgradeFlag="false"/>
  <enho:contentSpecific>
    <enho:hookTechnology enho:nextId="2">
      <enho:enhancedObject adtcore:uri="%s" adtcore:type="%s" adtcore:name="%s"/>
      <enho:hookImplementation enho:id="1" enho:spotname="" enho:programname="%s" enho:overwrite="" enho:method="" enho:enhmode="%s" enho:full_name="%s" enho:full_description=""/>
    </enho:hookTechnology>
  </enho:contentSpecific>
</enho:enhancement>`,
		xmlAttr(opts.Name), xmlAttr(opts.Description), lang, xmlAttr(opts.Package),
		xmlAttr(target.URI), xmlAttr(target.Type), xmlAttr(target.Name),
		xmlAttr(target.Program), mode, xmlAttr(opts.Option))
}

func xmlAttr(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return strings.ReplaceAll(b.String(), `"`, "&quot;")
}
