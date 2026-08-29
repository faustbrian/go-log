.PHONY: docs otel-boundary

docs:
	go test -run='^Example' ./...
	go list -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' ./... | xargs -n 1 go doc >/dev/null

otel-boundary:
	./verification/check-otel-boundary.sh
