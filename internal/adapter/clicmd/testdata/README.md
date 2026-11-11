# clicmd test data

The `*.golden*` files are **verbatim copies** of files in the `commitbrief` CLI
repository. They are the contract test's ground truth for JSON schema v1 and
the ADR-0048 posting report. Never edit them by hand: when the CLI changes
them, copy the new file and update the row below (`TestContractGoldenChecksums`
fails until the SHA-256 matches).

CLI source commit: `db4a08a7b6b2278d23934ff9d7f30e12ace0bf98` (ADR-0048, not yet released; target v1.22.0).

| File here | Source in `commitbrief/` | Source blob SHA (git) | Last touched in | SHA-256 of the file |
|---|---|---|---|---|
| `v1.golden` | `internal/render/testdata/json/v1.golden` | `e6c8dc8dce41956653d1f972e4f711a622df08ea` | `d11d92f` | `b9b6518d81f500aeaa780e23765876bcabdbc050011b8e22f44ef533ec1ab2f1` |
| `v1_findings.golden` | `internal/render/testdata/json/v1_findings.golden` | `a7e55d5b3f6d5b589905045efbb46433c599573c` | `fc0ca6a` | `24d78b1c7ecb43e1adfcac25dff3edee33895b8f6f008bc69fc5906da0072e02` |
| `remote_pr_report.golden.json` | `internal/cli/testdata/remote_pr_report.golden.json` | `a474285e6add6ede53f14891423521be6f25717c` | `db4a08a` | `258ef4f01b315d98a37c768aec9d5ff3bc34c0a0bf06689ff06fe95e3d75d24a` |

`v1.golden` is the one the phase brief names; the other two are copied so the
decoder is also checked against a document with findings and against the
`remote` container.

`fakecli/` is a stand-in `commitbrief` binary. The tests build it once with
`go build` and point the adapter at it; its behaviour is driven by `FAKECLI_*`
environment variables (see the comment in `fakecli/main.go`).
