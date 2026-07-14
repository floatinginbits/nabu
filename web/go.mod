// Not a real Go module: this boundary stops Go tooling (go test ./..., lint)
// from descending into web/, where npm packages can ship stray .go files.
module github.com/floatinginbits/nabu/web

go 1.26
