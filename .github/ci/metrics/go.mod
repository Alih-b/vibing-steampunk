// A module of its own, so the CI metrics tool's two dependencies never reach
// vsp's go.mod. Go skips directories that start with a dot, so `go build ./...`
// at the repository root does not see this.
module github.com/oisee/vibing-steampunk/.github/ci/metrics

go 1.24.0

require (
	github.com/fzipp/gocyclo v0.6.0
	github.com/uudashr/gocognit v1.2.1
)

require golang.org/x/tools v0.42.0 // indirect
