GOLIB ?= golib

.PHONY: source-docs-check check check-full ci cohesion inventory repository-check otel-boundary

check: source-docs-check otel-boundary
	$(GOLIB) check --local --all

check-full: source-docs-check
	$(GOLIB) check --all

ci: source-docs-check otel-boundary
	$(GOLIB) repository check
	$(GOLIB) cohesion check
	$(GOLIB) check --local --all

cohesion:
	$(GOLIB) cohesion check

inventory repository-check:
	$(GOLIB) repository check

otel-boundary:
	$(MAKE) -f verification/package.mk otel-boundary

source-docs-check:
	$(GOLIB) source-docs check --json
