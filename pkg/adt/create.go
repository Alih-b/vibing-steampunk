package adt

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// --- Create Object Operations ---

// CreatableObjectType defines types of ABAP objects that can be created.
type CreatableObjectType string

const (
	ObjectTypeProgram       CreatableObjectType = "PROG/P"
	ObjectTypeInclude       CreatableObjectType = "PROG/I"
	ObjectTypeClass         CreatableObjectType = "CLAS/OC"
	ObjectTypeInterface     CreatableObjectType = "INTF/OI"
	ObjectTypeFunctionGroup CreatableObjectType = "FUGR/F"
	ObjectTypeFunctionMod   CreatableObjectType = "FUGR/FF"
	ObjectTypeTable         CreatableObjectType = "TABL/DT"
	ObjectTypePackage       CreatableObjectType = "DEVC/K"
	ObjectTypeMessageClass  CreatableObjectType = "MSAG/N"
	// RAP object types (read-only via ADT, created via RAP generators)
	ObjectTypeDDLS CreatableObjectType = "DDLS/DF"  // CDS DDL Source
	ObjectTypeBDEF CreatableObjectType = "BDEF/BDO" // Behavior Definition
	ObjectTypeSRVD CreatableObjectType = "SRVD/SRV" // Service Definition
	ObjectTypeSRVB CreatableObjectType = "SRVB/SVB" // Service Binding
)

// CreateObjectOptions contains options for creating a new ABAP object.
type CreateObjectOptions struct {
	ObjectType  CreatableObjectType `json:"objectType"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	PackageName string              `json:"packageName"`
	Transport   string              `json:"transport,omitempty"`
	// Chosen, when given, receives the request picked for a transportable
	// object created with no Transport named — reused or created — and why.
	Chosen      *TransportChoice `json:"-"`
	Responsible string           `json:"responsible,omitempty"`
	// For function modules - the function group name
	ParentName string `json:"parentName,omitempty"`
	// For packages - the software component (required for transportable packages)
	SoftwareComponent string `json:"softwareComponent,omitempty"`

	// RAP-specific options
	// For BDEF: the root CDS entity name (e.g., "ZTRAVEL" for define behavior for ZTRAVEL)
	RootEntity string `json:"rootEntity,omitempty"`
	// For BDEF: implementation type (managed, unmanaged, projection, interface, abstract)
	ImplementationType string `json:"implementationType,omitempty"`
	// For SRVB: the service definition name to bind
	ServiceDefinition string `json:"serviceDefinition,omitempty"`
	// For SRVB: binding type ("ODATA" for both V2 and V4)
	BindingType string `json:"bindingType,omitempty"`
	// For SRVB: binding version ("V2" or "V4")
	BindingVersion string `json:"bindingVersion,omitempty"`

	// IAM options
	// For SIA6: application type, e.g. "EXT" (external app) or "IBS"
	// (generated business service). Defaults to EXT.
	AppType string `json:"appType,omitempty"`
	// For SIA6: the object the app stands for, e.g. the generated communication
	// scenario on an IBS app. Empty for a plain external app.
	SecondaryID string `json:"secondaryID,omitempty"`
	// For SIA7: the business catalog receiving the app.
	BusinessCatalogID string `json:"businessCatalogID,omitempty"`
	// For SIA7: the IAM app being assigned.
	AppID string `json:"appID,omitempty"`

	// leavePartialObject is for the workflows that create a throwaway object
	// under a generated name (CheckABAP, ExecuteABAP). When the create fails
	// with anything but "already exists" and the probe then finds an object of
	// that name, nothing shows that this call created it: the name could have
	// been taken already, with the failure hiding the conflict. Such a caller
	// must never delete a stranger's object to tidy up after itself, so with
	// this set the object is left in place and reported, not deleted.
	leavePartialObject bool

	// For SRVB: category per SAP domain SRVB_BND_CATEGORY:
	// "0" = UI (User Interface), "1" = A2X (Application to X users, i.e. Web API)
	BindingCategory string `json:"bindingCategory,omitempty"`

	// For BDEF: source code (required for creation - ADT API embeds source in creation request)
	Source string `json:"source,omitempty"`

	// For MSAG: the original language as an ISO code ("EN", "DE"); the
	// session language when empty.
	MasterLanguage string `json:"masterLanguage,omitempty"`
}

// objectTypeInfo contains metadata for creating object types.
type objectTypeInfo struct {
	creationPath string
	rootName     string
	namespace    string
	// bodyBuilder, when set, replaces the generic create payload for this type.
	// Types whose ADT resource needs a nested <content> block register one from
	// their own file, so a new type is additive rather than another branch in
	// buildCreateObjectBody.
	bodyBuilder func(opts CreateObjectOptions, typeInfo objectTypeInfo, responsible string) string
}

var objectTypes = map[CreatableObjectType]objectTypeInfo{
	ObjectTypeProgram: {
		creationPath: "/sap/bc/adt/programs/programs",
		rootName:     "program:abapProgram",
		namespace:    `xmlns:program="http://www.sap.com/adt/programs/programs"`,
	},
	ObjectTypeInclude: {
		creationPath: "/sap/bc/adt/programs/includes",
		rootName:     "include:abapInclude",
		namespace:    `xmlns:include="http://www.sap.com/adt/programs/includes"`,
	},
	ObjectTypeClass: {
		creationPath: "/sap/bc/adt/oo/classes",
		rootName:     "class:abapClass",
		namespace:    `xmlns:class="http://www.sap.com/adt/oo/classes"`,
	},
	ObjectTypeInterface: {
		creationPath: "/sap/bc/adt/oo/interfaces",
		rootName:     "intf:abapInterface",
		namespace:    `xmlns:intf="http://www.sap.com/adt/oo/interfaces"`,
	},
	ObjectTypeFunctionGroup: {
		creationPath: "/sap/bc/adt/functions/groups",
		rootName:     "group:abapFunctionGroup",
		namespace:    `xmlns:group="http://www.sap.com/adt/functions/groups"`,
	},
	ObjectTypeFunctionMod: {
		creationPath: "/sap/bc/adt/functions/groups/%s/fmodules",
		rootName:     "fmodule:abapFunctionModule",
		namespace:    `xmlns:fmodule="http://www.sap.com/adt/functions/fmodules"`,
	},
	ObjectTypeMessageClass: {
		creationPath: "/sap/bc/adt/messageclass",
		rootName:     "mc:messageClass",
		namespace:    `xmlns:mc="http://www.sap.com/adt/MessageClass"`,
		bodyBuilder:  messageClassCreateBody,
	},
	ObjectTypePackage: {
		creationPath: "/sap/bc/adt/packages",
		rootName:     "pack:package",
		namespace:    `xmlns:pack="http://www.sap.com/adt/packages"`,
	},
	// RAP object types
	ObjectTypeDDLS: {
		creationPath: "/sap/bc/adt/ddic/ddl/sources",
		rootName:     "ddl:ddlSource",
		namespace:    `xmlns:ddl="http://www.sap.com/adt/ddic/ddlsources"`,
	},
	ObjectTypeBDEF: {
		creationPath: "/sap/bc/adt/bo/behaviordefinitions",
		rootName:     "bdef:behaviorDefinition",
		namespace:    `xmlns:bdef="http://www.sap.com/adt/bo/behaviordefinitions"`,
	},
	ObjectTypeSRVD: {
		creationPath: "/sap/bc/adt/ddic/srvd/sources",
		rootName:     "srvd:srvdSource",
		namespace:    `xmlns:srvd="http://www.sap.com/adt/ddic/srvdsources"`,
	},
	ObjectTypeSRVB: {
		creationPath: "/sap/bc/adt/businessservices/bindings",
		rootName:     "srvb:serviceBinding",
		namespace:    `xmlns:srvb="http://www.sap.com/adt/ddic/ServiceBindings"`,
	},
}

// CreateObject creates a new ABAP object.
// IMPORTANT: This function validates package existence BEFORE calling SAP ADT CreateObject API.
// This prevents orphan ENQUEUE locks that SAP creates internally during CreateObject
// before validating the request. These orphan locks can only be cleared via SM12.
func (c *Client) CreateObject(ctx context.Context, opts CreateObjectOptions) error {
	typeInfo, ok := objectTypes[opts.ObjectType]
	if !ok {
		return fmt.Errorf("unsupported object type: %s", opts.ObjectType)
	}

	opts.Name = strings.ToUpper(opts.Name)
	opts.PackageName = strings.ToUpper(opts.PackageName)

	// For package creation, check the package being created (opts.Name), not the parent (opts.PackageName)
	packageToCheck := opts.PackageName
	if opts.ObjectType == ObjectTypePackage {
		packageToCheck = opts.Name
	}

	// Unified mutation policy gate (op type + package + transport)
	if err := c.checkMutation(ctx, MutationContext{
		Op:        OpCreate,
		OpName:    "CreateObject",
		Package:   packageToCheck,
		Transport: opts.Transport,
	}); err != nil {
		return err
	}

	// A transportable object with no request named: pick one the way the
	// editor would, rather than let SAP generate a request per write.
	if opts.Transport == "" && opts.ObjectType != ObjectTypePackage && opts.PackageName != "" && !strings.HasPrefix(opts.PackageName, "$") && c.config.Safety.TransportChoice != "off" {
		if objectURL, uerr := c.buildObjectURLWithParent(opts.ObjectType, opts.Name, opts.ParentName); uerr == nil {
			choice := c.planTransport(ctx, "", objectURL, opts.PackageName)
			if choice.Err != nil {
				return choice.Err
			}
			if choice.Transport != "" {
				if err := c.checkTransportableEdit(choice.Transport, "CreateObject"); err != nil {
					return err
				}
				opts.Transport = choice.Transport
			}
			if opts.Chosen != nil {
				*opts.Chosen = *choice
			}
		}
	}

	// Package creation validation: local packages always allowed, transportable requires opt-in
	if opts.ObjectType == ObjectTypePackage && !strings.HasPrefix(opts.Name, "$") {
		// Transportable package - check if transports are enabled
		if !c.config.Safety.EnableTransports && !c.config.Safety.AllowTransportableEdits {
			return fmt.Errorf("creating transportable packages requires --enable-transports or --allow-transportable-edits flag. Package: %s", opts.Name)
		}
		// Transportable package requires a transport request
		if opts.Transport == "" {
			return fmt.Errorf("transport request is required for creating transportable package %s", opts.Name)
		}
	}

	// CRITICAL: Validate package exists BEFORE calling SAP ADT CreateObject API.
	// SAP ADT creates ENQUEUE locks internally BEFORE validating the request.
	// If package doesn't exist, SAP fails but leaves the lock orphaned.
	// These orphan locks can only be cleared via SM12 transaction.
	// By checking first, we prevent this scenario entirely.
	if opts.ObjectType != ObjectTypePackage && opts.PackageName != "" {
		if !c.packageExists(ctx, opts.PackageName) {
			return fmt.Errorf("package %s does not exist - create it first to avoid orphan locks", opts.PackageName)
		}
	}

	// Build creation URL
	creationURL := creationURLFor(opts, typeInfo)

	// Build request body with current user as default responsible
	defaultResponsible := c.config.Username
	if defaultResponsible == "" {
		defaultResponsible = "DDIC" // Fallback to standard development user
	}
	body := buildCreateObjectBody(opts, typeInfo, defaultResponsible)

	params := url.Values{}
	if opts.Transport != "" {
		params.Set("corrNr", opts.Transport)
	}

	// BDEF requires specific content type
	contentType := "application/*"
	if opts.ObjectType == ObjectTypeBDEF {
		contentType = "application/vnd.sap.adt.blues.v1+xml"
	}
	if opts.ObjectType == ObjectTypeMessageClass {
		contentType = "application/vnd.sap.adt.mc.messageclass+xml"
		if opts.MasterLanguage == "" {
			opts.MasterLanguage = c.config.Language
		}
		body = buildCreateObjectBody(opts, typeInfo, defaultResponsible)
	}

	// First attempt
	_, err := c.transport.Request(ctx, creationURL, &RequestOptions{
		Method:      http.MethodPost,
		Query:       params,
		Body:        []byte(body),
		ContentType: contentType,
	})

	// If we hit a lock conflict, try to clean up orphan lock and retry once
	if isLockConflictError(err) {
		// Get the object URL for lock cleanup
		objectURL := GetObjectURL(opts.ObjectType, opts.Name, opts.ParentName)
		if objectURL != "" {
			c.tryCleanupOrphanLock(ctx, objectURL)

			// Retry creation
			_, err = c.transport.Request(ctx, creationURL, &RequestOptions{
				Method:      http.MethodPost,
				Query:       params,
				Body:        []byte(body),
				ContentType: contentType,
			})
		}
	}

	if err != nil {
		// reconcileFailedCreate probes whether SAP persisted the object
		// before the request failed and runs best-effort compensating
		// cleanup if so. On a clean pre-persistence failure it returns
		// the original error unchanged; on partial persistence it
		// returns a *PartialCreateError wrapping the original error and
		// describing what cleanup was attempted.
		recErr := c.reconcileFailedCreate(ctx, opts, fmt.Errorf("creating object: %w", err))
		return recErr
	}

	return nil
}

// creationURLFor is the collection a new object is POSTed to. A function
// module's is inside its group, and a namespaced group ("/NS/GROUP") has to be
// escaped like every other object URL, or the POST goes to
// .../groups//NS/GROUP/fmodules and comes back 404.
func creationURLFor(opts CreateObjectOptions, typeInfo objectTypeInfo) string {
	if opts.ObjectType == ObjectTypeFunctionMod && opts.ParentName != "" {
		return fmt.Sprintf(typeInfo.creationPath, url.PathEscape(strings.ToLower(opts.ParentName)))
	}
	return typeInfo.creationPath
}

func buildCreateObjectBody(opts CreateObjectOptions, typeInfo objectTypeInfo, defaultResponsible string) string {
	responsible := opts.Responsible
	if responsible == "" {
		responsible = defaultResponsible
	}

	// A type that registered its own builder owns its whole payload.
	if typeInfo.bodyBuilder != nil {
		return typeInfo.bodyBuilder(opts, typeInfo, responsible)
	}

	// For packages, use special structure with attributes element
	// Local packages use "LOCAL" software component, transportable packages need explicit component
	if opts.ObjectType == ObjectTypePackage {
		// Determine software component based on package type
		softwareComponent := "LOCAL"
		transportLayer := ""
		if !strings.HasPrefix(opts.Name, "$") {
			// Transportable package - use provided software component or empty
			// SAP requires explicit software component for transportable packages
			softwareComponent = opts.SoftwareComponent
		}
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<%s %s xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:description="%s"
  adtcore:name="%s"
  adtcore:type="%s"
  adtcore:responsible="%s">
  <pack:attributes pack:packageType="development"/>
  <pack:superPackage adtcore:name="%s" adtcore:type="DEVC/K"/>
  <pack:applicationComponent/>
  <pack:transport>
    <pack:softwareComponent pack:name="%s"/>
    <pack:transportLayer pack:name="%s"/>
  </pack:transport>
  <pack:translation/>
  <pack:useAccesses/>
  <pack:packageInterfaces/>
  <pack:subPackages/>
</%s>`,
			typeInfo.rootName, typeInfo.namespace,
			escapeXML(opts.Description),
			opts.Name,
			opts.ObjectType,
			responsible,
			opts.PackageName,
			softwareComponent,
			transportLayer,
			typeInfo.rootName)
	}

	// For function modules, reference the function group
	if opts.ObjectType == ObjectTypeFunctionMod {
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<%s %s xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:description="%s"
  adtcore:name="%s"
  adtcore:type="%s"
  adtcore:responsible="%s">
  <adtcore:containerRef adtcore:name="%s" adtcore:type="FUGR/F"
    adtcore:uri="/sap/bc/adt/functions/groups/%s"/>
</%s>`,
			typeInfo.rootName, typeInfo.namespace,
			escapeXML(opts.Description),
			opts.Name,
			opts.ObjectType,
			responsible,
			strings.ToUpper(opts.ParentName),
			url.PathEscape(strings.ToLower(opts.ParentName)),
			typeInfo.rootName)
	}

	// For SRVD (Service Definition), include the srvdSourceType attribute
	if opts.ObjectType == ObjectTypeSRVD {
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<%s %s xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:description="%s"
  adtcore:name="%s"
  adtcore:type="%s"
  adtcore:responsible="%s"
  srvd:srvdSourceType="S">
  <adtcore:packageRef adtcore:name="%s"/>
</%s>`,
			typeInfo.rootName, typeInfo.namespace,
			escapeXML(opts.Description),
			opts.Name,
			opts.ObjectType,
			responsible,
			opts.PackageName,
			typeInfo.rootName)
	}

	// For SRVB (Service Binding), include service definition and binding info
	if opts.ObjectType == ObjectTypeSRVB {
		bindingType := opts.BindingType
		if bindingType == "" {
			bindingType = "ODATA"
		}
		bindingVersion := opts.BindingVersion
		if bindingVersion == "" {
			bindingVersion = "V2"
		}
		bindingCategory := opts.BindingCategory
		if bindingCategory == "" {
			bindingCategory = "0" // UI (SRVB_BND_CATEGORY: 0=UI, 1=A2X/Web API)
		}
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<%s %s xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:description="%s"
  adtcore:name="%s"
  adtcore:type="%s"
  adtcore:responsible="%s">
  <adtcore:packageRef adtcore:name="%s"/>
  <srvb:services srvb:name="%s">
    <srvb:content srvb:version="0001">
      <srvb:serviceDefinition adtcore:name="%s"/>
    </srvb:content>
  </srvb:services>
  <srvb:binding srvb:category="%s" srvb:type="%s" srvb:version="%s">
    <srvb:implementation adtcore:name=""/>
  </srvb:binding>
</%s>`,
			typeInfo.rootName, typeInfo.namespace,
			escapeXML(opts.Description),
			opts.Name,
			opts.ObjectType,
			responsible,
			opts.PackageName,
			opts.Name,
			strings.ToUpper(opts.ServiceDefinition),
			bindingCategory,
			bindingType,
			bindingVersion,
			typeInfo.rootName)
	}

	// For BDEF (Behavior Definition), use blue:blueSource as root element
	// ADT API expects this specific format (discovered from existing BDEFs)
	if opts.ObjectType == ObjectTypeBDEF {
		// BDEF creation uses blue:blueSource as root, source is set separately via PUT
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<blue:blueSource xmlns:blue="http://www.sap.com/wbobj/blue" xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:description="%s"
  adtcore:name="%s"
  adtcore:type="%s"
  adtcore:responsible="%s">
  <adtcore:packageRef adtcore:name="%s"/>
</blue:blueSource>`,
			escapeXML(opts.Description),
			opts.Name,
			opts.ObjectType,
			responsible,
			opts.PackageName)
	}

	// Standard object creation (DDLS uses standard body)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<%s %s xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:description="%s"
  adtcore:name="%s"
  adtcore:type="%s"
  adtcore:responsible="%s">
  <adtcore:packageRef adtcore:name="%s"/>
</%s>`,
		typeInfo.rootName, typeInfo.namespace,
		escapeXML(opts.Description),
		opts.Name,
		opts.ObjectType,
		responsible,
		opts.PackageName,
		typeInfo.rootName)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
