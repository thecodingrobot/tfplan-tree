# tfplan-tree

A small terminal UI for terraform plan files: it shows the resources a plan
will change as a collapsible **module → resource tree**, and makes it trivial
to copy `-target` addresses (single resources *or* whole modules) so you can
run a scoped `terraform apply`.

```
tf.plan1  ·  terraform v1.16.5  ·  78 resources: +1 ~11 →4  ·  (66 unchanged hidden)

  ▾ Drift (out-of-band changes, not part of this apply)
│  ├─   ▾ module.eks_prod_useast1  ~1
│  │  └─   ~ aws_eks_cluster.this
│  └─   ▾ module.eks_prod_useast2  ~1
│     └─   ~ aws_eks_cluster.this
  ▾ module.aws_ebs_alerting_useast1  ~5
│  ├─   ~ aws_cloudwatch_metric_alarm.this["instance-iops-limit"]
│  └─   ~ aws_cloudwatch_metric_alarm.this["volume-throughput-limit"]
```

## Why

`terraform show -json plan.out` requires the terraform binary **and** every
provider plugin to decode attribute values. `tfplan-tree` parses the plan file
directly (it's a zip containing the `tfplan` protobuf), so it works anywhere —
CI artifacts, laptops without the repo, air-gapped hosts. It never decodes
attribute values, so plan secrets stay in the file.

## Usage

```
go build -o tfplan-tree ./cmd/tfplan-tree
./tfplan-tree [flags] <plan-file>      # defaults to ./tfplan
```

Interactive (when stdin/stdout are a TTY):

| key | action |
|-----|--------|
| `j`/`k`, arrows | move · `g`/`G` top/bottom · `PgUp`/`PgDn` page |
| `→`/`l`/`enter` | expand · `←`/`h` collapse (or jump to parent) |
| `e` | expand subtree · `E`/`C` expand/collapse all |
| `space` | mark a single target · `a` mark whole subtree · `A` clear marks · `tab` next mark |
| `y` or `c` | copy `terraform apply -target='…' -target='…'` (marked rows, else cursor) |
| `d` | copy `terraform destroy -target='…'` |
| `Y` | copy bare addresses, space separated |
| `/` | filter tree by substring · `enter` apply · `esc` clear |
| `n` | toggle hidden no-op / data-read resources |
| `?` | help · `q` quit |

Marking a **module** node copies the module address — targeting it applies
everything inside it.

Copying uses local clipboard tools (`pbcopy`, `wl-copy`, `xclip`, `xsel`,
`termux-clipboard-set`, `clip.exe`) when available and falls back to the
OSC52 escape sequence, which also works over SSH and inside tmux.

Non-interactive:

```
tfplan-tree -no-tui plan.out    # print the tree (works in pipelines/CI)
tfplan-tree -flat plan.out      # print one changed address per line
tfplan-tree -all plan.out       # include unchanged + data-read resources
```

`-flat` output is handy for shell loops:

```
terraform apply $(tfplan-tree -flat plan.out | grep vpc_prod | sed "s/^/-target='/;s/$/'/")
```

## Install

```
go install github.com/ns/terraform-plan-preview/cmd/tfplan-tree@latest
```

## Notes

- Action legend: `+` create, `~` update, `-` delete, `±` replace (delete+create),
  `→` data read, `⊗` forget, `=` no-op.
- The **Drift** section lists out-of-band changes detected during refresh;
  they are not part of the planned apply.
- Tested with plans from terraform 1.x. The plan protobuf is an internal
  format; field numbers used here have been stable since 0.12 (older plans
  with per-part address fields are not supported).

## Development

```
go test ./...      # hermetic tests (synthetic plan zips, address parser)
```

Layout:

- `internal/planfile` — plan zip reader + minimal protobuf wire parser
- `internal/tree` — module/resource hierarchy from parsed addresses
- `internal/tui` — bubbletea UI, clipboard helpers
- `cmd/tfplan-tree` — CLI entrypoint
