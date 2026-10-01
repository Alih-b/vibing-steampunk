package saprfc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oisee/open-rfc-go/rfc"
)

// Probe is a fingerprint of a system, gathered over classic RFC. It answers the
// questions you ask before trusting a system with real work: what is it, what is
// installed on it, which helpers are present, and what may this user actually do.
// ADT cannot answer the last one at all; RFC_SIMULATE_AUTH_CHECK can, without
// executing anything.
type Probe struct {
	Destination Destination       `json:"destination"`
	System      SystemInfo        `json:"system"`
	Components  []Component       `json:"components,omitempty"`
	Helpers     map[string]bool   `json:"helpers"`
	Authorized  map[string]bool   `json:"authorized,omitempty"`
	Timings     map[string]string `json:"timings"`
	Warnings    []string          `json:"warnings,omitempty"`
}

// Destination records where the probe went, so a saved report is unambiguous.
type Destination struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Sysnr  string `json:"sysnr"`
	Client string `json:"client"`
	User   string `json:"user"`
}

// SystemInfo is the RFC_SYSTEM_INFO subset worth reporting.
type SystemInfo struct {
	SystemID     string `json:"system_id"`
	Release      string `json:"release"`
	Kernel       string `json:"kernel"`
	Host         string `json:"host"`
	Database     string `json:"database"`
	DatabaseHost string `json:"database_host"`
	OS           string `json:"os"`
	Codepage     string `json:"codepage"`
	Unicode      bool   `json:"unicode"`
	IPAddress    string `json:"ip_address"`
	Timezone     string `json:"timezone"`
}

// Component is one installed software component and its level.
type Component struct {
	Name    string `json:"name"`
	Release string `json:"release"`
	Level   string `json:"level"`
}

// helperObjects are the repository objects whose presence changes what vsp can do.
var helperObjects = map[string]struct{ object, name string }{
	"zadt_vsp": {"CLAS", "ZCL_VSP_APC_HANDLER"},
	"abapgit":  {"CLAS", "ZCL_ABAPGIT_OBJECTS"},
}

// probedFunctions are checked for existence and for this user's authorization.
// They are the modules vsp itself relies on, so a red line here explains a
// failure that would otherwise look like a bug.
var probedFunctions = []string{
	"RFC_READ_TABLE",
	"RFC_GET_FUNCTION_INTERFACE",
	"RFC_METADATA_GET",
	"Z_ABAPGIT_SERIALIZE_PACKAGE",
	"TPDA_ADT_START_LISTENER",
	"SUBST_START_REPORT_IN_BATCH",
}

// RunProbe fingerprints the system behind c. It never writes anything and keeps
// going when an individual probe fails, recording the reason as a warning.
func RunProbe(ctx context.Context, c *rfc.Client, dest Params) (*Probe, error) {
	p := &Probe{
		Destination: Destination{Host: dest.Host, Port: dest.Port, Sysnr: dest.Sysnr, Client: dest.Client, User: dest.User},
		Helpers:     map[string]bool{},
		Authorized:  map[string]bool{},
		Timings:     map[string]string{},
	}

	start := time.Now()
	info, err := c.Call(ctx, "RFC_SYSTEM_INFO", nil)
	p.Timings["system_info"] = took(start)
	if err != nil {
		return nil, fmt.Errorf("RFC_SYSTEM_INFO: %w", err)
	}
	if m, ok := info.Get("RFCSI_EXPORT").(map[string]any); ok {
		p.System = SystemInfo{
			SystemID:     str(m["RFCSYSID"]),
			Release:      str(m["RFCSAPRL"]),
			Kernel:       str(m["RFCKERNRL"]),
			Host:         str(m["RFCHOST"]),
			Database:     str(m["RFCDBSYS"]),
			DatabaseHost: str(m["RFCDBHOST"]),
			OS:           str(m["RFCOPSYS"]),
			Codepage:     str(m["RFCCHARTYP"]),
			// 4102/4103 are the UTF-16 code pages a Unicode system reports.
			Unicode:   strings.HasPrefix(str(m["RFCCHARTYP"]), "410"),
			IPAddress: str(m["RFCIPADDR"]),
			Timezone:  str(m["RFCTZONE"]),
		}
	}

	start = time.Now()
	comps, err := ReadTable(ctx, c, "CVERS", "", []string{"COMPONENT", "RELEASE", "EXTRELEASE"}, 0)
	p.Timings["components"] = took(start)
	if err != nil {
		p.Warnings = append(p.Warnings, "installed components unavailable: "+err.Error())
	} else {
		for _, row := range comps {
			p.Components = append(p.Components, Component{Name: row["COMPONENT"], Release: row["RELEASE"], Level: row["EXTRELEASE"]})
		}
		sort.Slice(p.Components, func(i, j int) bool { return p.Components[i].Name < p.Components[j].Name })
	}

	start = time.Now()
	for key, obj := range helperObjects {
		where := fmt.Sprintf("PGMID = 'R3TR' AND OBJECT = '%s' AND OBJ_NAME = '%s'", obj.object, obj.name)
		rows, err := ReadTable(ctx, c, "TADIR", where, []string{"OBJ_NAME"}, 1)
		if err != nil {
			p.Warnings = append(p.Warnings, "helper probe "+key+": "+err.Error())
			continue
		}
		p.Helpers[key] = len(rows) > 0
	}
	p.Timings["helpers"] = took(start)

	start = time.Now()
	present, err := rfcEnabled(ctx, c, probedFunctions)
	if err != nil {
		p.Warnings = append(p.Warnings, "function inventory unavailable: "+err.Error())
	}
	for _, fn := range probedFunctions {
		if !present[fn] {
			continue // absent or not remote-enabled: nothing to authorize
		}
		res, err := c.Call(ctx, "RFC_SIMULATE_AUTH_CHECK", rfc.Params{"IV_FM": fn, "IV_USER": dest.User})
		if err != nil {
			p.Warnings = append(p.Warnings, "authorization check for "+fn+": "+err.Error())
			continue
		}
		p.Authorized[fn] = strings.TrimSpace(fmt.Sprint(res.Get("EV_AUTHORIZED"))) == "X"
	}
	p.Timings["authorizations"] = took(start)

	return p, nil
}

// rfcEnabled reports which of the given function modules exist and are
// remote-enabled (TFDIR-FMODE = 'R').
func rfcEnabled(ctx context.Context, c *rfc.Client, names []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, n := range names {
		rows, err := ReadTable(ctx, c, "TFDIR", fmt.Sprintf("FUNCNAME = '%s' AND FMODE = 'R'", n), []string{"FUNCNAME"}, 1)
		if err != nil {
			return out, err
		}
		out[n] = len(rows) > 0
	}
	return out, nil
}

// Text renders the probe for a terminal.
func (p *Probe) Text() string {
	var b strings.Builder
	s := p.System
	fmt.Fprintf(&b, "%s  %s:%d (sysnr %s, client %s) as %s\n", s.SystemID, p.Destination.Host, p.Destination.Port, p.Destination.Sysnr, p.Destination.Client, p.Destination.User)
	fmt.Fprintf(&b, "  release %s · kernel %s · %s on %s · db %s@%s · codepage %s%s\n",
		s.Release, s.Kernel, s.Host, s.OS, s.Database, s.DatabaseHost, s.Codepage, unicodeNote(s.Unicode))

	if len(p.Components) > 0 {
		fmt.Fprintf(&b, "  components: ")
		parts := make([]string, 0, len(p.Components))
		for _, c := range p.Components {
			parts = append(parts, fmt.Sprintf("%s %s SP%s", c.Name, c.Release, strings.TrimLeft(c.Level, "0")))
		}
		fmt.Fprintf(&b, "%s\n", strings.Join(parts, ", "))
	}

	fmt.Fprintf(&b, "  helpers:")
	for _, k := range sortedKeys(p.Helpers) {
		fmt.Fprintf(&b, " %s=%s", k, yesNo(p.Helpers[k]))
	}
	b.WriteString("\n")

	if len(p.Authorized) > 0 {
		fmt.Fprintf(&b, "  authorizations (%s):\n", p.Destination.User)
		for _, k := range sortedKeys(p.Authorized) {
			fmt.Fprintf(&b, "    %-30s %s\n", k, yesNo(p.Authorized[k]))
		}
	}

	fmt.Fprintf(&b, "  timings:")
	for _, k := range sortedKeys(p.Timings) {
		fmt.Fprintf(&b, " %s=%s", k, p.Timings[k])
	}
	b.WriteString("\n")

	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "  ! %s\n", w)
	}
	return b.String()
}

func unicodeNote(u bool) string {
	if u {
		return " (Unicode)"
	}
	return " (non-Unicode)"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func str(v any) string {
	if v == nil {
		return "" // a field the result does not carry, not the word "<nil>"
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func took(start time.Time) string { return fmt.Sprintf("%dms", time.Since(start).Milliseconds()) }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
