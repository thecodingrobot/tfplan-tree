package planfile

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

// putVarint and msg helpers build a minimal tfplan protobuf.
func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendTag(b []byte, field int, wire wireType) []byte {
	return appendVarint(b, uint64(field)<<3|uint64(wire))
}

// appendString appends a length-delimited string field.
func appendString(b []byte, field int, s string) []byte {
	b = appendTag(b, field, wireBytes)
	b = appendVarint(b, uint64(len(s)))
	return append(b, s...)
}

// appendBytesField appends a length-delimited message field with raw payload.
func appendBytesField(b []byte, field int, payload []byte) []byte {
	b = appendTag(b, field, wireBytes)
	b = appendVarint(b, uint64(len(payload)))
	return append(b, payload...)
}

// appendVarintField appends a varint field.
func appendVarintField(b []byte, field int, v uint64) []byte {
	b = appendTag(b, field, wireVarint)
	return appendVarint(b, v)
}

func makeTestPlan(t *testing.T, path string) {
	t.Helper()

	ric := func(addr string, action uint64) []byte {
		change := appendVarintField(nil, 1, action) // Change.action
		b := appendString(nil, 13, addr)            // addr
		b = appendString(b, 8, `provider["registry.terraform.io/hashicorp/aws"]`)
		b = appendBytesField(b, 9, change)
		return b
	}

	plan := appendVarintField(nil, 14, 0) // placeholder to force varint helper use
	plan = nil
	plan = appendBytesField(plan, 3, ric("aws_instance.root", 1))
	plan = appendBytesField(plan, 3, ric("module.vpc.aws_subnet.main[0]", 3))
	plan = appendBytesField(plan, 3, ric("module.vpc.module.subnets[\"a.b\"].aws_subnet.x", 6))
	plan = appendBytesField(plan, 3, ric("module.vpc.data.aws_ami.x", 2))
	plan = appendBytesField(plan, 3, ric("aws_old.thing", 5)) // noop noise omitted; delete drift below
	plan = appendString(plan, 14, "1.9.0")
	plan = appendBytesField(plan, 18, ric("aws_instance.drifted", 5))

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("tfplan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(plan); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(path, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o644)
}

func TestOpenAndParse(t *testing.T) {
	path := t.TempDir() + "/test.tfplan"
	makeTestPlan(t, path)

	p, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.TerraformVersion != "1.9.0" {
		t.Errorf("version = %q", p.TerraformVersion)
	}
	if len(p.Changes) != 5 {
		t.Errorf("changes = %d, want 5", len(p.Changes))
	}
	if len(p.Drift) != 1 {
		t.Errorf("drift = %d, want 1", len(p.Drift))
	}
	if p.Drift[0].Addr != "aws_instance.drifted" || p.Drift[0].Action != ActionDelete {
		t.Errorf("drift[0] = %+v", p.Drift[0])
	}
	// replace action
	if p.Changes[2].Action != ActionDeleteThenCreate {
		t.Errorf("changes[2].Action = %v", p.Changes[2].Action)
	}
	if p.Changes[0].Provider == "" {
		t.Error("provider not parsed")
	}
}

func TestOpenRejectsNonPlan(t *testing.T) {
	path := t.TempDir() + "/bogus.zip"
	if err := writeFile(path, []byte("not a zip")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected error for non-plan file")
	}
}

func TestParseAddress(t *testing.T) {
	cases := []struct {
		addr         string
		wantModules  []string
		wantResource string
		wantData     bool
	}{
		{"aws_subnet.main", nil, "aws_subnet.main", false},
		{"aws_subnet.main[0]", nil, "aws_subnet.main[0]", false},
		{"aws_route_table.public[\"us-east-1a\"]", nil, "aws_route_table.public[\"us-east-1a\"]", false},
		{"data.aws_ami.x", nil, "data.aws_ami.x", true},
		{"module.vpc.aws_subnet.main[0]", []string{"module.vpc"}, "aws_subnet.main[0]", false},
		{"module.vpc.module.sub.aws_subnet.main", []string{"module.vpc", "module.sub"}, "aws_subnet.main", false},
		{"module.vpc[0].module.sub[\"a.b\"].data.aws_ami.x", []string{"module.vpc[0]", "module.sub[\"a.b\"]"}, "data.aws_ami.x", true},
		{"module.a.module.b.module.c.aws_x.y[3]", []string{"module.a", "module.b", "module.c"}, "aws_x.y[3]", false},
	}
	for _, c := range cases {
		pa := ParseAddress(c.addr)
		if len(pa.Modules) != len(c.wantModules) {
			t.Errorf("%s: modules = %v, want %v", c.addr, pa.Modules, c.wantModules)
			continue
		}
		for i := range c.wantModules {
			if pa.Modules[i] != c.wantModules[i] {
				t.Errorf("%s: modules[%d] = %q, want %q", c.addr, i, pa.Modules[i], c.wantModules[i])
			}
		}
		if pa.Resource != c.wantResource {
			t.Errorf("%s: resource = %q, want %q", c.addr, pa.Resource, c.wantResource)
		}
		if IsData(c.addr) != c.wantData {
			t.Errorf("%s: IsData = %v, want %v", c.addr, IsData(c.addr), c.wantData)
		}
	}
}

func TestActionKindShort(t *testing.T) {
	for a, want := range map[Action]string{
		ActionNoop: "=", ActionCreate: "+", ActionUpdate: "~", ActionDelete: "-",
		ActionDeleteThenCreate: "±", ActionCreateThenDelete: "±", ActionRead: "→",
	} {
		if a.Short() != want {
			t.Errorf("Action(%d).Short() = %q, want %q", a, a.Short(), want)
		}
	}
}
