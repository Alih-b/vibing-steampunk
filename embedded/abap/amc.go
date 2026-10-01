package embedded

// AMC application ZVSP_TRANSPORT: how the background job that adds an
// uploaded request to the import buffer reports back. The job
// (ZCL_VSP_TRANSPORT_SERVICE=>run_job) sends one TEXT message on channel
// /buffer, with the uploading session's id as the channel extension; the APC
// handler binds each WebSocket to its own extension, so the message reaches
// that client and no other. vsp install creates and activates it; without it
// the upload still works and its outcome is read with the status call.
//
// This is the definition as ADT edits it (source/main of the SAMC object),
// not an abapGit file: deploying src/ through abapGit needs the application
// created in transaction SAMC with these values.

// AMCApplicationName is the AMC application of the transport push.
const AMCApplicationName = "ZVSP_TRANSPORT"

// AMCApplicationDescription is its short text.
const AMCApplicationDescription = "VSP transport upload result push"

// AMCApplicationDefinition: channel /buffer, client scope, TEXT messages;
// the transport service may send, the APC handler may bind a WebSocket.
const AMCApplicationDefinition = `<?xml version="1.0" encoding="utf-8"?>` +
	`<asx:abap xmlns:asx="http://www.sap.com/uc_object_type_group/samc/abapxml"><CONTENT><AMC_COMPLETE>` +
	`<AMC_APPL>ZVSP_TRANSPORT</AMC_APPL><CHANNEL_ATTR><AMC_ADTCHANNELATTR>` +
	`<CHANNEL_ID>/buffer</CHANNEL_ID><SCOPE>C</SCOPE><MESSAGE_TYPE_ID>TEXT</MESSAGE_TYPE_ID><VSI_PROFILE_SEND/>` +
	`<AUTHORITIES>` +
	`<AMC_ADTCONTENTAUTHORITIES><NR>1</NR><OBJ_TYPE>CLAS</OBJ_TYPE><OBJ_NAME>ZCL_VSP_TRANSPORT_SERVICE</OBJ_NAME><ACTIVITY>S</ACTIVITY></AMC_ADTCONTENTAUTHORITIES>` +
	`<AMC_ADTCONTENTAUTHORITIES><NR>2</NR><OBJ_TYPE>CLAS</OBJ_TYPE><OBJ_NAME>ZCL_VSP_APC_HANDLER</OBJ_NAME><ACTIVITY>C</ACTIVITY></AMC_ADTCONTENTAUTHORITIES>` +
	`</AUTHORITIES></AMC_ADTCHANNELATTR></CHANNEL_ATTR></AMC_COMPLETE></CONTENT></asx:abap>`

// AMC application ZVSP_GIT: how the background job that imports an abapGit
// zip (ZCL_VSP_GIT_SERVICE=>run_job) reports back, one TEXT message on
// channel /import with the importing session's id as the extension. It is
// separate from ZVSP_TRANSPORT because it names the git service, which exists
// only where abapGit does: vsp install creates it only with the git service.

// AMCGitApplicationName is the AMC application of the git import push.
const AMCGitApplicationName = "ZVSP_GIT"

// AMCGitApplicationDescription is its short text.
const AMCGitApplicationDescription = "VSP abapGit import result push"

// AMCGitApplicationDefinition: channel /import, client scope, TEXT messages;
// the git service may send, the APC handler may bind a WebSocket.
const AMCGitApplicationDefinition = `<?xml version="1.0" encoding="utf-8"?>` +
	`<asx:abap xmlns:asx="http://www.sap.com/uc_object_type_group/samc/abapxml"><CONTENT><AMC_COMPLETE>` +
	`<AMC_APPL>ZVSP_GIT</AMC_APPL><CHANNEL_ATTR><AMC_ADTCHANNELATTR>` +
	`<CHANNEL_ID>/import</CHANNEL_ID><SCOPE>C</SCOPE><MESSAGE_TYPE_ID>TEXT</MESSAGE_TYPE_ID><VSI_PROFILE_SEND/>` +
	`<AUTHORITIES>` +
	`<AMC_ADTCONTENTAUTHORITIES><NR>1</NR><OBJ_TYPE>CLAS</OBJ_TYPE><OBJ_NAME>ZCL_VSP_GIT_SERVICE</OBJ_NAME><ACTIVITY>S</ACTIVITY></AMC_ADTCONTENTAUTHORITIES>` +
	`<AMC_ADTCONTENTAUTHORITIES><NR>2</NR><OBJ_TYPE>CLAS</OBJ_TYPE><OBJ_NAME>ZCL_VSP_APC_HANDLER</OBJ_NAME><ACTIVITY>C</ACTIVITY></AMC_ADTCONTENTAUTHORITIES>` +
	`</AUTHORITIES></AMC_ADTCHANNELATTR></CHANNEL_ATTR></AMC_COMPLETE></CONTENT></asx:abap>`
