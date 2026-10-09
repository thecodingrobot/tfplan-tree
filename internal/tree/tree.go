// Package tree builds a module → resource hierarchy from plan changes.
package tree

import (
	"sort"

	"github.com/ns/terraform-plan-preview/internal/planfile"
)

type Kind int

const (
	KindModule Kind = iota
	KindResource
	KindSection // synthetic root (e.g. "Drift")
)

// Node is one element of the displayed tree.
type Node struct {
	Label    string // relative display label, e.g. "flux_bootstrap" / "aws_subnet.main[0]"
	Addr     string // full terraform address, usable with -target
	Kind     Kind
	Change   *planfile.ResourceChange // set for KindResource
	Children []*Node
	Counts   map[string]int // action kind → count (aggregated over subtree)
}

// Include decides whether a change appears in the tree.
type Include func(*planfile.ResourceChange) bool

// DefaultInclude hides no-ops and data-source reads (plan noise).
func DefaultInclude(rc *planfile.ResourceChange) bool {
	if planfile.IsData(rc.Addr) && rc.Action.Kind() == "read" {
		return false
	}
	return rc.Action.Kind() != "noop"
}

// AllInclude includes everything.
func AllInclude(*planfile.ResourceChange) bool { return true }

// Build constructs the forest: the main changes at the top level, optionally
// preceded by a synthetic "Drift" section when out-of-band changes exist.
func Build(p *planfile.Plan, changes, drift []planfile.ResourceChange, inc Include) []*Node {
	var roots []*Node
	if sec := buildSection(p, drift, inc, "Drift (out-of-band changes, not part of this apply)"); sec != nil {
		roots = append(roots, sec)
	}
	if main := buildSection(p, changes, inc, ""); main != nil {
		roots = append(roots, main.Children...)
	}
	return roots
}

// buildSection creates a forest for one set of changes. If label is non-empty
// the forest is wrapped in a single synthetic section node.
func buildSection(p *planfile.Plan, changes []planfile.ResourceChange, inc Include, label string) *Node {
	root := &Node{Counts: map[string]int{}}
	modules := map[string]*Node{} // accumulated module path → node

	moduleFor := func(addr string) *Node {
		pa := planfile.ParseAddress(addr)
		parent := root
		cur := ""
		for _, seg := range pa.Modules {
			if cur == "" {
				cur = seg
			} else {
				cur += "." + seg
			}
			n, ok := modules[cur]
			if !ok {
				n = &Node{Kind: KindModule, Label: seg, Addr: cur, Counts: map[string]int{}}
				modules[cur] = n
				parent.Children = append(parent.Children, n)
			}
			parent = n
		}
		return parent
	}

	for i := range changes {
		rc := &changes[i]
		if !inc(rc) {
			continue
		}
		parent := moduleFor(rc.Addr)
		parent.Children = append(parent.Children, &Node{
			Kind:   KindResource,
			Label:  planfile.ParseAddress(rc.Addr).Resource,
			Addr:   rc.Addr,
			Change: rc,
			Counts: map[string]int{rc.Action.Kind(): 1},
		})
	}
	if len(root.Children) == 0 {
		return nil
	}
	finalize(root)
	if label == "" {
		return root
	}
	sec := &Node{Kind: KindSection, Label: label, Counts: map[string]int{}}
	mergeCounts(sec, root)
	sec.Children = root.Children
	return sec
}

func mergeCounts(dst, src *Node) {
	for k, v := range src.Counts {
		dst.Counts[k] += v
	}
}

// finalize sorts children (modules first, then resources) and aggregates counts.
func finalize(n *Node) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if (a.Kind == KindResource) != (b.Kind == KindResource) {
			return a.Kind != KindResource // modules first
		}
		return a.Addr < b.Addr
	})
	for _, c := range n.Children {
		finalize(c)
		mergeCounts(n, c)
	}
}
