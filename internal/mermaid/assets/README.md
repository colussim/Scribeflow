mermaid.min.js is downloaded during the build (see Makefile/build.sh) and
embedded in the binary through go:embed. The generated file is not tracked in
the repository. Run `./scripts/fetch-mermaid.sh` before `go build` when it is
missing from this directory.
