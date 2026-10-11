GOLIB ?= golib

.PHONY: check check-full ci cohesion inventory repository-check otel-boundary

check: otel-boundary
	$(GOLIB) check --local --all

check-full:
	$(GOLIB) check --all

ci: otel-boundary
	$(GOLIB) repository check
	$(GOLIB) cohesion check
	$(GOLIB) check --local --all

cohesion:
	$(GOLIB) cohesion check

inventory repository-check:
	$(GOLIB) repository check

otel-boundary:
	$(MAKE) -f verification/package.mk otel-boundary
