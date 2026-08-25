## What this changes and why

## Testing

- [ ] `go test -race ./...` passes
- [ ] `go vet ./...` is clean
- [ ] New logic has a test
- [ ] `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` still succeeds, if this touches anything outside test files

## Notes for the reviewer

Anything that needs a closer look, or a tradeoff you want a second opinion on.
