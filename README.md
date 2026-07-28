# chess6502

A maximum-strength chess engine for the Apple IIe (1 MHz 6502, 128K),
built to demonstrate how far chess engine theory has advanced since the
1980s. Target: measurably stronger than anything that ever ran on a
1 MHz 6502.

- **The plan**: [docs/plan.md](docs/plan.md)
- **Decisions**: [docs/decisions.md](docs/decisions.md)
- **Dev loop & testing**: [docs/testing.md](docs/testing.md)
- **Match & measurement log**: [docs/results.md](docs/results.md)
- **Research notes** (with citations): [docs/research/](docs/research/)

## Quick start

Requires: Go and cc65 (`brew install cc65`). Dependencies
([go6502](https://github.com/zellyn/go6502),
[goapple2](https://github.com/zellyn/goapple2)) resolve as normal Go
modules; to hack on them locally, create a `go.work` pointing at sibling
checkouts (`go work init . ../go6502 ../goapple2`).

```sh
make        # assemble + run the smoke tests, build the engine, run all tests
make dsk    # build asm/8fish.dsk, a bootable Apple II floppy you can play
```

`make dsk` additionally needs [diskii](https://github.com/zellyn/diskii)
(`go install github.com/zellyn/diskii@latest`), which writes peterferrie's
"Standard Delivery" boot loader. The disk carries the copier, the on-device
UI, the resident opening book and the engine as one contiguous image; see
[docs/ui-design.md §12.2.1](docs/ui-design.md). Boot it in slot 6 of an Apple
IIe (128K) and play.

`cmd/a2run` runs 6502 binaries against the cycle-accurate go6502 core and
the Apple IIe 128K memory model (`goapple2/iie`, validated against
[a2audit](https://github.com/zellyn/a2audit)'s hardware-verified tests).
