package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// vsp git: an abapGit offline zip into a package, and the removal of what
// such an import left, through ZADT_VSP's git service and the abapGit
// installed on the system.

var gitCmd = &cobra.Command{
	Use:   "git",
	Short: "abapGit on the system: import an offline zip into a package, delete what it left (needs ZADT_VSP and abapGit)",
}

var gitImportZipCmd = &cobra.Command{
	Use:   "import-zip <zip> --package <PACKAGE>",
	Short: "Import an abapGit offline zip into a package, in one call (needs ZADT_VSP and abapGit)",
	Long: `Import an abapGit offline zip (the one abapGit's "Export" or vsp export
writes) into a package of the connected system, with the abapGit installed
there: an offline repository for the package, abapGit's deserialize checks,
deserialize, activation. It runs as background job ZVSP_GIT_IMPORT; vsp waits
for it (--wait) and prints the outcome, abapGit's errors and warnings, the
TADIR rows created or changed and the repository key.

Nothing that exists is overwritten without --overwrite (and a package that
already has a repository is refused without it). A package that exists
without a repository is imported into without --overwrite; its own package
entry is left as it is. --overwrite also needs deletes allowed, since
abapGit deletes and recreates an object whose type changed. Unmet requirements or
dependencies, an object of another package, a package move, potential data
loss and an unsupported object type refuse the import. A local object the
zip does not have is never deleted.

Refused under read_only/SAP_READ_ONLY. The package, and every package the
zip's folders map to, must pass allowed_packages. A transportable package
needs --allow-transportable-edits and a transport (--transport, or one chosen
as for any write); a local ($) package takes none.

  vsp -s devsys git import-zip ./demo.zip --package '$ZDEMO'
  vsp -s devsys git import-zip ./demo.zip --package '$ZDEMO' --overwrite`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pkg, _ := cmd.Flags().GetString("package")
		transport, _ := cmd.Flags().GetString("transport")
		overwrite, _ := cmd.Flags().GetBool("overwrite")
		// Every gate before the zip is read or a connection opened. A
		// .vsp.json system's client does not see SAP_READ_ONLY, so the
		// CLI's own read-only test comes first.
		params, err := resolveSystemParams(cmd)
		if err != nil {
			return err
		}
		if cliReadOnly(params) {
			return fmt.Errorf("operation 'GitImportZip' is blocked: read-only mode enabled (read_only in .vsp.json, or SAP_READ_ONLY)")
		}
		client, err := createADTClientFor(cmd)
		if err != nil {
			return err
		}
		if err = client.CheckGitImportPolicy(pkg, transport, overwrite); err != nil {
			return err
		}
		data, err := adt.ReadGitZip(args[0])
		if err != nil {
			return err
		}
		plan, err := adt.AnalyzeGitZip(data, pkg)
		if err != nil {
			return err
		}
		if err = client.CheckGitImportPlan(plan); err != nil {
			return err
		}
		ws, closeWS, err := gitServiceWS(client)
		if err != nil {
			return err
		}
		defer closeWS()

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		repoName, _ := cmd.Flags().GetString("repo-name")
		started, err := client.StartGitImport(ctx, ws, data, adt.GitImportOptions{Package: pkg, RepoName: repoName, Overwrite: overwrite, Transport: transport})
		var unconfirmed *adt.GitImportUnconfirmedError
		if errors.As(err, &unconfirmed) {
			// The job may be running: say what is known, and fail.
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_ = printJSON(gitImportOutput{Started: started})
			}
			return err
		}
		if err != nil {
			return err
		}
		out := gitImportOutput{Started: started}
		var waitErr error
		if wait, _ := cmd.Flags().GetDuration("wait"); wait > 0 {
			wctx, cancel := context.WithTimeout(ctx, wait)
			var werr error
			out.Status, werr = client.WaitGitImport(wctx, ws, started.JobCount)
			cancel()
			waitErr = gitWaitError(started.Job, started.JobCount, werr, ctx.Err())
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			if err := printJSON(out); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "%s -> %s client %s: job %s %s\n", args[0], started.System, started.Client, started.Job, started.JobCount)
			fmt.Fprintf(os.Stderr, "  packages: %s\n", strings.Join(plan.Packages, ", "))
			if started.Transport != "" {
				fmt.Fprintf(os.Stderr, "  transport: %s\n", started.Transport)
			}
			if started.Note != "" {
				fmt.Fprintln(os.Stderr, "  "+started.Note)
			}
			if out.Status != nil {
				printGitImportStatus(out.Status)
			} else {
				fmt.Fprintf(os.Stderr, "  outcome: vsp git import-status %s\n", started.JobCount)
			}
		}
		if waitErr != nil {
			return waitErr
		}
		return gitImportExit(out.Status)
	},
}

// gitImportOutput is what vsp git import-zip --json prints.
type gitImportOutput struct {
	Started *adt.GitImportStarted `json:"started"`
	Status  *adt.GitImportStatus  `json:"status,omitempty"`
}

// gitWaitError is what waiting for the job ended with: nil when it ended
// normally or --wait ran out (a pending result), else the error with the job
// number -- a refusal, a lost connection, or Ctrl-C (parent is the
// command's own context error).
func gitWaitError(job, jobCount string, werr, parent error) error {
	if werr == nil || (errors.Is(werr, context.DeadlineExceeded) && parent == nil) {
		return nil
	}
	return fmt.Errorf("waiting for job %s %s: %w -- the import may still be running: vsp git import-status %s", job, jobCount, werr, jobCount)
}

// gitImportExit fails the command on anything but an import that ran.
func gitImportExit(st *adt.GitImportStatus) error {
	if st == nil {
		return nil
	}
	if st.Result == nil {
		if st.State == adt.GitJobPending {
			return nil
		}
		return fmt.Errorf("%s: %s", st.State, st.Note)
	}
	switch st.Result.Outcome {
	case adt.GitImported:
		return nil
	case adt.GitImportedWithErrors:
		return errors.New("imported with errors: see abapGit's log above")
	}
	return fmt.Errorf("%s (%s): %s", st.Result.Outcome, st.Result.Code, st.Result.Message)
}

func printGitImportStatus(st *adt.GitImportStatus) {
	fmt.Fprintf(os.Stderr, "  job state: %s (status %q)\n", st.State, st.JobStatus)
	if r := st.Result; r != nil {
		fmt.Fprintf(os.Stderr, "  outcome: %s", r.Outcome)
		if r.Code != "" {
			fmt.Fprintf(os.Stderr, " (%s)", r.Code)
		}
		fmt.Fprintln(os.Stderr)
		if r.Message != "" {
			fmt.Fprintln(os.Stderr, "  "+r.Message)
		}
		if r.RepoKey != "" {
			created := ""
			if r.RepoCreated {
				created = ", created"
			}
			fmt.Fprintf(os.Stderr, "  repository: %s %q%s\n", r.RepoKey, r.RepoName, created)
		}
		if r.PackageCreated {
			fmt.Fprintf(os.Stderr, "  package %s created\n", r.Package)
		}
		for _, row := range r.Tadir {
			what := "changed"
			if row.Created {
				what = "created"
			}
			fmt.Fprintf(os.Stderr, "  %s %s %-4s %-40s %s\n", what, row.PgmID, row.Object, row.ObjName, row.DevClass)
		}
		for _, l := range r.Log {
			fmt.Fprintf(os.Stderr, "  [%s] %s %s %s\n", l.Type, l.ObjType, l.ObjName, l.Text)
		}
		if r.InfoCount > 0 {
			fmt.Fprintf(os.Stderr, "  (%d info/success messages not shown)\n", r.InfoCount)
		}
	} else {
		for _, l := range st.JobLog {
			fmt.Fprintln(os.Stderr, "    "+l)
		}
	}
	if st.Note != "" {
		fmt.Fprintln(os.Stderr, "  "+st.Note)
	}
}

var gitImportStatusCmd = &cobra.Command{
	Use:   "import-status <JOB>",
	Short: "Show the state and result of an import job (read-only; needs ZADT_VSP)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := createADTClientFor(cmd)
		if err != nil {
			return err
		}
		ws, closeWS, err := gitServiceWS(client)
		if err != nil {
			return err
		}
		defer closeWS()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		st, err := client.GitImportStatus(ctx, ws, args[0])
		if err != nil {
			return err
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			return printJSON(st)
		}
		fmt.Fprintf(os.Stderr, "job %s %s\n", st.Job, st.JobCount)
		printGitImportStatus(st)
		return nil
	},
}

var gitDeleteObjectsCmd = &cobra.Command{
	Use:   `delete-objects --package <PACKAGE> "TYPE NAME" ...`,
	Short: "Delete exactly the given objects of a package, then the package if empty (needs ZADT_VSP)",
	Long: `Delete exactly the named TADIR items of a package -- each only if the
package's TADIR has it, each through the same gated delete as any other --
then the package itself if nothing and no subpackage is left in it and no
abapGit repository is registered for it. Nothing outside the package is
deleted, and a package is never deleted as an item. If an object cannot be
deleted, the repository and the package stay.

The abapGit repository registered for the package (its row: URL, branch,
settings; not its objects) is kept unless --delete-repo is given, and even
then it is unregistered only when it is an offline repository and the
package is empty after the deletes. An online repository is never
unregistered: --delete-repo with one is refused before anything is deleted.

Refused under read_only/SAP_READ_ONLY; the package must pass
allowed_packages; a transportable package needs --allow-transportable-edits
and --transport.

  vsp -s devsys git delete-objects --package '$ZDEMO' "PROG ZDEMO_REPORT" "CLAS ZCL_DEMO"
  vsp -s devsys git delete-objects --package '$ZDEMO' --delete-repo "PROG ZDEMO_REPORT"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pkg, _ := cmd.Flags().GetString("package")
		transport, _ := cmd.Flags().GetString("transport")
		params, err := resolveSystemParams(cmd)
		if err != nil {
			return err
		}
		if cliReadOnly(params) {
			return fmt.Errorf("operation 'GitDeleteObjects' is blocked: read-only mode enabled (read_only in .vsp.json, or SAP_READ_ONLY)")
		}
		client, err := createADTClientFor(cmd)
		if err != nil {
			return err
		}
		if err = client.CheckGitDelete(pkg, transport); err != nil {
			return err
		}
		items, err := adt.ParseGitDeleteItems(args)
		if err != nil {
			return err
		}
		ws, closeWS, err := gitServiceWS(client)
		if err != nil {
			return err
		}
		defer closeWS()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		deleteRepo, _ := cmd.Flags().GetBool("delete-repo")
		res, derr := client.DeleteGitObjects(ctx, ws, pkg, items, transport, deleteRepo)
		if res != nil {
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				if err := printJSON(res); err != nil {
					return err
				}
			} else {
				for _, o := range res.Objects {
					fmt.Fprintf(os.Stderr, "  %-8s %s %s %s\n", o.Status, o.Type, o.Name, o.Reason)
				}
				switch {
				case res.RepoDeleted && res.Repo != nil:
					fmt.Fprintf(os.Stderr, "  repository %s %q deleted\n", res.Repo.Key, res.Repo.Name)
				case res.RepoNote != "":
					fmt.Fprintln(os.Stderr, "  repository: "+res.RepoNote)
				}
				if res.PackageDeleted {
					fmt.Fprintf(os.Stderr, "  package %s deleted\n", res.Package)
				} else if res.PackageNote != "" {
					fmt.Fprintf(os.Stderr, "  package %s %s\n", res.Package, res.PackageNote)
					for _, r := range res.Remaining {
						fmt.Fprintln(os.Stderr, "    "+r)
					}
				}
			}
		}
		return derr
	},
}

// gitServiceWS is a WebSocket to ZADT_VSP, opened only after every gate has
// passed, authenticated as the profile's HTTP client is.
func gitServiceWS(client *adt.Client) (*adt.DebugWebSocketClient, func(), error) {
	ws := adt.NewDebugWebSocketClient(cfg.BaseURL, cfg.Client, cfg.Username, cfg.Password, cfg.InsecureSkipVerify)
	if cookies := transportWSCookies(client); len(cookies) > 0 {
		ws.SetCookies(cookies)
	}
	if err := ws.Connect(context.Background()); err != nil {
		return nil, nil, fmt.Errorf("ZADT_VSP (WebSocket) is not reachable: %w -- the abapGit import needs ZADT_VSP with ZCL_VSP_GIT_SERVICE and abapGit (vsp install zadt-vsp)", err)
	}
	return ws, func() { ws.Close() }, nil
}

func init() {
	gitImportZipCmd.Flags().String("package", "", "Target package (required)")
	gitImportZipCmd.Flags().String("repo-name", "", "Name of the offline repository created for the package (default: the package)")
	gitImportZipCmd.Flags().Bool("overwrite", false, "Overwrite objects that exist (and import into the package's existing offline repository)")
	gitImportZipCmd.Flags().String("transport", "", "Transport request, for a transportable package")
	gitImportZipCmd.Flags().Duration("wait", 5*time.Minute, "How long to wait for the import job (0: return once it is started)")
	gitImportZipCmd.Flags().Bool("json", false, "Emit JSON")
	gitImportStatusCmd.Flags().Bool("json", false, "Emit JSON")
	gitDeleteObjectsCmd.Flags().String("package", "", "The package (required)")
	gitDeleteObjectsCmd.Flags().String("transport", "", "Transport request, for a transportable package")
	gitDeleteObjectsCmd.Flags().Bool("delete-repo", false, "Also unregister the package's abapGit repository: only an offline one, only once the package is empty")
	gitDeleteObjectsCmd.Flags().Bool("json", false, "Emit JSON")
	gitCmd.AddCommand(gitImportZipCmd, gitImportStatusCmd, gitDeleteObjectsCmd)
	rootCmd.AddCommand(gitCmd)
}
