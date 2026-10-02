package adt

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// --- object versions: what git_delete_objects compares under the lock -------
//
// A caller that decides in one call and deletes in another reads the
// versions first (git_object_versions) and passes them back as each item's
// expect. git_delete_objects then takes the ADT lock, reads the version
// again while it holds the lock, and deletes only when it still matches.
//
// sha256 is the one to use when it matters (ZCL_VSP_GIT_SERVICE=>
// object_sha256): lower-case hex SHA-256 of the UTF-8 text of the lines
// "<file name>=<lower-case hex SHA-256 of the file>", one per file of the
// object's abapGit serialisation in its original language only
// (TADIR-MASTERLANG), sorted (byte order), joined by LF, without a final
// LF. It covers everything abapGit serialises for the object -- but only
// the active version: an object with an inactive version (a row of the
// inactive worklist DWINACTIV for it or a part of it) is never a sha256
// match, it is "changed". When both stamp and sha256 are expected, sha256
// decides.
//
// stamp is cheaper and coarser (ZCL_VSP_GIT_SERVICE=>object_stamp):
//
//	v2:<TABLES>:<YYYYMMDDHHMMSS>:<ROWS>:<DIGEST>
//
// the newest change date and time over every dated version row of the
// object, active and inactive, the number of those rows, and the first 16
// hex digits of a SHA-256 over the rows of its tables that carry no date:
//
//	CLAS, INTF  REPOSRC (every include of the pool but CS) and REPOTEXT of
//	            the pool; digest SEOCLASSDF, SEOCLASSTX, SEOCOMPOTX
//	PROG        REPOSRC, REPOTEXT, D020S (DGEN/TGEN)
//	TABL        DD02L, DD09L, DD12L; digest DD02T, DD35L, TDDAT
//	DTEL        DD04L; digest DD04T
//	DOMA        DD01L; digest DD01T, DD07L, DD07T
//	TTYP        DD40L; digest DD40T
//	DDLS        DDDDLSRC; digest DDDDLSRCT
//
// It does NOT cover, among others: documentation (DOKHL/DOKTL) of any type;
// a program's GUI status and titles (EUDB, RSMPTEXTS) and its dynpro flow
// logic and fields beyond D020S's generation date; a class's or interface's
// SOTR texts, its exception texts beyond the text pool, its friends and
// other relations (SEOMETAREL, SEOFRIENDS), and component definitions
// beyond what their source carries; a table's field texts (DD03T),
// foreign keys (DD05S/DD08L) and enhancement category beyond DD02L's date;
// a search help's field mapping (DD36M); sub-component texts (SEOSUBCOTX,
// e.g. parameter and exception descriptions). A change only there leaves the
// stamp as it was: expect sha256 when such changes must not be deleted.
// CS (the class's whole-source include) is left out on purpose: it is
// regenerated and moves without the source changing. The resolution of the
// date part is a second. The stamp also moves on things sha256 does not
// see -- a translation in any language (the text tables are read for every
// language), and D020S's generation date, which a dynpro regeneration moves
// without a change -- so it can say "changed" where nothing that sha256
// covers changed. That errs on the safe side. Inactive versions are found
// by name in DWINACTIV, whatever the object type: an inactive object of
// another type with the same name (DTEL ZFOO for DOMA ZFOO) also counts,
// which again only errs on the safe side.

// GitExpect is the version of an object a caller saw. With sha256, sha256
// decides (and the stamp is only reported); otherwise the stamp. A version
// that cannot be read, or is missing, never matches.
type GitExpect struct {
	Stamp  string `json:"stamp,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// GitRepoExpect is the repository row a caller saw for the package.
type GitRepoExpect struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// GitDeleteOptions are DeleteGitObjectsWith's options.
type GitDeleteOptions struct {
	Transport  string
	DeleteRepo bool
	// ExpectRepo, with DeleteRepo: drop the repository row only when it is
	// exactly this one.
	ExpectRepo *GitRepoExpect
	// KeepOrder deletes the objects in the order given, not users before
	// what they use (see GitDeleteRank).
	KeepOrder bool
}

// GitChangedError says an object is no longer the version the caller
// expected; it was kept.
type GitChangedError struct {
	Type, Name string
	Observed   GitExpect
	// Inactive: it has an inactive version, which sha256 does not see.
	Inactive bool
}

func (e *GitChangedError) Error() string {
	if e.Inactive {
		return "it has an inactive version, which its sha256 does not cover (unactivated work); not deleted"
	}
	var seen []string
	if e.Observed.Stamp != "" {
		seen = append(seen, "stamp "+e.Observed.Stamp)
	}
	if e.Observed.SHA256 != "" {
		seen = append(seen, "sha256 "+e.Observed.SHA256)
	}
	what := "it has no version any more"
	if len(seen) > 0 {
		what = "it is now " + strings.Join(seen, ", ")
	}
	return fmt.Sprintf("changed since its version was read (%s); not deleted", what)
}

// gitStampTables is what the stamp of each type that has one is over.
var gitStampTables = map[string]string{
	"CLAS": "REPOSRC.REPOTEXT.SEOCLASSDF.SEOCLASSTX.SEOCOMPOTX",
	"INTF": "REPOSRC.REPOTEXT.SEOCLASSDF.SEOCLASSTX.SEOCOMPOTX",
	"PROG": "REPOSRC.REPOTEXT.D020S",
	"TABL": "DD02L.DD09L.DD12L.DD02T.DD35L.TDDAT",
	"DTEL": "DD04L.DD04T",
	"DOMA": "DD01L.DD01T.DD07L.DD07T",
	"TTYP": "DD40L.DD40T",
	"DDLS": "DDDDLSRC.DDDDLSRCT",
}

var (
	gitStampRe  = regexp.MustCompile(`^v2:([A-Z0-9.]+):([0-9]{14}):([0-9]+):([0-9a-f]{16})$`)
	gitSHA256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// GitStampTypes lists the TADIR types that have a stamp.
func GitStampTypes() string { return "CLAS, INTF, PROG, TABL, DTEL, DOMA, TTYP, DDLS" }

// normalized checks an expectation for an object of objType: at least one of
// stamp and sha256, each well formed, a stamp of a type that has one and of
// that type's table. It never drops an expectation it cannot check.
func (e *GitExpect) normalized(objType string) (*GitExpect, error) {
	if e == nil {
		return nil, nil
	}
	out := &GitExpect{Stamp: strings.TrimSpace(e.Stamp), SHA256: strings.ToLower(strings.TrimSpace(e.SHA256))}
	if out.Stamp == "" && out.SHA256 == "" {
		return nil, errors.New("expect needs stamp or sha256 (read them with git_object_versions)")
	}
	if out.Stamp != "" {
		table, ok := gitStampTables[strings.ToUpper(objType)]
		if !ok {
			return nil, fmt.Errorf("expect stamp: type %s has no stamp (only %s); use sha256", objType, GitStampTypes())
		}
		m := gitStampRe.FindStringSubmatch(out.Stamp)
		if m == nil {
			if strings.HasPrefix(out.Stamp, "v1:") {
				return nil, fmt.Errorf("expect stamp %q is a v1 stamp, which covered less; read the version again with git_object_versions", out.Stamp)
			}
			return nil, fmt.Errorf("expect stamp %q is not v2:<TABLES>:<YYYYMMDDHHMMSS>:<ROWS>:<DIGEST> as git_object_versions reports it", out.Stamp)
		}
		if m[1] != table {
			return nil, fmt.Errorf("expect stamp %q is of %s; a %s stamp is of %s", out.Stamp, m[1], objType, table)
		}
	}
	if out.SHA256 != "" && !gitSHA256Re.MatchString(out.SHA256) {
		return nil, fmt.Errorf("expect sha256 %q is not 64 hex digits", e.SHA256)
	}
	return out, nil
}

// ParseGitExpect reads an item's expect: {"stamp": "...", "sha256": "..."},
// either or both. Any other key, or a value that is not a string, is refused.
func ParseGitExpect(objType string, raw any) (*GitExpect, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expect must be {\"stamp\": ..., \"sha256\": ...}, not %T", raw)
	}
	var e GitExpect
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expect %s must be a string, not %T", k, v)
		}
		switch k {
		case "stamp":
			e.Stamp = s
		case "sha256":
			e.SHA256 = s
		default:
			return nil, fmt.Errorf("expect: unknown key %q (stamp, sha256)", k)
		}
	}
	return e.normalized(objType)
}

// sameGitExpect says whether two expectations are the same (both absent too).
func sameGitExpect(a, b *GitExpect) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ApplyGitExpectFlags attaches the CLI's --expect values to items: each
// "TYPE NAME stamp=<v>" or "TYPE NAME sha256=<h>" (both may follow one
// object). An --expect for an object that is not among items is refused.
func ApplyGitExpectFlags(items []GitDeleteItem, flags []string) error {
	for _, f := range flags {
		fields := strings.Fields(f)
		if len(fields) < 3 {
			return fmt.Errorf("--expect %q is not \"TYPE NAME stamp=<v>\" or \"TYPE NAME sha256=<h>\"", f)
		}
		typ, name := strings.ToUpper(fields[0]), strings.ToUpper(fields[1])
		raw := map[string]any{}
		for _, kv := range fields[2:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || v == "" {
				return fmt.Errorf("--expect %q: %q is not stamp=<v> or sha256=<h>", f, kv)
			}
			if _, dup := raw[k]; dup {
				return fmt.Errorf("--expect %q: %s given twice", f, k)
			}
			raw[k] = v
		}
		exp, err := ParseGitExpect(typ, raw)
		if err != nil {
			return fmt.Errorf("--expect %q: %w", f, err)
		}
		found := false
		for i := range items {
			if items[i].Type == typ && items[i].Name == name {
				if items[i].Expect != nil && !sameGitExpect(items[i].Expect, exp) {
					return fmt.Errorf("--expect %q: %s %s has another --expect already", f, typ, name)
				}
				items[i].Expect, found = exp, true
			}
		}
		if !found {
			return fmt.Errorf("--expect %q: %s %s is not among the objects to delete", f, typ, name)
		}
	}
	return nil
}

// normalized checks an expected repository: only with deleteRepo, key and
// name both given.
func (r *GitRepoExpect) normalized(deleteRepo bool) (*GitRepoExpect, error) {
	if r == nil {
		return nil, nil
	}
	out := &GitRepoExpect{Key: strings.TrimSpace(r.Key), Name: strings.TrimSpace(r.Name)}
	if !deleteRepo {
		return nil, errors.New("expect_repo is only for delete_repo: the repository is kept without it")
	}
	if out.Key == "" || out.Name == "" {
		return nil, errors.New("expect_repo needs key and name: the repository row as git_object_versions or the package's contents reported it")
	}
	return out, nil
}

// matches says whether repo is exactly the expected row.
func (r *GitRepoExpect) matches(repo *GitRepoInfo) bool {
	return repo != nil && repo.Key == r.Key && repo.Name == r.Name
}

// GitObjectVersion is an object's version as ZADT_VSP reads it.
type GitObjectVersion struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Package is the object's TADIR package (empty: no TADIR entry).
	Package string `json:"package"`
	// InPackage: the object is in the package asked about. Only then is
	// there a version.
	InPackage   bool   `json:"inPackage"`
	Stamp       string `json:"stamp,omitempty"`
	StampError  string `json:"stampError,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	SHA256Error string `json:"sha256Error,omitempty"`
	// Files is the number of files the sha256 is over.
	Files int `json:"files,omitempty"`
	// Inactive: the object or a part of it has an inactive version. nil
	// when ZADT_VSP did not say (one that predates the field): unknown,
	// never "no".
	Inactive *bool `json:"inactive,omitempty"`
}

type gitVersionsAnswer struct {
	Objects []struct {
		Type        string `json:"type"`
		Name        string `json:"name"`
		Devclass    string `json:"devclass"`
		InPackage   bool   `json:"in_package"`
		Stamp       string `json:"stamp"`
		StampError  string `json:"stamp_error"`
		SHA256      string `json:"sha256"`
		SHA256Error string `json:"sha256_error"`
		Files       int    `json:"files"`
		Inactive    *bool  `json:"inactive"`
	} `json:"objects"`
}

// GitMaxVersions is how many objects one GitObjectVersions call may name
// (ZCL_VSP_GIT_SERVICE=>c_max_versions).
const GitMaxVersions = 500

// GitObjectVersions reads the stamp -- and with withSHA the sha256 -- of
// objects of pkg through ZADT_VSP. It changes nothing. Each answer is for
// the item at the same index; an answer that does not say which object it
// is, or is missing, is an error.
func (c *Client) GitObjectVersions(ctx context.Context, ws GitService, pkg string, items []GitDeleteItem, withSHA bool) ([]GitObjectVersion, error) {
	if err := c.checkSafety(OpRead, "GitObjectVersions"); err != nil {
		return nil, err
	}
	p, err := NormalizeGitPackage(pkg)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("objects is empty: name the objects to read")
	}
	if len(items) > GitMaxVersions {
		return nil, fmt.Errorf("%d objects: at most %d per call", len(items), GitMaxVersions)
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		if !gitObjTypeRe.MatchString(it.Type) || it.Name == "" || len(it.Name) > 40 || strings.ContainsAny(it.Name, ", \"\\") {
			return nil, fmt.Errorf("object %s %s is not a TADIR type and name", it.Type, it.Name)
		}
		names = append(names, it.Type+" "+it.Name)
	}
	params := map[string]any{"package": p, "objects": strings.Join(names, ",")}
	if withSHA {
		params["sha256"] = "true"
	}
	var a gitVersionsAnswer
	if err := gitCall(ctx, ws, "object_versions", params, 5*time.Minute, &a); err != nil {
		return nil, err
	}
	if len(a.Objects) != len(items) {
		return nil, fmt.Errorf("ZADT_VSP answered %d versions for %d objects", len(a.Objects), len(items))
	}
	t := strings.TrimSpace
	out := make([]GitObjectVersion, 0, len(items))
	for i, o := range a.Objects {
		v := GitObjectVersion{Type: strings.ToUpper(t(o.Type)), Name: strings.ToUpper(t(o.Name)), Package: t(o.Devclass), InPackage: o.InPackage,
			Stamp: t(o.Stamp), StampError: t(o.StampError), SHA256: strings.ToLower(t(o.SHA256)), SHA256Error: t(o.SHA256Error), Files: o.Files, Inactive: o.Inactive}
		if v.Type != items[i].Type || v.Name != items[i].Name {
			return nil, fmt.Errorf("ZADT_VSP answered %s %s where %s %s was asked", v.Type, v.Name, items[i].Type, items[i].Name)
		}
		if v.InPackage && !strings.EqualFold(v.Package, p) {
			return nil, fmt.Errorf("ZADT_VSP says %s %s is in package %s and in %s", v.Type, v.Name, v.Package, p)
		}
		out = append(out, v)
	}
	return out, nil
}

// checkGitExpect reads item's version and compares it with item.Expect. It
// runs while the ADT lock is held. It returns what was observed, and nil
// only on a match: a *GitChangedError when the object is another version (or
// has none), any other error when the version could not be read -- never
// nil for an uncertain comparison.
func (c *Client) checkGitExpect(ctx context.Context, ws GitService, pkg string, item GitDeleteItem) (*GitExpect, error) {
	exp := item.Expect
	if exp == nil {
		return nil, errors.New("no expectation to check")
	}
	vs, err := c.GitObjectVersions(ctx, ws, pkg, []GitDeleteItem{{Type: item.Type, Name: item.Name}}, exp.SHA256 != "")
	if err != nil {
		return nil, fmt.Errorf("its version could not be read under the lock, so it was not deleted: %w", err)
	}
	v := vs[0]
	if !v.InPackage {
		where := "it has no TADIR entry any more"
		if v.Package != "" {
			where = "it is in package " + v.Package + " now"
		}
		return nil, fmt.Errorf("%s, not %s; not deleted", where, pkg)
	}
	obs := &GitExpect{}
	if exp.Stamp != "" {
		obs.Stamp = v.Stamp
	}
	if exp.SHA256 != "" {
		obs.SHA256 = v.SHA256
	}
	changed := &GitChangedError{Type: item.Type, Name: item.Name, Observed: *obs}
	if exp.SHA256 != "" {
		// sha256 decides: the stamp misses parts of an object that abapGit
		// serialises, so a stamp match never outweighs a sha256 mismatch.
		switch {
		case v.SHA256Error != "":
			return obs, fmt.Errorf("its sha256 could not be read (%s), so it was not deleted", v.SHA256Error)
		case v.Inactive == nil:
			// An older ZADT_VSP does not say; unknown is never "active only".
			return obs, errors.New("ZADT_VSP too old to say whether it has an inactive version, which its sha256 does not cover: reinstall it (vsp install zadt-vsp); not deleted")
		case *v.Inactive:
			changed.Inactive = true
			return obs, changed
		case v.SHA256 == "" || v.SHA256 != exp.SHA256:
			return obs, changed
		}
		return obs, nil
	}
	switch {
	case v.StampError != "":
		return obs, fmt.Errorf("its stamp could not be read (%s), so it was not deleted", v.StampError)
	case v.Stamp == "" || v.Stamp != exp.Stamp:
		return obs, changed
	}
	return obs, nil
}
