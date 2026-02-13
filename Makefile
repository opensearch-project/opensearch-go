SHELL := /bin/bash

# Tool versions
GOLANGCI_LINT_VERSION := v2.8.0

GOLANGCI_LINT_BUILD_TAGS := "integration core plugins plugin_security plugin_index_management multinode"

##@ Format project using goimports tool
format:
	goimports -w .;

##@ Test
test-unit:  ## Run unit tests
	@printf "\033[2m-> Running unit tests...\033[0m\n"
ifdef race
	$(eval testunitargs += "-race")
endif
	$(eval testunitargs += "-cover" "./..." "-args" "-test.gocoverdir=$(PWD)/tmp/unit")
	@rm -rf $(PWD)/tmp/unit
	@mkdir -p $(PWD)/tmp/unit
	@echo "go test -v" $(testunitargs); \
	go test -v $(testunitargs);
ifdef coverage
	@go tool covdata textfmt -i=$(PWD)/tmp/unit -o $(PWD)/tmp/unit.cov
endif
test: test-unit

test-integ:  ## Run integration tests
	@printf "\033[2m-> Running integration tests...\033[0m\n"
	$(eval testintegtags += "integration,core,plugins")
	$(eval testintegdir ?= integration)
ifdef multinode
	$(eval testintegtags += "multinode")
endif
ifdef race
	$(eval testintegargs += "-race")
endif
	$(eval TEST_PARALLEL ?= $(shell ncpu=$$(go env GOMAXPROCS 2>/dev/null); [ -z "$$ncpu" ] && ncpu=$$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 4); parallel=$$((ncpu - 1)); [ $$parallel -lt 1 ] && parallel=1; echo $$parallel))
	$(eval testintegargs += "-cover" "-tags=$(testintegtags)" "-timeout=10m" "-parallel=$(TEST_PARALLEL)" "./..." "-args" "-test.gocoverdir=$(PWD)/tmp/$(testintegdir)")
	@rm -rf $(PWD)/tmp/$(testintegdir)
	@mkdir -p $(PWD)/tmp/$(testintegdir)
	@echo "go test -v" $(testintegargs); \
	go test -v $(testintegargs);
ifdef coverage
	@go tool covdata textfmt -i=$(PWD)/tmp/$(testintegdir) -o $(PWD)/tmp/$(testintegdir).cov
endif

test-integ-core:  ## Run base integration tests
	@$(MAKE) test-integ testintegtags=integration,core testintegdir=integration-core

test-integ-plugins:  ## Run plugin integration tests
	@$(MAKE) test-integ testintegtags=integration,plugins testintegdir=integration-plugins

test-integ-secure: ##Run secure integration tests
	@SECURE_INTEGRATION=true $(MAKE) test-integ

test-integ-core-secure:  ## Run secure base integration tests
	@SECURE_INTEGRATION=true $(MAKE) test-integ testintegtags=integration,core

test-integ-plugins-secure:  ## Run secure plugin integration tests
	@SECURE_INTEGRATION=true $(MAKE) test-integ testintegtags=integration,plugins

test-all:  ## Run all tests with all build tags (unit + integration)
	@printf "\033[2m-> Running all unit tests...\033[0m\n"
	@$(MAKE) test-unit
	@printf "\033[2m-> Running all integration tests with all tags...\033[0m\n"
	@$(MAKE) test-integ testintegtags=integration,core,plugins,plugin_security,plugin_index_management,multinode

test-race:  ## Run all tests with race detection enabled
	@printf "\033[2m-> Running all unit tests with race detection...\033[0m\n"
	@$(MAKE) test-unit race=true
	@printf "\033[2m-> Running all integration tests with race detection and all tags...\033[0m\n"
	@$(MAKE) test-integ race=true testintegtags=integration,core,plugins,plugin_security,plugin_index_management,multinode

test-bench:  ## Run benchmarks
	@printf "\033[2m-> Running benchmarks...\033[0m\n"
	go test -run=none -bench=. -benchmem ./...

coverage:  ## Print test coverage report
	@$(MAKE) gen-coverage
	@go tool cover -func=$(PWD)/tmp/total.cov
	@printf "\033[0m--------------------------------------------------------------------------------\n\033[0m"

coverage-html: ## Open test coverage report in browser
	@$(MAKE) gen-coverage
	@go tool cover -html $(PWD)/tmp/total.cov

gen-coverage:  ## Generate test coverage report
	@printf "\033[2m-> Generating test coverage report...\033[0m\n"
	@rm -rf tmp
	@mkdir tmp
	@mkdir tmp/unit
	@mkdir tmp/integration
	@$(MAKE) test-unit coverage=true
	@$(MAKE) test-integ coverage=true
	@$(MAKE) build-coverage

build-coverage:
	@go tool covdata textfmt -i=$(PWD)/tmp/unit,$(PWD)/tmp/integration -o $(PWD)/tmp/total.cov

##@ Development
lint:  ## Run lint on the package
	@$(MAKE) linters

lint.local:  ## Run lint locally (not in Docker) with all build tags
	@printf "\033[2m-> Running golangci-lint locally with all build tags...\033[0m\n"
	golangci-lint run --fix --build-tags $(GOLANGCI_LINT_BUILD_TAGS) --timeout=5m -v ./...

package := "prettier"
lint.markdown:
	@printf "\033[2m-> Checking node installed...\033[0m\n"
	if type node > /dev/null 2>&1 && which node > /dev/null 2>&1 ; then \
		node -v; \
		echo -e "\033[33m Node is installed, continue...\033[0m\n"; \
	else \
		echo -e "\033[31m Please install node\033[0m\n"; \
		exit 1; \
	fi
	@printf "\033[2m-> Checking npm installed...\033[0m\n"
	if type npm > /dev/null 2>&1 && which npm > /dev/null 2>&1 ; then \
		npm -v; \
		echo -e "\033[33m NPM is installed, continue...\033[0m\n"; \
	else \
		echo -e "\033[31m Please install npm\033[0m\n"; \
		exit 1; \
	fi
	@printf "\033[2m-> Checking $(package) installed...\033[0m\n"
	if [ `npm list -g | grep -c $(package)` -eq 0 -o ! -d node_module ]; then \
		echo -e "\033[33m Installing $(package)...\033[0m"; \
		npm install -g $(package) --no-shrinkwrap; \
	fi
	@printf "\033[2m-> Running markdown lint...\033[0m\n"
	if npx $(package) --prose-wrap never --check **/*.md; [ $$? -ne 0 ]; then \
		echo -e "\033[32m-> Found invalid files. Want to auto-format invalid files? (y/n) \033[0m"; \
		read RESP; \
		if [ "$$RESP" = "y" ] || [ "$$RESP" = "Y" ]; then \
		  echo -e "\033[33m Formatting...\033[0m"; \
		  npx $(package) --prose-wrap never --write **/*.md; \
		  echo -e "\033[34m \nAll invalid files are formatted\033[0m"; \
		else \
		  echo -e "\033[33m Unfortunately you are cancelled auto fixing. But we will definitely fix it in the pipeline\033[0m"; \
		fi \
	fi


backport: ## Backport one or more commits from main into version branches
ifeq ($(origin commits), undefined)
	@echo "Missing commit(s), exiting..."
	@exit 2
endif
ifndef branches
	$(eval branches_list = '1.x')
else
	$(eval branches_list = $(shell echo $(branches) | tr ',' ' ') )
endif
	$(eval commits_list = $(shell echo $(commits) | tr ',' ' '))
	@printf "\033[2m-> Backporting commits [$(commits)]\033[0m\n"
	@{ \
		set -e -o pipefail; \
		for commit in $(commits_list); do \
			git show --pretty='%h | %s' --no-patch $$commit; \
		done; \
		echo ""; \
		for branch in $(branches_list); do \
			printf "\033[2m-> $$branch\033[0m\n"; \
			git checkout $$branch; \
			for commit in $(commits_list); do \
				git cherry-pick -x $$commit; \
			done; \
			git status --short --branch; \
			echo ""; \
		done; \
		printf "\033[2m-> Push updates to Github:\033[0m\n"; \
		for branch in $(branches_list); do \
			echo "git push --verbose origin $$branch"; \
		done; \
	}

release: ## Release a new version to Github
	$(eval branch = $(shell git rev-parse --abbrev-ref HEAD))
	$(eval current_version = $(shell cat internal/version/version.go | sed -Ee 's/const Client = "(.*)"/\1/' | tail -1))
	@printf "\033[2m-> [$(branch)] Current version: $(current_version)...\033[0m\n"
ifndef version
	@printf "\033[31m[!] Missing version argument, exiting...\033[0m\n"
	@exit 2
endif
ifeq ($(version), "")
	@printf "\033[31m[!] Empty version argument, exiting...\033[0m\n"
	@exit 2
endif
	@printf "\033[2m-> [$(branch)] Creating version $(version)...\033[0m\n"
	@{ \
		set -e -o pipefail; \
		cp internal/version/version.go internal/version/version.go.OLD && \
		cat internal/version/version.go.OLD | sed -e 's/Client = ".*"/Client = "$(version)"/' > internal/version/version.go && \
		go vet internal/version/version.go && \
		go fmt internal/version/version.go && \
		git diff --color-words internal/version/version.go | tail -n 1; \
	}
	@{ \
		set -e -o pipefail; \
		printf "\033[2m-> Commit and create Git tag? (y/n): \033[0m\c"; \
		read continue; \
		if [ "$$continue" = "y" ]; then \
			git add internal/version/version.go && \
			git commit --no-status --quiet --message "Release $(version)" && \
			git tag --annotate v$(version) --message 'Release $(version)'; \
			printf "\033[2m-> Push `git show --pretty='%h (%s)' --no-patch HEAD` to Github:\033[0m\n\n"; \
			printf "\033[1m  git push origin HEAD && git push origin v$(version)\033[0m\n\n"; \
			mv internal/version/version.go.OLD internal/version/version.go && \
			git add internal/version/version.go && \
			original_version=`cat internal/version/version.go | sed -ne 's;^const Client = "\(.*\)"$$;\1;p'` && \
			git commit --no-status --quiet --message "Update version to $$original_version"; \
			printf "\033[2m-> Version updated to [$$original_version].\033[0m\n\n"; \
		else \
			echo "Aborting..."; \
			rm internal/version/version.go.OLD; \
			exit 1; \
		fi; \
	}

godoc: ## Display documentation for the package
	@printf "\033[2m-> Generating documentation...\033[0m\n"
	@echo "* http://localhost:6060/pkg/github.com/opensearch-project/opensearch-go"
	@echo "* http://localhost:6060/pkg/github.com/opensearch-project/opensearch-go/opensearchapi"
	@echo "* http://localhost:6060/pkg/github.com/opensearch-project/opensearch-go/opensearchtransport"
	@echo "* http://localhost:6060/pkg/github.com/opensearch-project/opensearch-go/opensearchutil"
	@printf "\n"
	godoc --http=localhost:6060 --play

cluster.build:
	@$(MAKE) cluster.docker-build

cluster.start:
	@$(MAKE) cluster.docker-up
	@$(MAKE) cluster.wait-ready
	@$(MAKE) cluster.get-cert

cluster.stop:
	docker compose --project-directory .ci/opensearch down;

cluster.docker-build:
	@# Determine version-specific settings
	$(eval OPENSEARCH_VERSION ?= latest)
	$(eval SECURE_INTEGRATION ?= false)
	$(eval version_major := $(shell \
		if [ "$(OPENSEARCH_VERSION)" = "latest" ]; then \
			echo "2"; \
		else \
			echo "$(OPENSEARCH_VERSION)" | awk -F. '{print $$1}'; \
		fi \
	))
	$(eval manager_role := $(shell \
		if [ "$(version_major)" = "1" ]; then \
			echo "master"; \
		else \
			echo "cluster_manager"; \
		fi \
	))
	@echo "Building OpenSearch $(OPENSEARCH_VERSION) with role: $(manager_role), secure: $(SECURE_INTEGRATION)"
	OPENSEARCH_MANAGER_ROLE=$(manager_role) OPENSEARCH_MANAGER_SETTING=$(manager_role) \
		docker compose --project-directory .ci/opensearch build --pull

cluster.docker-up:
	@# Determine version-specific settings
	$(eval OPENSEARCH_VERSION ?= latest)
	$(eval SECURE_INTEGRATION ?= false)
	$(eval version_major := $(shell \
		if [ "$(OPENSEARCH_VERSION)" = "latest" ]; then \
			echo "2"; \
		else \
			echo "$(OPENSEARCH_VERSION)" | awk -F. '{print $$1}'; \
		fi \
	))
	$(eval manager_role := $(shell \
		if [ "$(version_major)" = "1" ]; then \
			echo "master"; \
		else \
			echo "cluster_manager"; \
		fi \
	))
	@# Apply cgroup workaround for OpenSearch 2.0.1-2.3.0
	$(eval java_opts_extra := $(shell \
		if [ "$(OPENSEARCH_VERSION)" != "latest" ]; then \
			version() { echo "$$@" | awk -F. '{ printf("%d%03d%03d%03d\n", $$1,$$2,$$3,$$4); }'; }; \
			v=$$(version $(OPENSEARCH_VERSION)); \
			v_min=$$(version 2.0.1); \
			v_max=$$(version 2.3.0); \
			if [ $$v -ge $$v_min ] && [ $$v -le $$v_max ]; then \
				echo " -XX:-UseContainerSupport"; \
			fi; \
		fi \
	))
	@echo "Starting OpenSearch $(OPENSEARCH_VERSION) with role: $(manager_role), secure: $(SECURE_INTEGRATION)"
	export SECURE_INTEGRATION=$(SECURE_INTEGRATION); \
	export OPENSEARCH_VERSION=$(OPENSEARCH_VERSION); \
	export OPENSEARCH_MANAGER_ROLE=$(manager_role); \
	export OPENSEARCH_MANAGER_SETTING=$(manager_role); \
	export OPENSEARCH_JAVA_OPTS_EXTRA="$(java_opts_extra)"; \
	docker compose --project-directory .ci/opensearch up -d

cluster.scale.1: ## Start single-node cluster
	docker compose --project-directory .ci/opensearch up -d --scale opensearch-node2=0 --scale opensearch-node3=0;

cluster.scale.2: ## Start 2-node cluster
	docker compose --project-directory .ci/opensearch up -d --scale opensearch-node1=1 --scale opensearch-node2=1 --scale opensearch-node3=0;

cluster.scale.3: ## Start full 3-node cluster
	docker compose --project-directory .ci/opensearch up -d --scale opensearch-node1=1 --scale opensearch-node2=1 --scale opensearch-node3=1;

cluster.get-cert:
	@if [ -n "$${SECURE_INTEGRATION}" ] && [ "$${SECURE_INTEGRATION}" = "true" ]; then \
		CONTAINER=$$(docker compose --project-directory .ci/opensearch ps --format '{{.Name}}' | head -1); \
		if [ -z "$$CONTAINER" ]; then \
			echo "Error: No OpenSearch containers running. Start cluster first with 'make cluster.start'"; \
			exit 1; \
		fi; \
		docker cp $$CONTAINER:/usr/share/opensearch/config/kirk.pem admin.pem && \
		docker cp $$CONTAINER:/usr/share/opensearch/config/kirk-key.pem admin.key; \
	fi

cluster.wait-ready: ## Poll cluster until health status is green or yellow
	@printf "\033[2m-> Waiting for cluster to be ready...\033[0m\n"
	@{ \
		set -e; \
		MAX_ATTEMPTS=60; \
		ATTEMPT=1; \
		HTTP_URL="http://localhost:9200/_cluster/health"; \
		HTTPS_URL="https://localhost:9200/_cluster/health"; \
		HEALTH_URL=""; \
		CURL_OPTS=""; \
		VERSION="$${OPENSEARCH_VERSION:-latest}"; \
		if [ "$$VERSION" = "latest" ]; then \
			PASSWORD="myStrongPassword123!"; \
		else \
			MAJOR=$$(echo "$$VERSION" | cut -d. -f1); \
			MINOR=$$(echo "$$VERSION" | cut -d. -f2); \
			if [ $$MAJOR -gt 2 ] || ([ $$MAJOR -eq 2 ] && [ $$MINOR -ge 12 ]); then \
				PASSWORD="myStrongPassword123!"; \
			else \
				PASSWORD="admin"; \
			fi; \
		fi; \
		while [ $$ATTEMPT -le $$MAX_ATTEMPTS ]; do \
			if [ -z "$$HEALTH_URL" ]; then \
				if curl -sf "$$HTTP_URL" > /dev/null 2>&1; then \
					printf "\033[36m→ Detected insecure cluster (HTTP)\033[0m\n"; \
					HEALTH_URL="$$HTTP_URL"; \
					CURL_OPTS=""; \
				elif curl -sf -k -u "admin:$$PASSWORD" "$$HTTPS_URL" > /dev/null 2>&1; then \
					printf "\033[36m→ Detected secure cluster (HTTPS)\033[0m\n"; \
					HEALTH_URL="$$HTTPS_URL"; \
					CURL_OPTS="-k -u admin:$$PASSWORD"; \
				else \
					printf "\033[33m⋯ Waiting for cluster to respond (attempt $$ATTEMPT/$$MAX_ATTEMPTS)\033[0m\n"; \
					ATTEMPT=$$((ATTEMPT + 1)); \
					sleep 2; \
					continue; \
				fi; \
			fi; \
			if curl -sf $$CURL_OPTS "$$HEALTH_URL" > /dev/null 2>&1; then \
				STATUS=$$(curl -sf $$CURL_OPTS "$$HEALTH_URL" | grep -o '"status":"[^"]*"' | cut -d'"' -f4); \
				if [ "$$STATUS" = "green" ] || [ "$$STATUS" = "yellow" ]; then \
					printf "\033[32m✓ Cluster is ready (status: $$STATUS) after $$ATTEMPT attempts\033[0m\n"; \
					INFO_URL="$${HEALTH_URL%%/_cluster/health}"; \
					CLUSTER_INFO=$$(curl -sf $$CURL_OPTS "$$INFO_URL" 2>/dev/null); \
					if [ -n "$$CLUSTER_INFO" ]; then \
						CLUSTER_NAME=$$(echo "$$CLUSTER_INFO" | grep -o '"cluster_name":"[^"]*"' | cut -d'"' -f4); \
						CLUSTER_VERSION=$$(echo "$$CLUSTER_INFO" | grep -o '"number":"[^"]*"' | head -1 | cut -d'"' -f4); \
						printf "\033[2m  Cluster: $$CLUSTER_NAME\033[0m\n"; \
						printf "\033[2m  Version: $$CLUSTER_VERSION\033[0m\n"; \
						printf "\033[2m  URL:     $$INFO_URL\033[0m\n"; \
						if [ -n "$$CURL_OPTS" ]; then \
							printf "\033[2m  Auth:    admin:****\033[0m\n"; \
						fi; \
					fi; \
					exit 0; \
				fi; \
				printf "\033[33m⋯ Cluster status: $$STATUS (attempt $$ATTEMPT/$$MAX_ATTEMPTS)\033[0m\n"; \
			else \
				printf "\033[33m⋯ Waiting for cluster to respond (attempt $$ATTEMPT/$$MAX_ATTEMPTS)\033[0m\n"; \
			fi; \
			ATTEMPT=$$((ATTEMPT + 1)); \
			sleep 2; \
		done; \
		printf "\033[31m✗ Cluster failed to become ready after $$MAX_ATTEMPTS attempts\033[0m\n"; \
		printf "\033[2m\n--- Diagnostic Information ---\033[0m\n"; \
		printf "\033[2mDocker containers:\033[0m\n"; \
		docker compose --project-directory .ci/opensearch ps || true; \
		printf "\033[2m\nFull logs from all containers:\033[0m\n"; \
		docker compose --project-directory .ci/opensearch logs || true; \
		printf "\033[2m\nAttempted URLs:\033[0m\n"; \
		printf "  HTTP:  $$HTTP_URL\n"; \
		printf "  HTTPS: $$HTTPS_URL\n"; \
		printf "\033[2m\nCurl test results:\033[0m\n"; \
		printf "  HTTP: "; curl -sf "$$HTTP_URL" && echo "✓ OK" || echo "✗ Failed"; \
		printf "  HTTPS: "; curl -sf -k -u "admin:$$PASSWORD" "$$HTTPS_URL" && echo "✓ OK" || echo "✗ Failed"; \
		exit 1; \
	}

cluster.clean: ## Remove unused Docker volumes and networks
	@printf "\033[2m-> Cleaning up Docker assets...\033[0m\n"
	@# Stop and remove containers first to release volumes
	@docker compose --project-directory .ci/opensearch down --volumes 2>/dev/null || true
	@# Remove OpenSearch built images to ensure clean rebuilds when switching versions
	@docker images -q opensearch-opensearch-node* | xargs -r docker rmi -f || true
	@# Remove OpenSearch volumes to clear stale data
	@docker volume ls -q --filter "name=opensearch" | xargs -r docker volume rm || true
	@# Clean up unused Docker resources
	docker volume prune --force
	docker network prune --force
	docker system prune --volumes --force

linters:
	docker run -t --rm -v $$(pwd):/app -v ~/.cache/golangci-lint/$(GOLANGCI_LINT_VERSION):/root/.cache -w /app golangci/golangci-lint:$(GOLANGCI_LINT_VERSION) golangci-lint run --fix --build-tags $(GOLANGCI_LINT_BUILD_TAGS) --timeout=5m -v ./...

workflow: ## Run all github workflow commands here sequentially

# Lint
	$(MAKE) lint
# License Checker
	.github/check-license-headers.sh
# Unit Test
	$(MAKE) test-unit race=true
# Benchmarks Test
	$(MAKE) test-bench
# Integration Test
### OpenSearch
	$(MAKE) cluster.clean cluster.build cluster.start
	$(MAKE) test-integ race=true
	$(MAKE) cluster.stop

##@ Other
#------------------------------------------------------------------------------
help:  ## Display help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
#------------- <https://suva.sh/posts/well-documented-makefiles> --------------

.DEFAULT_GOAL := help
.PHONY: help backport cluster.build cluster.start cluster.stop cluster.docker-build cluster.docker-up cluster.clean coverage godoc lint lint.local release test test-all test-race test-bench test-integ test-unit linters linters.install
.SILENT: lint.markdown
