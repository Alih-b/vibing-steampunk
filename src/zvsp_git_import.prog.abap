*&---------------------------------------------------------------------*
*& Report ZVSP_GIT_IMPORT
*&---------------------------------------------------------------------*
*& The background step of ZCL_VSP_GIT_SERVICE's import_zip. abapGit's
*& deserialize activates and may run for minutes, which an ABAP Push
*& Channel is not the place for, so the service schedules this report as
*& job ZVSP_GIT_IMPORT with a protected variant naming the package and the
*& SHA-256 (base64) of the zip and of the import's parameters, which wait in
*& INDX(ZV) under the job's number. Run outside that job it does nothing; in
*& it, it imports only if both still have those SHA-256 values, stores the
*& result for import_status, and publishes the outcome on AMC ZVSP_GIT
*& /import for the WebSocket that started it (P_PUSH).
*&---------------------------------------------------------------------*
REPORT zvsp_git_import.

PARAMETERS: p_pkg  TYPE devclass,
            p_shz  TYPE c LENGTH 44 LOWER CASE,
            p_shm  TYPE c LENGTH 44 LOWER CASE,
            p_push TYPE c LENGTH 60 LOWER CASE.

START-OF-SELECTION.
  zcl_vsp_git_service=>run_job( iv_package = p_pkg iv_zip_sha = p_shz iv_meta_sha = p_shm iv_push_id = p_push ).
