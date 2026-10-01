*&---------------------------------------------------------------------*
*& Report ZVSP_TRANSPORT_BUFFER
*&---------------------------------------------------------------------*
*& The background step of ZCL_VSP_TRANSPORT_SERVICE. tp is started over
*& synchronous RFC, which an ABAP Push Channel may not do, so the service
*& schedules this report as job ZVSP_TRANSPORT_BUFFER, with a protected
*& variant holding the request and the SHA-256 (base64) of its two files.
*& Run outside that job it does nothing; in it, it adds the request only if
*& both files still have those SHA-256 values, and publishes the outcome on
*& AMC ZVSP_TRANSPORT /buffer for the uploading WebSocket (P_PUSH).
*&---------------------------------------------------------------------*
REPORT zvsp_transport_buffer.

PARAMETERS: p_req  TYPE trkorr,
            p_shac TYPE c LENGTH 44 LOWER CASE,
            p_shad TYPE c LENGTH 44 LOWER CASE,
            p_push TYPE c LENGTH 60 LOWER CASE.

START-OF-SELECTION.
  zcl_vsp_transport_service=>run_job( iv_request = p_req iv_cofile_sha = p_shac iv_data_sha = p_shad iv_push_id = p_push ).
