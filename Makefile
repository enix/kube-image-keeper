# Thin shim over Taskfile.yaml (https://taskfile.dev), where the build logic lives.
#
# It only exists for tools that call make on their own: the kubebuilder CLI runs
# `make generate manifests` after scaffolding, and the e2e suite runs `make docker-build`,
# `make install`, `make deploy`... Everything else should call task directly.
#
# `make <target> VAR=value` forwards to `task <target> VAR=value`. Task is installed into
# bin/ on first use.

TASK_VERSION ?= v3.53.1
LOCALBIN := $(CURDIR)/bin
TASK := $(LOCALBIN)/task-$(TASK_VERSION)

.DEFAULT_GOAL := default
.PHONY: default $(MAKECMDGOALS)

default: $(TASK)
	@$(TASK) $(MAKEOVERRIDES)

$(TASK):
	GOBIN=$(LOCALBIN) go install github.com/go-task/task/v3/cmd/task@$(TASK_VERSION)
	mv $(LOCALBIN)/task $(TASK)
	ln -sf task-$(TASK_VERSION) $(LOCALBIN)/task

# Phony targets never match pattern rules, so the goals get an explicit rule.
ifneq ($(strip $(MAKECMDGOALS)),)
$(MAKECMDGOALS): $(TASK)
	@$(TASK) $@ $(MAKEOVERRIDES)
endif
