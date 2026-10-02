// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_crud.go registers lock, create, update, delete and clone tools.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// registerCRUDTools registers CRUD operations (lock, create, update, delete).
func (s *Server) registerCRUDTools(shouldRegister func(string) bool) {
	if shouldRegister("LockObject") {
		s.mcpServer.AddTool(mcp.NewTool("LockObject",
			mcp.WithDescription("Acquire an edit lock on an ABAP object"),
			mcp.WithString("object_url",
				mcp.Required(),
				mcp.Description("ADT URL of the object (e.g., /sap/bc/adt/programs/programs/ZTEST)"),
			),
			mcp.WithString("access_mode",
				mcp.Description("Access mode: MODIFY (default) or READ"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request (corrNr) for an object in a transportable "+
					"package. SAP wants it on the LOCK, not only on the write that follows."),
			),
		), s.handleLockObject)
	}

	if shouldRegister("UnlockObject") {
		s.mcpServer.AddTool(mcp.NewTool("UnlockObject",
			mcp.WithDescription("Release an edit lock on an ABAP object"),
			mcp.WithString("object_url",
				mcp.Required(),
				mcp.Description("ADT URL of the object (e.g., /sap/bc/adt/programs/programs/ZTEST)"),
			),
			mcp.WithString("lock_handle",
				mcp.Required(),
				mcp.Description("Lock handle from LockObject"),
			),
		), s.handleUnlockObject)
	}

	if shouldRegister("UpdateSource") {
		s.mcpServer.AddTool(mcp.NewTool("UpdateSource",
			mcp.WithDescription("Write source code to an ABAP object (requires lock)"),
			mcp.WithString("object_url",
				mcp.Required(),
				mcp.Description("ADT URL of the object (e.g., /sap/bc/adt/programs/programs/ZTEST)"),
			),
			mcp.WithString("source",
				mcp.Required(),
				mcp.Description("ABAP source code to write"),
			),
			mcp.WithString("lock_handle",
				mcp.Description("Optional lock handle. Omit it and this call takes and releases its own lock (#169)."),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (optional for local packages)"),
			),
		), s.handleUpdateSource)
	}

	if shouldRegister("CreateObject") {
		s.mcpServer.AddTool(mcp.NewTool("CreateObject",
			mcp.WithDescription("Create a new ABAP object. Supports: PROG/P (program), CLAS/OC (class), INTF/OI (interface), PROG/I (include), FUGR/F (function group), FUGR/FF (function module), DEVC/K (package), DDLS/DF (CDS view), BDEF/BDO (behavior definition), SRVD/SRV (service definition), SRVB/SVB (service binding), MSAG/N (message class)"),
			mcp.WithString("object_type",
				mcp.Required(),
				mcp.Description("Object type: PROG/P, CLAS/OC, INTF/OI, PROG/I, FUGR/F, FUGR/FF, DEVC/K, DDLS/DF, BDEF/BDO, SRVD/SRV, SRVB/SVB, MSAG/N"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Object name (e.g., ZTEST_PROGRAM)"),
			),
			mcp.WithString("description",
				mcp.Required(),
				mcp.Description("Object description"),
			),
			mcp.WithString("package_name",
				mcp.Required(),
				mcp.Description("Package name (e.g., $TMP for local, ZPACKAGE for transportable)"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (required for non-local packages)"),
			),
			mcp.WithString("parent_name",
				mcp.Description("Parent name (required for function modules - the function group name)"),
			),
			// Function module options (FUGR/FF)
			mcp.WithBoolean("rfc_enabled",
				mcp.Description("For FUGR/FF: make the module remote-enabled (RFC). Remote-enabled modules must pass every parameter by value"),
			),
			mcp.WithString("source",
				mcp.Description("For FUGR/FF: full module source including the signature (FUNCTION ... IMPORTING VALUE(iv_x) TYPE i ... ENDFUNCTION.) - a function module's interface lives in its source"),
			),
			// Message class options (MSAG/N)
			mcp.WithString("language",
				mcp.Description("For MSAG/N: original language of the class and its messages (ISO, e.g. EN, DE); defaults to the session language"),
			),
			mcp.WithObject("messages",
				mcp.Description("For MSAG/N: initial messages as {\"001\": \"text\", ...}"),
			),
			// RAP-specific options
			mcp.WithString("service_definition",
				mcp.Description("For SRVB: the service definition name to bind"),
			),
			mcp.WithString("binding_version",
				mcp.Description("For SRVB: OData version 'V2' or 'V4'. Defaults to 'V2' - pass 'V4' explicitly for Fiori Elements V4 apps, otherwise a V2 binding is created silently."),
			),
			mcp.WithString("binding_category",
				mcp.Description("For SRVB: '0' = UI (User Interface), '1' = A2X (Web API). Default: '0' (UI). Values follow SAP domain SRVB_BND_CATEGORY."),
			),
		), s.handleCreateObject)
	}

	if shouldRegister("CreatePackage") {
		s.mcpServer.AddTool(mcp.NewTool("CreatePackage",
			mcp.WithDescription("Create a new ABAP package. Local packages ($*) work by default. Transportable packages require --enable-transports flag and transport parameter."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Package name (e.g., $ZTEST for local, ZPRODUCTION for transportable)"),
			),
			mcp.WithString("description",
				mcp.Required(),
				mcp.Description("Package description"),
			),
			mcp.WithString("parent",
				mcp.Description("Parent package name (optional, e.g., $TMP, ZPROD). If not specified, creates a root-level package."),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (required for transportable packages, e.g., 'A4HK900114')"),
			),
			mcp.WithString("software_component",
				mcp.Description("Software component name (required for transportable packages, e.g., 'HOME', 'ZLOCAL'). Use GetInstalledComponents to list available components."),
			),
		), s.handleCreatePackage)
	}

	if shouldRegister("CreateTable") {
		s.mcpServer.AddTool(mcp.NewTool("CreateTable",
			mcp.WithDescription("Create a DDIC transparent table from a simple JSON definition. Handles full workflow: create → set source → activate. Supports common ABAP types: CHAR, NUMC, INT4, DEC, STRING, TIMESTAMPL, UUID, etc."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Table name (uppercase, max 30 chars, must start with Z/Y)"),
			),
			mcp.WithString("description",
				mcp.Required(),
				mcp.Description("Short description of the table"),
			),
			mcp.WithString("package",
				mcp.Description("Target package (default: $TMP)"),
			),
			mcp.WithString("fields",
				mcp.Required(),
				mcp.Description("JSON array of fields: [{\"name\":\"ID\",\"type\":\"CHAR32\",\"key\":true},{\"name\":\"VALUE\",\"type\":\"STRING\",\"notNull\":true}]. Attributes: name, type, length, decimals, description, key, notNull; any other attribute is refused. Attribute names are case-insensitive. Types: CHAR/CHARnn, NUMC/NUMCnn, INT4, DEC, STRING, TIMESTAMPL, UUID, DATS, TIMS, MANDT/CLIENT/CLNT/abap.clnt (client types), or data element name. Client field: a first key field of client type (MANDT, CLIENT, CLNT or abap.clnt) is the client field; without one, a 'key client : abap.clnt' field is put in front (see client_dependent). Non-key client-typed data columns (e.g. SRC_CLIENT) are allowed; a client-typed key field after the first is refused."),
			),
			mcp.WithBoolean("client_dependent",
				mcp.Description("Left out: add a key field CLIENT (abap.clnt) in front, unless the first key field has client type MANDT/CLIENT/CLNT/abap.clnt. A first key field NAMED MANDT or CLIENT typed with another data element (e.g. SYMANDT, ZMANDT) is refused unless this is true, which uses that field as the client field as is (false is refused: SAP may see a client field there). Named MANDT/CLIENT with a built-in type (CHAR3, INT4) is refused unless this is false, since SAP would make the table client-independent. true: client-dependent, same rules. false: client-independent table, no client field is added."),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (optional for $TMP)"),
			),
			mcp.WithString("delivery_class",
				mcp.Description("Delivery class: A=Application (default), C=Customizing, L=Temporary"),
			),
		), s.handleCreateTable)
	}

	if shouldRegister("CompareSource") {
		s.mcpServer.AddTool(mcp.NewTool("CompareSource",
			mcp.WithDescription("Compare source code of two objects and return unified diff. Supports all object types from GetSource."),
			mcp.WithString("type1",
				mcp.Required(),
				mcp.Description("Object type of first object: PROG, CLAS, INTF, FUNC, FUGR, INCL, DDLS, BDEF, SRVD"),
			),
			mcp.WithString("name1",
				mcp.Required(),
				mcp.Description("Name of first object"),
			),
			mcp.WithString("type2",
				mcp.Required(),
				mcp.Description("Object type of second object (can be same or different)"),
			),
			mcp.WithString("name2",
				mcp.Required(),
				mcp.Description("Name of second object"),
			),
			mcp.WithString("include1",
				mcp.Description("Class include type for first object if CLAS: definitions, implementations, macros, testclasses"),
			),
			mcp.WithString("include2",
				mcp.Description("Class include type for second object if CLAS"),
			),
			mcp.WithString("parent1",
				mcp.Description("Function group for first object if FUNC"),
			),
			mcp.WithString("parent2",
				mcp.Description("Function group for second object if FUNC"),
			),
		), s.handleCompareSource)
	}

	if shouldRegister("CloneObject") {
		s.mcpServer.AddTool(mcp.NewTool("CloneObject",
			mcp.WithDescription("Copy an ABAP object to a new name. Replaces object name in source. Supports PROG, CLAS, INTF."),
			mcp.WithString("object_type",
				mcp.Required(),
				mcp.Description("Object type: PROG, CLAS, INTF"),
			),
			mcp.WithString("source_name",
				mcp.Required(),
				mcp.Description("Name of object to copy"),
			),
			mcp.WithString("target_name",
				mcp.Required(),
				mcp.Description("Name for the new object"),
			),
			mcp.WithString("package",
				mcp.Required(),
				mcp.Description("Target package (e.g., $TMP)"),
			),
		), s.handleCloneObject)
	}

	if shouldRegister("GetClassInfo") {
		s.mcpServer.AddTool(mcp.NewTool("GetClassInfo",
			mcp.WithDescription("Get class metadata without full source: methods, attributes, interfaces, superclass, abstract/final status."),
			mcp.WithString("class_name",
				mcp.Required(),
				mcp.Description("Name of the ABAP class"),
			),
		), s.handleGetClassInfo)
	}

	if shouldRegister("DeleteObject") {
		s.mcpServer.AddTool(mcp.NewTool("DeleteObject",
			mcp.WithDescription("Delete an ABAP object (requires lock)"),
			mcp.WithString("object_url",
				mcp.Required(),
				mcp.Description("ADT URL of the object (e.g., /sap/bc/adt/programs/programs/ZTEST)"),
			),
			mcp.WithString("lock_handle",
				mcp.Description("Optional lock handle. Omit it and this call takes and releases its own lock (#169)."),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (optional for local packages)"),
			),
		), s.handleDeleteObject)
	}

	if shouldRegister("RecoverFailedCreate") {
		s.mcpServer.AddTool(mcp.NewTool("RecoverFailedCreate",
			mcp.WithDescription("Recover a zombie object left behind by an earlier failed CreateObject. "+
				"Probes whether SAP currently persists the object; if yes, runs best-effort compensating "+
				"cleanup (lock release, fresh-session lock acquisition, DeleteObject); if no, returns an "+
				"idempotent no-op. Does NOT require a lock_handle from the original session — the handler "+
				"acquires its own. Returns a structured report with status, attempted cleanup actions, "+
				"and residual manual steps if cleanup could not finish. "+
				"Use this when a CreateObject call returned a 5xx error and you cannot edit / delete the "+
				"object through normal MCP flows because the old lock handle is gone."),
			mcp.WithString("object_type",
				mcp.Required(),
				mcp.Description("ABAP object type (CLAS, PROG, INTF, FUGR, DDLS, FUNC, ...)"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Object name"),
			),
			mcp.WithString("package_name",
				mcp.Required(),
				mcp.Description("Package — used by the safety gate; must be in the configured allowed list"),
			),
			mcp.WithString("parent_name",
				mcp.Description("Parent function group name (required for FUNC / LIMU FUNC recovery)"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request the zombie object was attached to, if any"),
			),
		), s.handleRecoverFailedCreate)
	}

	// Transport-related tools
	if shouldRegister("GetUserTransports") {
		s.mcpServer.AddTool(mcp.NewTool("GetUserTransports", append([]mcp.ToolOption{
			mcp.WithDescription("Get the transport requests of a user from the transport organizer (requires --enable-transports): workbench and customizing, modifiable and released, with tasks and objects, grouped by target and CTS project. Reads GET /sap/bc/adt/cts/transportrequests with explicit requestType/requestStatus (the organizer returns only released requests without them), falls back to the saved search configuration and then to E070/E07T."),
			mcp.WithString("user_name",
				mcp.Description("SAP user name (uppercased); default: the connection user. '*' for every user (source sql only)"),
			),
		}, transportListingParams...)...,
		), s.handleGetUserTransports)
	}

	if shouldRegister("GetTransportInfo") {
		s.mcpServer.AddTool(mcp.NewTool("GetTransportInfo",
			mcp.WithDescription("Get transport information for an ABAP object (requires --enable-transports flag). Returns available transports and lock status."),
			mcp.WithString("object_url",
				mcp.Required(),
				mcp.Description("ADT URL of the object (e.g., /sap/bc/adt/programs/programs/ZTEST)"),
			),
			mcp.WithString("dev_class",
				mcp.Required(),
				mcp.Description("Development class/package of the object"),
			),
		), s.handleGetTransportInfo)
	}
}
