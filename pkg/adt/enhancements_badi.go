package adt

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// A BAdI implementation -- an ENHO of tool type BADI_IMPL -- is created the way
// the editor's wizard does it, in two requests: a POST of an empty container
// that names only the enhancement spot, then the implementation itself written
// into it (lock, PUT, unlock). A POST that carries the implementation straight
// away is refused with an empty "I::000", and leaves a catalog entry behind.

const (
	enhoxhbCollection  = "/sap/bc/adt/enhancements/enhoxhb"
	enhoxhbContentType = "application/vnd.sap.adt.enh.enhoxhb.v4+xml"
)

// BadiImplementationOptions describes a BAdI implementation to create.
type BadiImplementationOptions struct {
	// Name is the enhancement implementation (the ENHO).
	Name        string
	Description string
	Package     string
	Transport   string
	// Spot is the enhancement spot the BAdI belongs to.
	Spot string
	// BadiDefinition is the BAdI; it defaults to the spot's name, which is
	// what it is for every spot with a single BAdI.
	BadiDefinition string
	// ImplementingClass must exist and implement the BAdI's interface.
	ImplementingClass string
	// Implementation is the BAdI implementation's name inside the ENHO; it
	// defaults to Name.
	Implementation string
	ShortText      string
	// Inactive creates the implementation switched off: the ENHO is active,
	// the implementation is not called.
	Inactive bool
}

// CreateBadiImplementation creates an ENHO with one BAdI implementation and
// returns its URL. The ENHO is left inactive; activate it to put it in force.
func (c *Client) CreateBadiImplementation(ctx context.Context, opts BadiImplementationOptions) (string, error) {
	opts.Name = strings.ToUpper(strings.TrimSpace(opts.Name))
	opts.Package = strings.ToUpper(strings.TrimSpace(opts.Package))
	opts.Spot = strings.ToUpper(strings.TrimSpace(opts.Spot))
	opts.BadiDefinition = strings.ToUpper(strings.TrimSpace(opts.BadiDefinition))
	opts.ImplementingClass = strings.ToUpper(strings.TrimSpace(opts.ImplementingClass))
	opts.Implementation = strings.ToUpper(strings.TrimSpace(opts.Implementation))
	if opts.Name == "" || opts.Package == "" || opts.Spot == "" || opts.ImplementingClass == "" {
		return "", fmt.Errorf("name, package, the enhancement spot and the implementing class are required")
	}
	if opts.Description == "" {
		return "", fmt.Errorf("a description is required")
	}
	if opts.BadiDefinition == "" {
		opts.BadiDefinition = opts.Spot
	}
	if opts.Implementation == "" {
		opts.Implementation = opts.Name
	}
	objectURL := enhoxhbCollection + "/" + url.PathEscape(strings.ToLower(opts.Name))

	transport, err := c.enhancementCreateGates(ctx, "CreateBadiImplementation", opts.Package, opts.Transport, objectURL)
	if err != nil {
		return "", err
	}
	opts.Transport = transport
	// The implementation is written by a PUT into the container: an update.
	// Refused here, before the POST, so a refusal leaves no empty ENHO.
	if err = c.checkSafety(OpUpdate, "CreateBadiImplementation"); err != nil {
		return "", err
	}

	params := url.Values{}
	if opts.Transport != "" {
		params.Set("corrNr", opts.Transport)
	}
	if _, err = c.transport.Request(ctx, enhoxhbCollection, &RequestOptions{
		Method:      http.MethodPost,
		Query:       params,
		Body:        []byte(badiImplementationBody(opts, c.config.Language, false)),
		ContentType: enhoxhbContentType,
		Accept:      enhoxhbContentType,
	}); err != nil {
		return "", fmt.Errorf("creating %s: %w", opts.Name, err)
	}

	lock, err := c.LockObject(ctx, objectURL, "MODIFY", opts.Transport)
	if err != nil {
		return objectURL, fmt.Errorf("created %s, but locking it to add the implementation failed: %w", opts.Name, err)
	}
	// With no request chosen at creation, the PUT goes with the one the lock
	// names, as every other write under a lock does.
	if opts.Transport, err = c.resolveWriteTransport(opts.Transport, lock.CorrNr, "CreateBadiImplementation"); err != nil {
		if uerr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); uerr != nil {
			return objectURL, fmt.Errorf("created %s, but %w; %s", opts.Name, err, strandedLockAdvice(objectURL, uerr))
		}
		return objectURL, fmt.Errorf("created %s, but %w", opts.Name, err)
	}
	put := url.Values{}
	put.Set("lockHandle", lock.LockHandle)
	if opts.Transport != "" {
		put.Set("corrNr", opts.Transport)
	}
	_, err = c.transport.Request(ctx, objectURL, &RequestOptions{
		Method:      http.MethodPut,
		Query:       put,
		Body:        []byte(badiImplementationBody(opts, c.config.Language, true)),
		ContentType: enhoxhbContentType,
		Accept:      enhoxhbContentType,
		Stateful:    true,
	})
	if err != nil {
		// Released on a context of its own: the PUT may have failed because
		// ctx was cancelled, and the lock must not stay behind.
		if uerr := c.releaseLockAfterFailure(ctx, objectURL, lock.LockHandle); uerr != nil {
			return objectURL, fmt.Errorf("created %s, but adding the implementation failed: %w; %s", opts.Name, err, strandedLockAdvice(objectURL, uerr))
		}
		return objectURL, fmt.Errorf("created %s, but adding the implementation failed: %w", opts.Name, err)
	}
	if uerr := c.UnlockObject(ctx, objectURL, lock.LockHandle); uerr != nil {
		return objectURL, fmt.Errorf("created %s with its implementation, but %s", opts.Name, strandedLockAdvice(objectURL, uerr))
	}
	return objectURL, nil
}

// badiImplementationBody is the BADI_IMPL document: the container the POST
// creates, naming the spot, and -- with the implementation -- what the PUT
// writes into it.
func badiImplementationBody(opts BadiImplementationOptions, language string, withImplementation bool) string {
	lang := ""
	if language != "" {
		lang = fmt.Sprintf(` adtcore:language=%q adtcore:masterLanguage=%q`, xmlAttr(language), xmlAttr(language))
	}
	impl := "<enho:badiImplementations/>"
	if withImplementation {
		active := "true"
		if opts.Inactive {
			active = "false"
		}
		impl = fmt.Sprintf(`<enho:badiImplementations>
        <enho:badiImplementation enho:name="%s" enho:shortText="%s" enho:example="false" enho:default="false" enho:active="%s" enho:customizingLock="">
          <enho:enhancementSpot adtcore:type="ENHS/XSB" adtcore:name="%s"/>
          <enho:badiDefinition adtcore:type="ENHS/XB" adtcore:name="%s"/>
          <enho:implementingClass adtcore:type="CLAS/OC" adtcore:name="%s"/>
        </enho:badiImplementation>
      </enho:badiImplementations>`,
			xmlAttr(opts.Implementation), xmlAttr(opts.ShortText), active,
			xmlAttr(opts.Spot), xmlAttr(opts.BadiDefinition), xmlAttr(opts.ImplementingClass))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<enho:objectData xmlns:enho="http://www.sap.com/adt/enhancements/enho" xmlns:adtcore="http://www.sap.com/adt/core" xmlns:enhcore="http://www.sap.com/abapsource/enhancementscore" adtcore:name="%s" adtcore:type="ENHO/XHB" adtcore:description="%s"%s>
  <adtcore:packageRef adtcore:name="%s"/>
  <enho:contentCommon enho:toolType="BADI_IMPL">
    <enho:usages>
      <enhcore:referencedObject enhcore:element_usage="EXTO" enhcore:program_id="R3TR">
        <enhcore:objectReference adtcore:name="%s" adtcore:type="ENHS/XS"/>
        <enhcore:mainObjectReference/>
      </enhcore:referencedObject>
    </enho:usages>
  </enho:contentCommon>
  <enho:contentSpecific>
    <enho:badiTechnology>
      %s
    </enho:badiTechnology>
  </enho:contentSpecific>
</enho:objectData>`,
		xmlAttr(opts.Name), xmlAttr(opts.Description), lang, xmlAttr(opts.Package), xmlAttr(opts.Spot), impl)
}
