# Complexity and modularity map of vsp

**Date:** 2026-10-02 · **Tree:** `origin/main` @ `ca6912b` (detached worktree, read-only) · **Churn window:** last 90 days, 549 commits (401 non-merge, 281 touching `.go`)

**Goal:** lower the mental load and the token load of reading this code, for humans and for AI agents. This report measures where that load sits today. It then ranks the splits by **load saved per unit of effort**, not by complexity alone.

**Tools:** `gocyclo` and `gocognit` (latest), a small `go/ast` walker for lines, functions, function lengths and exported identifiers, `go list -json`, and `git log --since=90.days --name-only`. Token cost is estimated as bytes/4.

**Exclusions:** the research packages (`pkg/llvm2abap`, `pkg/wasmcomp`, `pkg/ts2abap`, `pkg/ts2go`, `pkg/jseval`) get their own section (section 7). No Go file in the tree carries a `Code generated … DO NOT EDIT` header. The only generator is `embedded/abap/sync_from_src.go` (`//go:build ignore`). It copies ABAP sources and produces no Go, so the only thing excluded as generated is that file itself. Unless a table says otherwise, its numbers are for non-test code.

---

## 0. Summary

- **Eleven files carry 51% of the whole-file read load of all 281 Go-touching commits** in the last 90 days, and 43% of those commits touch at least one of them. These files are `devops.go`, `client.go`, `tools_register.go`, `cli_extra.go`, `crud.go`, `handlers_help.go`, `workflows_source.go`, `handlers_graph.go`, `main.go`, `readonly_invariant_test.go` and `http.go`.
- **Adding an MCP tool costs about 72k tokens** of whole-file reading today. About 50k of that comes from three files that the agent only needs one slice of: `tools_register.go` (27k), `readonly_invariant_test.go` (21.7k) and `handlers_help.go` (12.5k). After three mechanical S-sized splits it costs about 18k.
- **`handlers_universal.go` is not a god file any more.** It is 226 lines and 2.1k tokens, and it dispatches through a list of 31 `route*Action` functions that already live in their domain files. Do not split it. The assumption is not confirmed by the data.
- **`handlers_help.go` is the most frequently changed Go file in the repo.** It sits in 40 commits, 14% of all Go commits, and in 35% of commits that touch `internal/mcp`. It is a single 695-line `switch` of string literals. Extracting it is the cheapest win per token.
- **`cmd/vsp` and `internal/mcp` duplicate the graph, boundary and health logic.** At least 200 lines are verbatim (10-line windows) in `devops.go` and `cli_extra.go` versus `handlers_graph.go`, `handlers_health.go` and `handlers_transport_analysis.go`. For example, `resolveFMviaTFDIRcli` and `resolveFMviaTFDIR` are near-identical. 60 commits in the window touched both trees. This is the seam CLAUDE.md already names: "SQL/ADT adapters pending, unify `cli_deps.go` + `cli_extra.go`".
- **The architecture is clean.** There are no import cycles and no upward imports (`pkg/*` never imports `internal/*` or `cmd/*`). The only smell is that `cmd/vsp/cli_compile.go` links three research packages into the production binary.
- **Simulated effect of the top 10 splits on the 90-day history:** per-commit whole-file read load drops from median 16.3k / mean 26.6k / p90 59.8k tokens to **12.7k / 17.3k / 37.3k**, and the total falls by 35%. The simulation is conservative: each god file is replaced by the *largest* file that replaces it.

---

## 1. Per package

The table covers non-research packages, ordered by non-test lines. "Exported" counts exported top-level funcs/methods, types, vars and consts. Fan-in counts internal packages that import this one, from non-test imports; the number after "+" counts additional packages whose tests import it. Fan-out is internal packages imported.

| Package | Go lines | Test lines | Files (+test) | Tokens (non-test) | Exported | Fan-in | Fan-out | Churn (file-commits) |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| `pkg/adt` | 44,771 | 30,214 | 117 (+139) | 388k | **1,208** | 9 (+6) | 4 | 475 |
| `cmd/vsp` | 25,680 | 4,966 | 55 (+34) | 213k | 19 | 0 | **19** | 253 |
| `internal/mcp` | 22,235 | 8,423 | 67 (+54) | 210k | 43 | 1 (+1) | 10 | 355 |
| `pkg/graph` | 6,296 | 5,002 | 24 (+27) | 49k | 196 | 3 (+2) | 1 | 18 |
| `pkg/saprfc` | 5,699 | 1,821 | 20 (+11) | 49k | 193 | 3 (+3) | 1 | 91 |
| `pkg/dsl` | 2,753 | 1,118 | 6 (+2) | 18k | 180 | 1 | 1 | 1 |
| `pkg/scripting` | 2,245 | 745 | 4 (+3) | 15k | 13 | 1 | 4 | 13 |
| `pkg/abaplint` | 2,032 | 1,527 | 6 (+4) | 14k | 128 | 4 | 0 | 4 |
| `pkg/ctxcomp` | 1,991 | 2,128 | 9 (+13) | 15k | 49 | 4 | 1 | 19 |
| `pkg/datacluster` | 1,769 | 923 | 7 (+3) | 13k | 34 | 3 (+1) | 1 | 25 |
| `pkg/cache` | 1,293 | 647 | 4 (+4) | 8k | 63 | 2 | 1 | 7 |
| `internal/lsp` | 806 | 155 | 3 (+1) | 5k | 38 | 1 | 2 | 0 |
| `embedded/deps` | 417 | 57 | 1 (+1) | 3k | 15 | 2 (+1) | 0 | 2 |
| `pkg/config` | 363 | 278 | 1 (+2) | 3k | 16 | 2 (+1) | 0 | 7 |
| `pkg/sapcompress` | 306 | 213 | 2 (+2) | 1k | 9 | 2 (+1) | 0 | 7 |
| `embedded/abap` | 281 | 1,819 | 2 (+4) | 3k | 22 | 2 (+1) | 0 | 11 |
| `pkg/itf`, `pkg/temse` | 258 / 154 | 88 / 122 | 1 / 1 | <2k | 3 / 4 | 1 / 1 | 0 | 2 / 3 |
| `cmd/vsp-sso`, `cmd/abapgit-pack`, `internal/install`, `tools/fetchdeps`, `fun` | <210 each | | 1 each | | | | ≤1 | |

The dependency direction is layered:

```
cmd/vsp ─┬─> internal/mcp ──> pkg/{adt,graph,ctxcomp,saprfc,cache,config,datacluster} ──> pkg/{abaplint,itf,temse,sapcompress}
         ├─> internal/{lsp,install}
         └─> pkg/* (incl. research: llvm2abap, ts2abap, wasmcomp via cli_compile.go)
```

- **Cycles:** none. Go forbids them, and `go list` reports no errors.
- **Upward dependencies** (`pkg` → `internal`/`cmd`): **none**.
- **Smell:** `cmd/vsp/cli_compile.go` imports `pkg/llvm2abap`, `pkg/ts2abap` and `pkg/wasmcomp`, so the production CLI links research code. `fun/` imports `pkg/llvm2abap`, which is a demo and fine.
- **`pkg/adt` is the gravity well.** It holds 388k tokens, 1,208 exported identifiers and 9 importers. Nearly every method hangs off `*adt.Client`, which is why it cannot be split into sub-packages cheaply (see section 6).

---

## 2. Per file: top 25 by lines (non-test)

| # | File | Lines | Funcs | Max cyclo | Tokens | Churn 90d |
|---:|---|---:|---:|---:|---:|---:|
| 1 | `cmd/vsp/devops.go` | 3,892 | 57 | 54 | **32.3k** | 17 |
| 2 | `pkg/adt/client.go` | 2,846 | 81 | 26 | 24.2k | **27** |
| 3 | `internal/mcp/tools_register.go` | 2,574 | 26 | 19 | **27.0k** | 20 |
| 4 | `cmd/vsp/cli_extra.go` | 2,334 | 28 | 50 | 19.4k | 17 |
| 5 | `internal/mcp/handlers_graph.go` | 1,960 | 38 | 47 | 17.7k | 10 |
| 6 | `pkg/adt/debugger.go` | 1,840 | 37 | 14 | 16.3k | 3 |
| 7 | `cmd/vsp/cli_cr_config_audit.go` | 1,816 | 27 | 66 | 16.5k | 3 |
| 8 | `pkg/adt/workflows_source.go` | 1,775 | 12 | 61 | 14.7k | 15 |
| 9 | `pkg/adt/crud.go` | 1,713 | 37 | 30 | 16.2k | 22 |
| 10 | `pkg/adt/git_import.go` | 1,603 | 35 | 44 | 14.7k | 2 |
| 11 | `pkg/adt/devtools.go` | 1,459 | 29 | 17 | 12.3k | 9 |
| 12 | `pkg/adt/http.go` | 1,331 | 57 | 31 | 11.6k | 14 |
| 13 | `pkg/scripting/bindings.go` | 1,193 | 39 | 15 | 8.1k | 5 |
| 14 | `internal/mcp/sweep.go` | 1,081 | 27 | 24 | 10.5k | 15 |
| 15 | `pkg/adt/transport_upload.go` | 1,080 | 28 | 25 | 10.5k | 2 |
| 16 | `cmd/vsp/config_cmd.go` | 1,032 | 14 | 32 | 7.0k | 1 |
| 17 | `cmd/vsp/main.go` | 995 | 15 | 63 | 9.6k | 18 |
| 18 | `pkg/adt/transport.go` | 983 | 24 | 11 | 7.9k | 5 |
| 19 | `pkg/saprfc/amdp.go` | 882 | 30 | 10 | 8.0k | 7 |
| 20 | `cmd/vsp/debug.go` | 862 | 17 | 35 | 5.2k | 6 |
| 21 | `pkg/adt/compat.go` | 837 | 19 | 26 | 7.1k | 2 |
| 22 | `cmd/vsp/cli.go` | 789 | 23 | 30 | 6.2k | 13 |
| 23 | `internal/mcp/handlers_help.go` | 780 | 3 | 18 | 12.5k | **40** |
| 24 | `pkg/adt/workflows_execute.go` | 767 | 18 | 37 | 7.6k | 8 |
| 25 | `pkg/adt/callees.go` | 736 | 23 | 14 | 7.2k | 6 |

### Token cost: files over ~25k tokens

An agent that reads one of these whole burns its context.

| File | Tokens | Lines |
|---|---:|---:|
| `cmd/vsp/devops.go` | 32.3k | 3,892 |
| `internal/mcp/tools_register.go` | 27.0k | 2,574 |
| `pkg/adt/client.go` | 24.2k | 2,846 (just under; included) |
| `internal/mcp/readonly_invariant_test.go` (test) | 21.7k | 1,937 (below the line, but it must be read for every new tool) |

`handlers_help.go` is token-dense: 780 lines but 12.5k tokens, because it is all prose.

---

## 3. Per function

### Top 30 by cyclomatic complexity (non-test, non-research)

| Cyclo | Cognit | Lines | Function | Location |
|---:|---:|---:|---|---|
| 131 | 201 | 546 | `runDebugCommand` | cmd/vsp/rfc_debug.go:173 |
| 66 | 149 | 239 | `printCRConfigAuditText` | cmd/vsp/cli_cr_config_audit.go:1578 |
| 63 | 77 | 180 | `resolveConfig` | cmd/vsp/main.go:419 |
| 63 | 132 | 173 | `ExtractEffects` | pkg/graph/effects.go:70 |
| 62 | 100 | 352 | `(Client).EditSourceWithOptions` | pkg/adt/workflows_edit.go:160 |
| 61 | 113 | 427 | `(Client).writeSourceCreate` | pkg/adt/workflows_source.go:471 |
| 59 | 53 | 112 | `(Lexer).process` | pkg/abaplint/lexer.go:351 |
| 56 | 61 | 176 | `ParseABAPFile` | pkg/adt/fileparser.go:82 |
| 54 | 147 | 212 | `printCLIHealthHTML` | cmd/vsp/devops.go:2848 |
| 53 | 75 | 189 | `(Server).routeRFCAction` | internal/mcp/handlers_rfc.go:44 |
| 52 | 100 | 342 | `(Client).writeSourceUpdate` | pkg/adt/workflows_source.go:900 |
| 50 | 78 | 264 | `runExamples` | cmd/vsp/cli_extra.go:1823 |
| 47 | 16 | 104 | `classifyStatement` | internal/mcp/handlers_context.go:323 |
| 47 | 79 | 302 | `(Server).deployZip` | internal/mcp/handlers_deploy.go:40 |
| 47 | 114 | 186 | `(Server).fetchTransportData` | internal/mcp/handlers_graph.go:637 |
| 47 | 120 | 159 | `ExtractDynamicCalls` | pkg/graph/builder_parser.go:536 |
| 46 | 71 | 216 | `SAMLLogin` | pkg/adt/saml_auth.go:43 |
| 45 | 39 | 79 | `(Lexer).add` | pkg/abaplint/lexer.go:259 |
| 44 | 64 | 134 | `(Server).routeReadAction` | internal/mcp/handlers_read.go:17 |
| 44 | 43 | 166 | `(Client).DeleteGitObjects` | pkg/adt/git_import.go:1438 |
| 42 | 92 | 150 | `emitClusters` | cmd/vsp/cluster.go:210 |
| 42 | 39 | 189 | `(Client).WriteSource` | pkg/adt/workflows_source.go:220 |
| 40 | 60 | 228 | `runGraphCoChange` | cmd/vsp/cli_extra.go:1189 |
| 40 | 98 | 135 | `printCLIHealth` | cmd/vsp/devops.go:2546 |
| 39 | 96 | 121 | `(Client).Variant` | pkg/adt/variants.go:92 |
| 39 | 52 | 272 | `(Client).UpdateFromFileWithOptions` | pkg/adt/workflows_deploy.go:264 |
| 39 | 70 | 166 | `AnalyzeCrossings` | pkg/graph/crossing.go:282 |
| 38 | 47 | 142 | `runUpdate` | cmd/vsp/update.go:135 |
| 37 | 46 | 234 | `(Client).ExecuteABAP` | pkg/adt/workflows_execute.go:133 |
| 35 | 51 | 116 | `(debugSession).repl` | cmd/vsp/debug.go:197 |

### Top 30 by cognitive complexity (non-test, non-research)

| Cognit | Cyclo | Lines | Function | Location |
|---:|---:|---:|---|---|
| 201 | 131 | 546 | `runDebugCommand` | cmd/vsp/rfc_debug.go:173 |
| 149 | 66 | 239 | `printCRConfigAuditText` | cmd/vsp/cli_cr_config_audit.go:1578 |
| 147 | 54 | 212 | `printCLIHealthHTML` | cmd/vsp/devops.go:2848 |
| 132 | 63 | 173 | `ExtractEffects` | pkg/graph/effects.go:70 |
| 120 | 47 | 159 | `ExtractDynamicCalls` | pkg/graph/builder_parser.go:536 |
| 114 | 47 | 186 | `(Server).fetchTransportData` | internal/mcp/handlers_graph.go:637 |
| 113 | 61 | 427 | `(Client).writeSourceCreate` | pkg/adt/workflows_source.go:471 |
| 100 | 62 | 352 | `(Client).EditSourceWithOptions` | pkg/adt/workflows_edit.go:160 |
| 100 | 52 | 342 | `(Client).writeSourceUpdate` | pkg/adt/workflows_source.go:900 |
| 98 | 40 | 135 | `printCLIHealth` | cmd/vsp/devops.go:2546 |
| 96 | 39 | 121 | `(Client).Variant` | pkg/adt/variants.go:92 |
| 92 | 42 | 150 | `emitClusters` | cmd/vsp/cluster.go:210 |
| 79 | 47 | 302 | `(Server).deployZip` | internal/mcp/handlers_deploy.go:40 |
| 78 | 50 | 264 | `runExamples` | cmd/vsp/cli_extra.go:1823 |
| 77 | 63 | 180 | `resolveConfig` | cmd/vsp/main.go:419 |
| 75 | 33 | 199 | `matchValueLevelFindings` | cmd/vsp/value_match.go:28 |
| 75 | 53 | 189 | `(Server).routeRFCAction` | internal/mcp/handlers_rfc.go:44 |
| 74 | 30 | 117 | `runConfigShow` | cmd/vsp/config_cmd.go:103 |
| 73 | 33 | 96 | `(Client).FunctionTestData` | pkg/adt/fmtest.go:54 |
| 71 | 46 | 216 | `SAMLLogin` | pkg/adt/saml_auth.go:43 |
| 70 | 39 | 166 | `AnalyzeCrossings` | pkg/graph/crossing.go:282 |
| 67 | 29 | 134 | `(Server).handleCRHistory` | internal/mcp/handlers_transport_analysis.go:35 |
| 65 | 33 | 176 | `runValueLevelAudit` | cmd/vsp/cli_cr_config_audit.go:690 |
| 64 | 44 | 134 | `(Server).routeReadAction` | internal/mcp/handlers_read.go:17 |
| 64 | 29 | 74 | `extractFormData` | pkg/adt/saml_auth.go:321 |
| 64 | 24 | 84 | `walkLayout` | pkg/datacluster/layout.go:105 |
| 63 | 33 | 159 | `runSlim` | cmd/vsp/cli_extra.go:1661 |
| 62 | 22 | 80 | `(Server).routeSourceAction` | internal/mcp/handlers_source.go:18 |
| 62 | 27 | 82 | `(parser).row` | pkg/datacluster/cluster.go:506 |
| 61 | 56 | 176 | `ParseABAPFile` | pkg/adt/fileparser.go:82 |

Test-code outliers, for information only: `checkTransportService` (embedded/abap/transport_service_test.go:164) has cyclo 89 and cognit 185. `checkGitPolicy` has cyclo 57. `synthValue` (readonly_invariant_test.go:725) has cyclo 54.

### Every function over 150 lines (non-test: 55)

| Lines | Cyclo | Cognit | Function | Location |
|---:|---:|---:|---|---|
| 695 | 18 | 1 | `handleHelp` (a string table) | internal/mcp/handlers_help.go:13 |
| 546 | 131 | 201 | `runDebugCommand` | cmd/vsp/rfc_debug.go:173 |
| 427 | 61 | 113 | `(Client).writeSourceCreate` | pkg/adt/workflows_source.go:471 |
| 352 | 62 | 100 | `(Client).EditSourceWithOptions` | pkg/adt/workflows_edit.go:160 |
| 342 | 52 | 100 | `(Client).writeSourceUpdate` | pkg/adt/workflows_source.go:900 |
| 302 | 47 | 79 | `(Server).deployZip` | internal/mcp/handlers_deploy.go:40 |
| 292 | 14 | 13 | `(Server).registerCRUDTools` | internal/mcp/tools_register.go:973 |
| 272 | 39 | 52 | `(Client).UpdateFromFileWithOptions` | pkg/adt/workflows_deploy.go:264 |
| 264 | 50 | 78 | `runExamples` | cmd/vsp/cli_extra.go:1823 |
| 257 | 33 | 47 | `(Server).handleInstallDummyTest` | internal/mcp/handlers_install.go:40 |
| 239 | 66 | 149 | `printCRConfigAuditText` | cmd/vsp/cli_cr_config_audit.go:1578 |
| 234 | 37 | 46 | `(Client).ExecuteABAP` | pkg/adt/workflows_execute.go:133 |
| 228 | 40 | 60 | `runGraphCoChange` | cmd/vsp/cli_extra.go:1189 |
| 218 | 19 | 18 | `(Server).registerReadTools` | internal/mcp/tools_register.go:126 |
| 216 | 46 | 71 | `SAMLLogin` | pkg/adt/saml_auth.go:43 |
| 212 | 54 | 147 | `printCLIHealthHTML` | cmd/vsp/devops.go:2848 |
| 201 | 23 | 26 | `(Client).CreateFromFile` | pkg/adt/workflows_deploy.go:49 |
| 199 | 33 | 75 | `matchValueLevelFindings` | cmd/vsp/value_match.go:28 |
| 195 | 29 | 40 | `FinalizeCRConfigAuditReport` | pkg/graph/cr_config_audit.go:212 |
| 192 | 32 | 49 | `runInstallZadtVsp` | cmd/vsp/devops.go:3464 |
| 189 | 53 | 75 | `(Server).routeRFCAction` | internal/mcp/handlers_rfc.go:44 |
| 189 | 42 | 39 | `(Client).WriteSource` | pkg/adt/workflows_source.go:220 |
| 186 | 47 | 114 | `(Server).fetchTransportData` | internal/mcp/handlers_graph.go:637 |
| 186 | 35 | 43 | `(Analyzer).Analyze` | pkg/ctxcomp/analyzer.go:106 |
| 185 | 32 | 48 | `(Server).handleInstallZADTVSP` | internal/mcp/handlers_install.go:298 |
| 181 | 27 | 36 | `walkDDICMetadata` | cmd/vsp/cli_cr_config_audit.go:491 |
| 180 | 63 | 77 | `resolveConfig` | cmd/vsp/main.go:419 |
| 180 | 10 | 9 | `(Server).registerAnalysisTools` | internal/mcp/tools_register.go:383 |
| 177 | 22 | 29 | `(Client).RenameObject` | pkg/adt/workflows_fileio.go:28 |
| 176 | 33 | 65 | `runValueLevelAudit` | cmd/vsp/cli_cr_config_audit.go:690 |
| 176 | 56 | 61 | `ParseABAPFile` | pkg/adt/fileparser.go:82 |
| 175 | 1 | 0 | `graphProbes` (table) | internal/mcp/sweep_probes.go:231 |
| 173 | 34 | 39 | `runBoundaries` | cmd/vsp/devops.go:148 |
| 173 | 29 | 47 | `(Server).handleCheckBoundaries` | internal/mcp/handlers_graph.go:19 |
| 173 | 63 | 132 | `ExtractEffects` | pkg/graph/effects.go:70 |
| 172 | 28 | 41 | `runGraphWhereUsedConfig` | cmd/vsp/cli_extra.go:2090 |
| 172 | 12 | 15 | `buildCreateObjectBody` | pkg/adt/crud.go:945 |
| 168 | 1 | 0 | `focusedToolSet` (table) | internal/mcp/tools_focused.go:6 |
| 168 | 31 | 54 | `(Transport).request` | pkg/adt/http.go:259 |
| 166 | 44 | 43 | `(Client).DeleteGitObjects` | pkg/adt/git_import.go:1438 |
| 166 | 39 | 70 | `AnalyzeCrossings` | pkg/graph/crossing.go:282 |
| 164 | 26 | 40 | `runCopy` | cmd/vsp/copy_cmd.go:67 |
| 162 | 2 | 1 | `(StatementMatcher).register` (table) | pkg/abaplint/matcher.go:80 |
| 161 | 22 | 36 | `ComputeSlim` | pkg/graph/queries_slim.go:63 |
| 160 | 24 | 30 | `runCRConfigAudit` | cmd/vsp/cli_cr_config_audit.go:85 |
| 159 | 33 | 63 | `runSlim` | cmd/vsp/cli_extra.go:1661 |
| 159 | 32 | 57 | `fillSweepTargets` | cmd/vsp/sweep.go:172 |
| 159 | 13 | 12 | `(Server).registerDevTools` | internal/mcp/tools_register.go:812 |
| 159 | 26 | 48 | `(Client).GetFunctionGroupAllSources` | pkg/adt/client.go:748 |
| 159 | 47 | 120 | `ExtractDynamicCalls` | pkg/graph/builder_parser.go:536 |
| 155 | 30 | 38 | `(Server).handleCreateBusinessCatalog` | internal/mcp/handlers_iam.go:114 |
| 154 | 30 | 59 | `runCRHistory` | cmd/vsp/devops.go:879 |
| 153 | 11 | 21 | `parseUnitTestResult` | pkg/adt/devtools.go:907 |
| 152 | 23 | 34 | `runInstallAbapGit` | cmd/vsp/devops.go:3657 |
| 152 | 23 | 26 | `(Client).writeClassMethodUpdate` | pkg/adt/workflows_source.go:1245 |

Four of these are data tables (`handleHelp`, `graphProbes`, `focusedToolSet`, `StatementMatcher.register`), and the four `register*Tools` functions are schema declarations. The length check should report them but not count them as complexity.

---

## 4. Hotspots and change locality

### 4.1 Hotspot ranking (lines × 90-day churn, non-test)

| # | File | Lines | Max cyclo | Σ cyclo | Churn | Tokens | Verdict |
|---:|---|---:|---:|---:|---:|---:|---|
| 1 | `pkg/adt/client.go` | 2,846 | 26 | 360 | 27 | 24.2k | **split (files)** |
| 2 | `cmd/vsp/devops.go` | 3,892 | 54 | 735 | 17 | 32.3k | **split (files) + dedupe** |
| 3 | `internal/mcp/tools_register.go` | 2,574 | 19 | 189 | 20 | 27.0k | **split (files)** |
| 4 | `cmd/vsp/cli_extra.go` | 2,334 | 50 | 369 | 17 | 19.4k | **split (files) + dedupe** |
| 5 | `pkg/adt/crud.go` | 1,713 | 30 | 223 | 22 | 16.2k | **split (files)** |
| 6 | `internal/mcp/handlers_help.go` | 780 | 18 | 31 | **40** | 12.5k | **extract to embedded text** |
| 7 | `pkg/adt/workflows_source.go` | 1,775 | 61 | 285 | 15 | 14.7k | split files; refactor functions later |
| 8 | `internal/mcp/handlers_graph.go` | 1,960 | 47 | 396 | 10 | 17.7k | **dedupe with CLI** |
| 9 | `cmd/vsp/rfc_debug.go` | 735 | **131** | 134 | 26 | 6.2k | **function refactor** (verb table) |
| 10 | `pkg/adt/http.go` | 1,331 | 31 | 264 | 14 | 11.6k | leave the core alone (see section 6) |
| 11 | `cmd/vsp/main.go` | 995 | 63 | 204 | 18 | 9.6k | split files |
| 12 | `internal/mcp/sweep.go` | 1,081 | 24 | 159 | 15 | 10.5k | leave |
| 13 | `pkg/adt/devtools.go` | 1,459 | 17 | 163 | 9 | 12.3k | optional later |
| 14 | `internal/mcp/server.go` | 546 | 25 | 73 | 19 | 5.0k | leave (small) |
| — | `internal/mcp/readonly_invariant_test.go` (test) | 1,937 | 54 | — | 7 | 21.7k | **split table from harness** |

### 4.2 Change locality

- **Files per commit:** median 3, mean 4.7, p90 9 Go files per Go-touching commit (all files: median 3, mean 4.2, p90 9). Commits are already reasonably local. The load comes from *which* files they touch, not from how many.
- **Whole-file read load per commit** (sum of tokens of the Go files touched, at today's sizes): median **16.3k**, mean **26.6k**, p90 **59.8k**.
- **God-file presence:**

| File | Commits (of 281) | Share | Tokens to read whole |
|---|---:|---:|---:|
| `internal/mcp/handlers_help.go` | 40 | 14% | 12.5k |
| `pkg/adt/client.go` | 27 | 10% | 24.2k |
| `cmd/vsp/rfc_debug.go` | 26 | 9% | 6.2k |
| `pkg/adt/crud.go` | 22 | 8% | 16.2k |
| `internal/mcp/tools_register.go` | 20 | 7% | 27.0k |
| `internal/mcp/server.go` | 19 | 7% | 5.0k |
| `cmd/vsp/main.go` | 18 | 6% | 9.6k |
| `cmd/vsp/devops.go` | 17 | 6% | 32.3k |
| `cmd/vsp/cli_extra.go` | 17 | 6% | 19.4k |
| `internal/mcp/handlers_universal.go` | 10 | 4% | 2.1k (not a load multiplier) |

- Of the 114 commits that touch non-test `internal/mcp`, **40 touch `handlers_help.go`** and 20 touch `tools_register.go`.
- **60 commits touch both `cmd/vsp` and `internal/mcp`**. Part of that is real CLI/MCP parity work. Part of it is the duplicated graph, boundary and health logic being fixed twice.
- `tools_register.go` hunks fall on many different `register*Tools` functions: `registerCRUDTools` 9, `registerI18NTools` 7, `registerDevTools` 6, `registerReadTools` 4, and 13 other functions 1–3 times each. Edits never need the other 25 functions, which confirms that the file is a bag of independent slices.
- `client.go` hunks spread over 25+ unrelated functions. The top one is `GetFunctionGroupAllSources` with 7, followed by `TraceExecution`, `SearchObjectByType` and `GetTypeInfo`. This also confirms a bag of independent slices.

### 4.3 Task-to-read cost (whole-file tokens an agent must open)

The "today" column counts whole-file reads, which is what an agent does when it cannot grep precisely. A careful agent can grep and read ranges, but it still has to find the range first, and every misjudged range costs a re-read.

| Task (CLAUDE.md table) | Files today | Tokens today | Files after splits | Tokens after | Saved |
|---|---|---:|---|---:|---:|
| **Add an MCP tool** | `tools_register.go` 27.0k, `handlers_<domain>.go` ~5.9k, `handlers_universal.go` 2.1k, `tools_focused.go` 2.0k, `tools_groups.go` 0.7k, `handlers_help.go` 12.5k, `readonly_invariant_test.go` 21.7k (a new tool fails "classify me") | **~71.9k** | `tools_<domain>.go` 1–3.1k, handler 5.9k, universal 2.1k, focused 2.0k, groups 0.7k, `help/<topic>.txt` 0.2–3.0k, `readonly_classes_test.go` 5.1k | **~18–21k** | **~52k (−73%)** |
| **Add an ADT operation** | `client.go` 24.2k (for the pattern and `Client`) + domain file, e.g. `crud.go` 16.2k or `devtools.go` 12.3k | **~36–40k** | `client.go` core 1.0k (+ `package_guard.go` 1.4k when mutating) + domain slice, e.g. `create.go` 6.2k or `lock.go` 4.9k | **~7–9k** | **~30k (−78%)** |
| **Add a lint rule** | `rules.go` 4.3k, `lint.go` 0.7k, `lint_test.go` 3.9k | **~8.9k** | unchanged | 8.9k | 0: already right-sized |
| **Fix a handler** (typical: CRUD) | `handlers_crud.go` 5.9k + `crud.go` 16.2k + `handlers_help.go` 12.5k | **~34.6k** | handler 5.9k + `create.go` 6.2k + one help topic ~1k | **~13k** | ~21k |
| **Fix a graph/boundary handler** | `handlers_graph.go` 17.7k + its CLI twin in `devops.go` 32.3k (duplicate logic) + `client.go` 24.2k | **~74k** | `pkg/graph/adtsource` file ~4k + `handlers_graph_*.go` slice ~5k + `cmd/vsp/boundaries.go` ~8k (after dedupe) + `client.go` slice ~2k | **~19k** | **~55k** |
| **Fix/add a CLI command** | `devops.go` 32.3k or `cli_extra.go` 19.4k, + `main.go` 9.6k | **~29–42k** | command-family file 1.4–12k + `main.go` root 5.0k | **~7–17k** | ~22–25k |

**The 90-day history replayed against the proposed splits** substitutes each god file with the *largest* file that replaces it:

| | Median | Mean | p90 | Total over 281 commits |
|---|---:|---:|---:|---:|
| Today | 16.3k | 26.6k | 59.8k | 7.48M |
| After the top-10 splits | 12.7k | 17.3k | 37.3k | 4.86M (**−35%**) |

---

## 5. Split proposals, ranked by load saved per unit of effort

Effort: **S** is under half a day and purely mechanical in the same package. **M** is 1–2 days, or crosses packages. **L** is more than 3 days, or redesigns an API.

All the "same package" moves below are **zero API change**. Go lets a package be spread over any number of files, and methods on `*Client` and `*Server` can live in any file of their package. Proof of equivalence for those moves is: the build passes, `go vet` passes, the test count is unchanged, and the pinned surface tests are unchanged (`tools_parity_test`, `mode_parity_test`, `docs_parity_test`, `readonly_invariant_test`).

### #1. `internal/mcp/handlers_help.go` → embedded help topics (S)

- **Data:** 40 commits, the #1 churned Go file. Its 695-line `handleHelp` is a `switch` over 17 topic string literals. It costs 12.5k tokens, and an edit needs a single 0.05–3k topic.
- **Move:**
  - Each `case` body goes to `internal/mcp/help/<topic>.txt` (`read`, `edit`, `create`, `delete`, `search`, `query`, `test`, `info`, `rfc`, `i18n`, `revisions`, `lint`, `grep`, `debug`, `analyze`, `system`, `tips`).
  - The files are loaded with `//go:embed help/*.txt` into a `map[string]string`, with aliases (`history`→`revisions`, `best_practices`/`workflows`/`best`→`tips`) kept in a small Go map.
  - `handleHelp` drops to about 20 lines.
  - `getUnhandledErrorMessage` and `actionNeedsTarget` stay in `handlers_help.go`. Analyze, at 3.0k, could later be split by sub-type.
  - Prose stops counting as Go complexity, and help diffs become plain text diffs.
- **Dependency:** unchanged, inside `internal/mcp`.
- **Risk:** low. No safety gate is involved. The safeguard is a one-off test that renders every topic before and after and asserts byte equality. `handlers_rfc_docs_test` and `docs_parity_test` already read help output.
- **Saves:** about 10k tokens on 40 commits per 90 days, and on every "add tool" task.

### #2. `internal/mcp/tools_register.go` → `tools_<domain>.go` (S)

- **Data:** 27.0k tokens, the second-largest file, in 20 commits. It is 26 `register<X>Tools` functions that do not call each other, and the hunks spread over 17 of them.
- **Move:** each `register<X>Tools` goes to `tools_<x>.go`, beside the existing `handlers_<x>.go`: `tools_read.go`, `tools_crud.go` (3.1k, the largest), `tools_devtools.go`, `tools_i18n.go`, `tools_debugger.go`, `tools_analysis.go` and so on. `tools_register.go` keeps `registerTools` with the `shouldRegister` closure and the ordered list of calls, about 1k tokens. That list is the one piece someone adding a group must see.
  - Keeping the schemas in separate `tools_*.go` files, not inside `handlers_*.go`, keeps the handler files from growing. An agent opens the `tools_x.go` / `handlers_x.go` pair.
- **Dependency:** unchanged.
- **Risk:** low. Registration order is preserved by the unchanged call list in `registerTools`. `tools_parity_test` pins the tool counts and `mode_parity_test` pins per-mode sets. No safety gate is involved: `shouldRegister` filters by mode and config, not by read-only.
- **Saves:** about 24–26k tokens per "add or change tool" task (20 commits per 90 days).

### #3. Split `readOnlyClasses` out of `readonly_invariant_test.go` (S)

- **Data:** 21.7k tokens. Every new tool or router case must be classified there ("classify me"), but the table is only 400 lines and 5.1k tokens. The rest is the fake-SAP harness.
- **Move:** lines 252–652 (`readOnlyClasses` and the `surfaceClass` constants, if they are local) go to `internal/mcp/readonly_classes_test.go`. The harness stays. Optionally, `actionCases` (199 lines) and `synthValue` move to `readonly_synth_test.go`.
- **Risk:** this file **is** the read-only safety gate. The move must be pure. Verify that `go test -run ReadOnly -v` gives identical subtest names and counts before and after, and do not reorder entries in the same commit.
- **Saves:** about 16k tokens per "add tool" task.

### #4. `pkg/adt/client.go` → files by domain inside `pkg/adt` (S–M)

- **Data:** 24.2k tokens and 27 commits, the most-churned file in `pkg/adt`. It holds 81 functions across about 10 unrelated domains, and the hunks spread over 25+ functions.
- **Move** (approximate token sizes after the move):

| New file | Content | ~tokens |
|---|---|---:|
| `client.go` | `Client`, `NewClient*`, keep-alive, cookies, `Language`, `Safety` accessor | 1.1k |
| `package_guard.go` | `checkSafety`, `checkPackageSafety`, `checkObjectPackageSafety`, `CheckObjectPackageByName`, `checkTransportableEdit`, `getObjectPackage`, `normalizeObjectURLForPackageCheck`, `canonicalizeObjectURL`, `objectNameFromURL`, `AllowPackageTemporarily` | 1.4k |
| `search.go` | `SearchObject*`, `CanonicalObjectType`, `FilterExactName`, `ResolveObjectRef` | 1.8k |
| `objects_read.go` | `GetProgram`…`GetMessageClass`, `isNotFound`, `parseSRVBMetadata`, `GetFunctionGroupAllSources` | 5.6k (could be split further by PROG/CLAS/FUGR/RAP) |
| `package_read.go` | `PackageExists`, `GetPackage`, `parsePackageNodeStructure` | 0.7k |
| `ddic_read.go` + **merge into existing `query_sql.go`** | `GetTable`/`View`/`Structure`/`TableContents`, `RunQuery`, `runQueryRaw`, `wrapSQL`, `splitAtCommas`, `parseTableContents` | 2.1k |
| `system_info.go` | `GetTransaction`, `GetTypeInfo`, `GetSystemInfo`, `GetInstalledComponents` | 2.9k |
| `callgraph.go` | `IsExecutableKind`, `FlattenCallGraph`, `AnalyzeCallGraph`, `CompareCallGraphs`, `ExtractCallEdgesFromTrace`, `TraceExecution` (sits beside `callees.go`) | 3.1k |
| `object_explorer.go` | `GetObjectStructureCAI` and its parsers | 0.7k |
| `traces.go` / `sqltrace.go` | ATRA traces / SQL trace state and directory | 2.3k / 2.1k |
| `api_release.go` | `GetAPIReleaseState` | 0.2k |

- **Dependency:** unchanged (same package).
- **Risk:** low for the move itself, but `package_guard.go` **is a safety gate** (the package allowlist used by `checkMutation` in `mutation_gate.go`). Move it in its own commit, unchanged. Tests: `safety_test.go`, `client_test.go`, `client_fugr_gaps_test.go`, `client_include_fallback_test.go`, and the `cmd/vsp/cli_safety_test.go` precedence test.
- **Saves:** about 20k tokens per "add ADT op" task; 27 commits per 90 days.

### #5. Dedupe graph, boundary and health logic into `pkg/graph/adtsource` (M)

- **Data:** at least 200 lines are verbatim duplicates on each side, and more are near-duplicates. The pairs are `devops.go`↔`handlers_graph.go` (26 shared 10-line windows), `devops.go`↔`handlers_health.go` (15), `cli_extra.go`↔`handlers_graph.go` (13) and `devops.go`↔`handlers_transport_analysis.go` (10). The named twins are:
  - `resolveTADIRcli` / `resolveTADIR`
  - `resolveFMviaTFDIRcli` / `resolveFMviaTFDIR`
  - `resolvePackagesCLI` / `(Server).resolvePackages`
  - `collectPackageBoundariesWithDetails` / `packageGraph`
  - `runCRHistory` / `handleCRHistory`
  - the health collectors.

  This is CLAUDE.md priority #1 ("SQL/ADT adapters pending").
- **Move:** a new package `pkg/graph/adtsource` that imports `pkg/adt` and `pkg/graph`. It would hold:
  - `ResolveTADIR`, `ResolveFMviaTFDIR`, `ResolvePackages`
  - `PackageGraph` / `BuildBoundaryGraph`
  - `FetchTransportData` (today's 186-line, cognit-114 `fetchTransportData`)
  - `FetchReverseDeps`
  - CR history collection
  - the health collectors (`CollectTests`/`ATC`/`Boundaries`/`Staleness`)

  The functions return plain structs. `cmd/vsp` and `internal/mcp` keep only flag/argument parsing and output formatting (text, MD, HTML for the CLI; MCP text for the server).
- **Dependency after:** `cmd/vsp → pkg/graph/adtsource → {pkg/graph, pkg/adt}` and `internal/mcp → pkg/graph/adtsource`. `pkg/graph` itself stays free of `pkg/adt`, which keeps its tests offline.
- **Risk:** medium. Behaviour has to be reconciled where the twins have drifted: the CLI returns `map[string]string` failures, while MCP returns `[]adt.Unsearched`. These are read-only paths with no mutation gate. Tests: `handlers_graph_boundary_test.go`, `unsearched_cli_test.go`, `pkg/graph` tests. Add a twin-parity test that runs the same fake SAP through the CLI and MCP formatters.
- **Saves:** halves the files touched for every graph or health fix (about 55k tokens on such a task). It also removes a class of "fixed in MCP, not in CLI" bugs.

### #6. `cmd/vsp/devops.go` → command-family files (S)

- **Data:** the largest file (32.3k tokens, the only non-test file over 25k), in 17 commits. It holds 21 cobra commands from 7 unrelated families. `cmd/vsp` already uses one `init()` per file (52 of them), so the pattern exists.
- **Move:**
  - `source.go` (read/write/edit/context, 1.4k)
  - `test_atc.go` (`runTest`, `printUnitTestReport`, `runATC`, 1.7k)
  - `health.go` (`runHealth`, the collectors, `printCLIHealth{,MD,HTML}`, ~9.8k, shrinking after #5)
  - `boundaries.go` (boundaries, what-package, tr/cr-boundaries, cr-history and their printers, ~11.9k, shrinking a lot after #5)
  - `transport.go` (list/get)
  - `deploy.go`
  - `install.go` (zadt-vsp, abapgit, list, 3.5k)

  Each file declares its own `cobra.Command` vars and `init()`.
- **Dependency:** unchanged.
- **Risk:** low. Cobra sorts subcommands by name, so `init` order does not change help output; confirm with a before/after `vsp --help` diff. `runSourceWrite` and `runSourceEdit` reach the write path, and `devops_write_source_package_test.go` and `cli_safety_test.go` cover it.
- **Saves:** about 20–30k tokens per CLI change touching this file.

### #7. `pkg/adt/crud.go` → `lock.go`, `create.go`, `object_urls.go`, `table_create.go` (S)

- **Data:** 16.2k tokens and 22 commits. The hunks concentrate on `LockObject` (7), `CreateObjectOptions` (5), `CreateObject`/`CreateTable` (4 each) and `UnlockObject` (3). Issue **#88 (lock handle bug)** lives here.
- **Move:**

| New file | Content | ~tokens |
|---|---|---:|
| `lock.go` | `LockObject`, `parseLockResult`, `lockExceptionError`, `UnlockObject`, `UpdateSource`, `tryCleanupOrphanLock`, `isLockConflictError` | 4.9k |
| `create.go` | `CreateObject`, `buildCreateObjectBody`, `objectTypes`, `PartialCreateError`, `reconcileFailedCreate`, `cleanupPartialObject`, `RecoverFailedCreate`, `DeleteObject` | 6.2k |
| `object_urls.go` | `GetObjectURL`, `GetSourceURL`, class-include URL helpers, `CreateTestInclude`/`Get`/`UpdateClassInclude` | 2.7k |
| `servicebinding_publish.go` | service binding publish/unpublish | 0.9k |
| `table_create.go` | `CreateTable`, `generateTableDDL`, `mapFieldType` | 1.7k |

  `lock.go` would sit beside the existing `lock_release.go` and `lock_window.go`, so lock handling stops being spread over a file named "crud".
- **Risk:** low as a pure move. These are mutation paths, but the gate itself (`mutation_gate.go`) is not touched. Tests: `crud_reconcile_test`, `crud_srvb_test`, `lock_corrnr_test`, `unlock_after_expiry_test`.
- **Saves:** about 10k tokens per CRUD or lock task, and it makes #88 easier to reason about.

### #8. `cmd/vsp/rfc_debug.go`: `runDebugCommand` → verb table (S–M)

- **Data:** the worst function in the repo (cyclo 131, cognit 201, 546 lines), and the third most-churned Go file (26 commits). Token cost is small (6.2k), but this is the biggest single **mental-load** item. Every new debugger verb lands in a 37-case `switch`.
- **Move:** a table `map[string]debugVerb{fn, help, mutates bool}` plus one small function per verb, in `rfc_debug_verbs.go`. Generate the `help` text from the table.
- **Risk:** medium. The REPL has a read-only guard, covered by `debug_repl_readonly_test.go` and `debug_readonly_test.go`. Make `mutates` explicit per verb, and add a test that every verb is in the table and classified, in the same spirit as `readOnlyClasses`.
- **Saves:** mostly mental load: no function above cyclo ~15, and verb addition becomes local.

### #9. `cmd/vsp/cli_extra.go` → `query.go`, `execute.go`, `graph_cmd.go`, `usage_cmd.go` (S)

- **Data:** 19.4k tokens and 17 commits. Hunks fall on `runExamples` (10), `runExecute` (8) and `runGraph` (6).
- **Move:**
  - `query.go` (query/grep/info/system/lint, 1.6k)
  - `execute.go` (`runExecute`, exit/reporting helpers, 2.1k)
  - `graph_cmd.go` (graph, co-change, where-used, method-signature, class-sections, 5.9k)
  - `usage_cmd.go` (rename-preview, slim, examples, where-used-config, 6.0k)

  `formatTable` and `readStdin` go to `cli_util.go`. Do this together with #5, since `graphFromCross` and `runGraphCoChange` overlap with MCP.
- **Risk:** low. `runExecute` is EXECUTE-class and covered by `cli_safety_test` and `test_report_test`.

### #10. `cmd/vsp/main.go` → `config_resolve.go` + `auth_flags.go` (S); `resolveConfig` table-driven (M, later)

- **Data:** 18 commits; `resolveConfig` has cyclo 63 and 180 lines.
- **Move:**
  - `resolveConfig`, `resolveCallTimeout`, `validateConfig` and `applyDefaultSystemSettings` go to `config_resolve.go` (2.1k).
  - `process{Browser,SSO,SAML,Cookie}Auth` go to `auth_flags.go` (2.5k).
  - `main.go` keeps the root command and `runServer` (5.0k).
- **Risk:** **medium-high for the later refactor, low for the move.** This is credential and system precedence (`.vsp.json` default vs env; see the memory note on live credentials). Tests: `cli_safety_test` (safe precedence), `default_system_settings_test`. Do the file move now. Do the table-driven precedence rewrite only with a precedence-matrix test written first.

### Also worth doing, lower ratio

- **`pkg/adt/workflows_source.go`** (14.7k, 15 commits, cyclo 61 and 52): split into `workflows_source.go` (`GetSource`/`WriteSource` entry, 4.3k), `workflows_source_create.go` (3.4k), `workflows_source_update.go` (incl. `writeClassMethodUpdate`, 4.3k) and `source_diff.go` (`CompareSource`, `generateUnifiedDiff`, `CloneObject`, `GetClassInfo`, 2.6k). That is **S**.
  - Decomposing `writeSourceCreate` (427 lines) and `writeSourceUpdate` (342 lines) into phases (resolve → lock → write → syntax-check → activate → verify) is **M–L** and **high risk**, because this is the core write path through the mutation gate and lock handling.
  - Do it only alongside #88 and with `integration_test` runs against a live system.
- **`internal/mcp/handlers_graph.go`:** after #5 it falls to about 10k. Optionally split the usage/config cluster (6.0k, `handleUsageExamples`/`handleWhereUsedConfig` and its 12 helpers) into `handlers_usage.go`. That is **S**.

---

## 6. Splits that are NOT worth it

| Candidate | Why not |
|---|---|
| `internal/mcp/handlers_universal.go` | Already 226 lines and 2.1k tokens, and only 4% of commits touch it. Dispatch is a list of 31 `route*Action` functions that already live in their domain files. The god file no longer exists. |
| Splitting `pkg/adt` into sub-packages (e.g. `adt/crud`, `adt/transport`) | **L** and invasive. Over 100 files hang methods off `*Client`, the package exports 1,208 identifiers, and it has 9 importers. Go cannot spread a type's methods across packages, so every call site would change. File-level splits (#4, #7) capture most of the token win at about 5% of the cost. Revisit only for Client-free leaf code (XML parsers, `fileparser.go`, call-graph pure functions) if it keeps growing. |
| `cmd/vsp/cli_cr_config_audit.go` (1,816 lines, cyclo 66, cognit 149) | 3 commits in 90 days. It is ugly but cold. Leave it until it is next changed, then split out the printers. |
| `pkg/adt/debugger.go` (1,840 lines) | 3 commits, and the REST debugger is marked deprecated in CLAUDE.md. Delete it eventually rather than split it. |
| `pkg/adt/http.go` → `(Transport).request` (cyclo 31, 168 lines) | CSRF, session and re-auth logic, flagged as session-sensitive in CLAUDE.md. The complexity is inherent, and a refactor risks real regressions. At most, move tracing (`traceHTTP*`) and the cookie jar (`resettableJar`, `adoptServerCookies`, `cloneCookies`) to `http_trace.go` / `http_cookies.go` (S, saves ~4k tokens). Do not touch `request`. |
| `pkg/abaplint/lexer.go` `process`/`add` (cyclo 59/45), `pkg/graph/effects.go` `ExtractEffects` (63), `handlers_context.go` `classifyStatement` (47) | State machines and statement classifiers: high cyclomatic counts with flat, readable `switch`es, and low churn. Splitting them hurts readability. |
| `internal/mcp/tools_focused.go`, `sweep_probes.go` `graphProbes`, `abaplint/matcher.go` `register` | Data tables. Their length is the point. |
| `internal/mcp/sweep.go` (10.5k, 15 commits) | Cohesive single feature, max cyclo 24. Fine as is. |
| Lint rule area (`pkg/abaplint`) | The "add a lint rule" task already costs only ~8.9k tokens. |

---

## 7. Research packages (excluded above)

| Package | Lines | Test lines | Files | Max cyclo | Max cognit | Churn 90d | Imported by |
|---|---:|---:|---:|---|---:|---:|---|
| `pkg/wasmcomp` | 4,260 | 1,669 | 18 | **210** `(compiler).emitInstructions` | 134 | 0 | `cmd/vsp` (cli_compile.go) |
| `pkg/llvm2abap` | 1,973 | 136 | 2 | 103 `(abapCompiler).emitInst` | 168 | 0 | `cmd/vsp`, `fun` |
| `pkg/jseval` | 1,782 | 959 | 9 | 168 `evalNode` | **358** | 3 | none |
| `pkg/ts2go` | 608 | 0 | 1 | 45 `(transpiler).expr` | 33 | 0 | none |
| `pkg/ts2abap` | 587 | 47 | 2 | 26 `(transpiler).emitStatement` | 40 | 0 | `cmd/vsp` |

These packages hold the four worst functions in the tree, all opcode or AST interpreter `switch`es, which is expected for compilers. They are cold, with 3 commits combined. Recommendations:

- Exclude them from the advisory CI report.
- Consider a `//go:build research` tag on `cmd/vsp/cli_compile.go`, so the production binary and its agents stop linking about 7k lines of research code and the reader is not tempted into them.
- `pkg/jseval` and `pkg/ts2go` have no importers. They are candidates to move under `research/` or `_research/`, outside `./...`.

---

## 8. Advisory CI report: thresholds and today's baseline

The report should be advisory, meaning it comments and never fails, and it should apply only to non-test, non-research code. The suggested mechanism is `golangci-lint` with the `gocyclo`, `gocognit` and `funlen` linters plus `--new-from-rev=origin/main`, so PRs see only findings in the lines they touch. A 30-line script implements the file-size and token ratchet.

| Check | Threshold | Today (baseline) | Notes |
|---|---|---:|---|
| Cyclomatic complexity | > 30 | **51** functions | > 20: 122 · > 50: 11 |
| Cognitive complexity | > 40 | **68** functions | > 30: 117 · > 60: 30 · > 100: 7 |
| Function length | > 150 lines | **55** functions | > 100: 127 · > 200: 17 · > 300: 6. Exempt data tables (`handleHelp`, `focusedToolSet`, `graphProbes`, `register*Tools`) via `//nolint:funlen // table` |
| File size ratchet | no **new** file > 1,000 lines; an existing file over 1,000 lines may not grow | **16** files > 1,000 lines (10 > 1,500, 4 > 2,000) | Store `{file: lines}` in `.complexity-baseline.json`. Shrinking lowers the baseline automatically. |
| File token ratchet | no file > 25k tokens (bytes/4); warn at 15k | **2** > 25k (`devops.go`, `tools_register.go`); **8** > 15k | This is the agent-facing limit. Add `readonly_invariant_test.go` (21.7k) as a test-file warning. |
| Test code, informational | cyclo > 30 / cognit > 40 / len > 150 | 7 / 14 / 13 | Report only. 4 test files > 1,000 lines. |

Starting config (not run here, since `golangci-lint` is not installed on this machine):

```yaml
# .golangci.yml (advisory job: `golangci-lint run --new-from-rev=origin/main --issues-exit-code=0`)
linters:
  default: none
  enable: [gocyclo, gocognit, funlen]
linters-settings:
  gocyclo:  { min-complexity: 30 }
  gocognit: { min-complexity: 40 }
  funlen:   { lines: 150, statements: -1 }
issues:
  exclude-dirs: [pkg/llvm2abap, pkg/wasmcomp, pkg/ts2abap, pkg/ts2go, pkg/jseval]
  exclude-rules:
    - path: _test\.go
      linters: [gocyclo, gocognit, funlen]
```

**Expected baseline after splits #1–#10:**
- Files > 25k tokens: 2 → **0**
- Files > 1,000 lines: 16 → about **10**
- cyclo > 30: 51 → about **48**. `runDebugCommand` and `printCLIHealth*` go; `fetchTransportData` moves.
- Function-length findings drop by about 8, from the help, register and verb tables.

The size and token ratchet is the one most likely to keep the load down. The complexity counts mostly move only when someone deliberately refactors a function.

---

## Appendix: method notes

- Lines are newline counts per file. Funcs are top-level `FuncDecl`s, including methods. Function length is from `func` to the closing brace.
- Exported identifiers are exported funcs and methods plus exported type, var and const names, counted at top level.
- Churn is `git log --since=90.days --name-only origin/main`. Merges list no files, so 401 of 549 commits carry file lists, and 281 touch `.go`. Renamed files lose their earlier history.
- Duplication was measured with exact matches of 10 whitespace-normalised lines containing at least 7 non-trivial lines, between `cmd/vsp` and `internal/mcp`. It is a lower bound: near-duplicates with renamed variables are not counted.
- The task costs and the "after" sizes come from function line ranges in today's files, at bytes/4. The real cost after a split is a little higher, because of repeated `package`/`import` headers (about 50–150 tokens per file).
