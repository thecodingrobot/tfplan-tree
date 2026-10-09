package tree

import (
	"testing"

	"github.com/ns/terraform-plan-preview/internal/planfile"
)

func TestBuildNestingAndCounts(t *testing.T) {
	rc := func(addr string, a planfile.Action) planfile.ResourceChange {
		return planfile.ResourceChange{Addr: addr, Action: a}
	}
	p := &planfile.Plan{
		Changes: []planfile.ResourceChange{
			rc("aws_instance.root", planfile.ActionCreate),
			rc("module.vpc.aws_subnet.main[0]", planfile.ActionUpdate),
			rc("module.vpc.aws_subnet.main[1]", planfile.ActionUpdate),
			rc("module.vpc.module.subnets.aws_subnet.x", planfile.ActionDeleteThenCreate),
			rc("module.vpc.data.aws_ami.x", planfile.ActionRead), // hidden by default
			rc("aws_untouched.old", planfile.ActionNoop),         // hidden by default
		},
		Drift: []planfile.ResourceChange{
			rc("module.vpc.aws_route.r[\"a.b\"]", planfile.ActionUpdate),
		},
	}

	roots := Build(p, p.Changes, p.Drift, DefaultInclude)
	if len(roots) != 3 {
		t.Fatalf("roots = %d, want 3 (drift section + module.vpc + aws_instance.root)", len(roots))
	}

	drift := roots[0]
	if drift.Kind != KindSection || len(drift.Children) != 1 {
		t.Fatalf("unexpected drift section: %+v", drift)
	}
	if drift.Children[0].Addr != "module.vpc" || len(drift.Children[0].Children) != 1 {
		t.Fatalf("drift nesting wrong: %+v", drift.Children[0])
	}

	main := roots[1:]
	if len(main) != 2 {
		t.Fatalf("main roots = %d, want 2 (module.vpc + aws_instance.root)", len(main))
	}
	var vpc *Node
	if main[0].Addr == "module.vpc" {
		vpc = main[0]
	} else {
		vpc = main[1]
	}
	// module.vpc: subnets module (1 replace), subnet.main[0..1] updates; data read filtered out
	if vpc.Counts["update"] != 2 || vpc.Counts["replace"] != 1 || vpc.Counts["read"] != 0 {
		t.Errorf("vpc counts = %v", vpc.Counts)
	}
	if len(vpc.Children) != 3 { // module.subnets + 2 subnets
		t.Errorf("vpc children = %d, want 3", len(vpc.Children))
	}
	if vpc.Children[0].Kind != KindModule { // modules sort first
		t.Errorf("first child not a module: %+v", vpc.Children[0])
	}
}

func TestAllInclude(t *testing.T) {
	p := &planfile.Plan{
		Changes: []planfile.ResourceChange{
			{Addr: "data.aws_ami.x", Action: planfile.ActionRead},
			{Addr: "aws_old.old", Action: planfile.ActionNoop},
		},
	}
	roots := Build(p, p.Changes, nil, AllInclude)
	total := 0
	for _, r := range roots {
		total += r.Counts["read"] + r.Counts["noop"]
	}
	if total != 2 {
		t.Errorf("AllInclude total = %d, want 2", total)
	}
}
