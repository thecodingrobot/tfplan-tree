// Package planfile reads terraform plan files (the zip written by
// `terraform plan -out=...`) without requiring the terraform binary or
// provider plugins. Only the fields needed to list resource changes are
// decoded; configuration and attribute values are never touched.
//
// Field numbers below mirror hashicorp/terraform internal/plans/planproto
// (planfile.proto).
package planfile

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
)

// Action mirrors planproto.Action.
type Action int

const (
	ActionNoop Action = 0
	// 4 unused
	ActionCreate           Action = 1
	ActionRead             Action = 2
	ActionUpdate           Action = 3
	ActionDelete           Action = 5
	ActionDeleteThenCreate Action = 6 // replace
	ActionCreateThenDelete Action = 7 // replace
	ActionForget           Action = 8
	ActionCreateThenForget Action = 9
	ActionForgetThenCreate Action = 10
)

// Short returns a short label: + create, ~ update, - delete, ± replace, ...
func (a Action) Short() string {
	switch a {
	case ActionCreate:
		return "+"
	case ActionRead:
		return "→"
	case ActionUpdate:
		return "~"
	case ActionDelete:
		return "-"
	case ActionDeleteThenCreate, ActionCreateThenDelete:
		return "±"
	case ActionForget:
		return "⊗"
	case ActionCreateThenForget, ActionForgetThenCreate:
		return "+⊗"
	default:
		return "="
	}
}

// Kind categorizes actions for filtering and coloring.
func (a Action) Kind() string {
	switch a {
	case ActionCreate:
		return "create"
	case ActionRead:
		return "read"
	case ActionUpdate:
		return "update"
	case ActionDelete:
		return "delete"
	case ActionDeleteThenCreate, ActionCreateThenDelete:
		return "replace"
	case ActionForget, ActionCreateThenForget, ActionForgetThenCreate:
		return "forget"
	default:
		return "noop"
	}
}

// ResourceChange is one planned resource instance change.
type ResourceChange struct {
	Addr         string // e.g. module.vpc.aws_subnet.main[0]
	PrevRunAddr  string
	DeposedKey   string
	Provider     string
	Action       Action
	ActionReason int32
}

// Plan is the decoded subset of a plan file we care about.
type Plan struct {
	Path             string
	TerraformVersion string
	Errored          bool
	Applyable        bool
	Changes          []ResourceChange // planned changes (field 3)
	Drift            []ResourceChange // out-of-band drift detected at refresh (field 18)
}

// Open parses the plan file at path.
func Open(path string) (*Plan, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open plan: %w", err)
	}
	defer zr.Close()

	p := &Plan{Path: path}
	var tfplan []byte
	for _, f := range zr.File {
		if f.Name == "tfplan" {
			tfplan, err = readZipFile(f)
			if err != nil {
				return nil, err
			}
		}
	}
	if tfplan == nil {
		return nil, fmt.Errorf("%s: not a terraform plan file (no tfplan entry)", path)
	}
	if err := p.parsePlan(tfplan); err != nil {
		return nil, err
	}
	return p, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", f.Name, err)
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// parsePlan decodes the tfplan protobuf (planproto.Plan).
func (p *Plan) parsePlan(data []byte) error {
	fields, err := parseFields(data)
	if err != nil {
		return err
	}
	for _, f := range fields {
		switch f.num {
		case 3: // repeated ResourceInstanceChange resource_changes
			rc, err := parseResourceChange(f.b)
			if err != nil {
				return err
			}
			p.Changes = append(p.Changes, rc)
		case 18: // repeated ResourceInstanceChange resource_drift
			rc, err := parseResourceChange(f.b)
			if err != nil {
				return err
			}
			p.Drift = append(p.Drift, rc)
		case 14: // string terraform_version
			p.TerraformVersion = string(f.b)
		case 20: // bool errored
			p.Errored = f.v != 0
		case 25: // bool applyable
			p.Applyable = f.v != 0
		}
	}
	return nil
}

// parseResourceChange decodes planproto.ResourceInstanceChange.
func parseResourceChange(data []byte) (ResourceChange, error) {
	var rc ResourceChange
	fields, err := parseFields(data)
	if err != nil {
		return rc, err
	}
	for _, f := range fields {
		switch f.num {
		case 7: // string deposed_key
			rc.DeposedKey = string(f.b)
		case 8: // string provider
			rc.Provider = string(f.b)
		case 9: // Change change
			cfields, err := parseFields(f.b)
			if err != nil {
				return rc, err
			}
			for _, cf := range cfields {
				if cf.num == 1 { // Action action
					rc.Action = Action(cf.v)
				}
			}
		case 12: // ResourceInstanceActionReason action_reason
			rc.ActionReason = int32(f.v)
		case 13: // string addr
			rc.Addr = string(f.b)
		case 14: // string prev_run_addr
			rc.PrevRunAddr = string(f.b)
		}
	}
	return rc, nil
}

// ParsedAddress is the decomposed form of a resource instance address.
type ParsedAddress struct {
	Modules  []string // module segments in order, e.g. ["module.vpc", "module.sub[0]"]
	Resource string   // e.g. "aws_subnet.main[0]" or "data.aws_ami.x"
}

// ParseAddress splits a terraform resource address into its module path and
// resource part. It understands count/for_each indices (including keys that
// contain dots) and data sources.
func ParseAddress(addr string) ParsedAddress {
	segs := splitSegments(addr)
	var pa ParsedAddress
	i := 0
	for i < len(segs) && segs[i] == "module" && i+1 < len(segs) {
		pa.Modules = append(pa.Modules, "module."+segs[i+1])
		i += 2
	}
	if i < len(segs) {
		pa.Resource = strings.Join(segs[i:], ".")
	}
	return pa
}

// splitSegments splits an address on '.' outside of [...] index brackets,
// keeping brackets attached to their segment.
func splitSegments(addr string) []string {
	var segs []string
	var cur strings.Builder
	depth := 0
	for _, r := range addr {
		switch r {
		case '[':
			depth++
			cur.WriteRune(r)
		case ']':
			depth--
			cur.WriteRune(r)
		case '.':
			if depth == 0 {
				segs = append(segs, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	segs = append(segs, cur.String())
	return segs
}

// IsData reports whether the address refers to a data source
// (root: `data.x.y`, inside modules: `module.m.data.x.y`).
func IsData(addr string) bool {
	return strings.HasPrefix(addr, "data.") || strings.Contains(addr, ".data.")
}
