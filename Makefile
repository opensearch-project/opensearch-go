SHELL := /bin/bash

##@ Test
test-unit:  ## Run unit tests
	@printf "\033[2m→ Running unit tests...\033[0m\n"
ifdef race
	$(eval testunitargs += "-race")
endif
	$(eval testunitargs += "-cover" "-coverprofile=tmp/unit.cov" "./...")
	@mkdir -p tmp
	@if which gotestsum > /dev/null 2>&1 ; then \
		echo "gotestsum --format=short-verbose --junitfile=tmp/unit-report.xml --" $(testunitargs); \
		gotestsum --format=short-verbose --junitfile=tmp/unit-report.xml -- $(testunitargs); \
	else \
		echo "go test -v" $(testunitargs); \
		go test -v $(testunitargs); \
	fi;
test: test-unit

test-integ:  ## Run integration tests
	@printf "\033[2m→ Running integration tests...\033[0m\n"
	$(eval testintegtags += "integration")
ifdef multinode
	$(eval testintegtags += "multinode")
endif
ifdef race
	$(eval testintegargs += "-race")
endif
	$(eval testintegargs += "-cover" "-coverprofile=tmp/integration-client.cov" "-tags='$(testintegtags)'" "-timeout=1h")
	@mkdir -p tmp
	@if which gotestsum > /dev/null 2>&1 ; then \
		echo "gotestsum --format=short-verbose --junitfile=tmp/integration-report.xml --" $(testintegargs); \
		gotestsum --format=short-verbose --junitfile=tmp/integration-report.xml -- $(testintegargs) "."; \
		gotestsum --format=short-verbose --junitfile=tmp/integration-report.xml -- $(testintegargs) "./estransport" "./opensearchapi" "./esutil"; \
	else \
		echo "go test -v" $(testintegargs) "."; \
		go test -v $(testintegargs) "./estransport" "./opensearchapi" "./esutil"; \
	fi;

test-api:  ## Run generated API integration tests
	@mkdir -p tmp
ifdef race
	$(eval testapiargs += "-race")
endif
	$(eval testapiargs += "-cover" "-coverpkg=github.com/opensearch-project/opensearch-go/opensearchapi" "-coverprofile=$(PWD)/tmp/integration-api.cov" "-tags='integration'" "-timeout=1h")
ifdef flavor
else
	$(eval flavor='free')
endif
	@printf "\033[2m→ Running API integration tests for [$(flavor)]...\033[0m\n"
ifeq ($(flavor), platinum)
	@{ \
		set -e ; \
		trap "test -d .git && git checkout --quiet $(PWD)/opensearchapi/test/go.mod" INT TERM EXIT; \
		export ELASTICSEARCH_URL='https://elastic:elastic@localhost:9200' && \
		if which gotestsum > /dev/null 2>&1 ; then \
			cd opensearchapi/test && \
			go mod download && \
				gotestsum --format=short-verbose --junitfile=$(PWD)/tmp/integration-api-report.xml -- $(testapiargs) $(PWD)/opensearchapi/test/xpack/*_test.go && \
				gotestsum --format=short-verbose --junitfile=$(PWD)/tmp/integration-api-report.xml -- $(testapiargs) $(PWD)/opensearchapi/test/xpack/ml/*_test.go && \
				gotestsum --format=short-verbose --junitfile=$(PWD)/tmp/integration-api-report.xml -- $(testapiargs) $(PWD)/opensearchapi/test/xpack/ml-crud/*_test.go; \
		else \
			echo "go test -v" $(testapiargs); \
			cd opensearchapi/test && \
			go mod download && \
				go test -v $(testapiargs) $(PWD)/opensearchapi/test/xpack/*_test.go && \
				go test -v $(testapiargs) $(PWD)/opensearchapi/test/xpack/ml/*_test.go && \
				go test -v $(testapiargs) $(PWD)/opensearchapi/test/xpack/ml-crud/*_test.go;  \
		fi; \
	}
else
	$(eval testapiargs += $(PWD)/opensearchapi/test/*_test.go)
	@{ \
		set -e ; \
		trap "test -d .git && git checkout --quiet $(PWD)/opensearchapi/test/go.mod" INT TERM EXIT; \
		if which gotestsum > /dev/null 2>&1 ; then \
			cd opensearchapi/test && \
			go mod download && \
			gotestsum --format=short-verbose --junitfile=$(PWD)/tmp/integration-api-report.xml -- $(testapiargs); \
		else \
			echo "go test -v" $(testapiargs); \
			cd opensearchapi/test && \
			go mod download && \
			go test -v $(testapiargs); \
		fi; \
	}
endif

test-bench:  ## Run benchmarks
	@printf "\033[2m→ Running benchmarks...\033[0m\n"
	go test -run=none -bench=. -benchmem ./...

test-examples: ## Execute the _examples
	@printf "\033[2m→ Testing the examples...\033[0m\n"
	@{ \
		set -e ; \
		trap "test -d .git && git checkout --quiet _examples/**/go.mod" INT TERM EXIT; \
		for d in _examples/*/; do \
			printf "\033[2m────────────────────────────────────────────────────────────────────────────────\n"; \
			printf "\033[1mUpdating dependencies for $$d\033[0m\n"; \
			printf "\033[2m────────────────────────────────────────────────────────────────────────────────\033[0m\n"; \
			(cd $$d && go mod download && make setup) || \
			( \
				printf "\033[31m────────────────────────────────────────────────────────────────────────────────\033[0m\n"; \
				printf "\033[31;1m⨯ ERROR\033[0m\n"; \
				false; \
			); \
	    done; \
	    \
		for f in _examples/*.go; do \
			printf "\033[2m────────────────────────────────────────────────────────────────────────────────\n"; \
			printf "\033[1m$$f\033[0m\n"; \
			printf "\033[2m────────────────────────────────────────────────────────────────────────────────\033[0m\n"; \
			(go run $$f && true) || \
			( \
				printf "\033[31m────────────────────────────────────────────────────────────────────────────────\033[0m\n"; \
				printf "\033[31;1m⨯ ERROR\033[0m\n"; \
				false; \
			); \
		done; \
		\
		for f in _examples/*/; do \
			printf "\033[2m────────────────────────────────────────────────────────────────────────────────\033[0m\n"; \
			printf "\033[1m$$f\033[0m\n"; \
			printf "\033[2m────────────────────────────────────────────────────────────────────────────────\033[0m\n"; \
			(cd $$f && make test && true) || \
			( \
				printf "\033[31m────────────────────────────────────────────────────────────────────────────────\033[0m\n"; \
				printf "\033[31;1m⨯ ERROR\033[0m\n"; \
				false; \
			); \
		done; \
		printf "\033[32m────────────────────────────────────────────────────────────────────────────────\033[0m\n"; \
		\
		printf "\033[32;1mSUCCESS\033[0m\n"; \
	}

test-coverage:  ## Generate test coverage report
	@printf "\033[2m→ Generating test coverage report...\033[0m\n"
	@go tool cover -html=tmp/unit.cov -o tmp/coverage.html
	@go tool cover -func=tmp/unit.cov | 'grep' -v 'opensearchapi/api\.' | sed 's/github.com\/elastic\/go-elasticsearch\///g'
	@printf "\033[0m--------------------------------------------------------------------------------\nopen tmp/coverage.html\n\n\033[0m"

##@ Development
lint:  ## Run lint on the package
	@printf "\033[2m→ Running lint...\033[0m\n"
	go vet github.com/opensearch-project/opensearch-go/...
	go list github.com/opensearch-project/opensearch-go/... | 'grep' -v internal | xargs golint -set_exit_status
	@{ \
		set -e ; \
		trap "test -d ../../../.git && git checkout --quiet go.mod" INT TERM EXIT; \
		echo "cd internal/build/ && go vet ./..."; \
		cd "internal/build/" && go mod tidy && go mod download && go vet ./...; \
	}


apidiff: ## Display API incompabilities
	@if ! command -v apidiff > /dev/null; then \
		printf "\033[31;1mERROR: apidiff not installed\033[0m\n"; \
		printf "go get -u github.com/go-modules-by-example/apidiff\n"; \
		printf "\033[2m→ https://github.com/go-modules-by-example/index/blob/master/019_apidiff/README.md\033[0m\n\n"; \
		false; \
	fi;
	@rm -rf tmp/apidiff-OLD tmp/apidiff-NEW
	@git clone --quiet --local .git/ tmp/apidiff-OLD
	@mkdir -p tmp/apidiff-NEW
	@tar -c --exclude .git --exclude tmp --exclude cmd . | tar -x -C tmp/apidiff-NEW
	@printf "\033[2m→ Running apidiff...\033[0m\n"
	@pritnf "tmp/apidiff-OLD/opensearchapi tmp/apidiff-NEW/opensearchapi\n"
	@{ \
		set -e ; \
		output=$$(apidiff tmp/apidiff-OLD/opensearchapi tmp/apidiff-NEW/opensearchapi); \
		printf "\n$$output\n\n"; \
		if echo $$output | grep -i -e 'incompatible' - > /dev/null 2>&1; then \
			printf "\n\033[31;1mFAILURE\033[0m\n\n"; \
			false; \
		else \
			printf "\033[32;1mSUCCESS\033[0m\n"; \
		fi; \
	}

backport: ## Backport one or more commits from master into version branches
ifeq ($(origin commits), undefined)
	@echo "Missing commit(s), exiting..."
	@exit 2
endif
ifndef branches
	$(eval branches_list = '7.x' '6.x' '5.x')
else
	$(eval branches_list = $(shell echo $(branches) | tr ',' ' ') )
endif
	$(eval commits_list = $(shell echo $(commits) | tr ',' ' '))
	@printf "\033[2m→ Backporting commits [$(commits)]\033[0m\n"
	@{ \
		set -e -o pipefail; \
		for commit in $(commits_list); do \
			git show --pretty='%h | %s' --no-patch $$commit; \
		done; \
		echo ""; \
		for branch in $(branches_list); do \
			printf "\033[2m→ $$branch\033[0m\n"; \
			git checkout $$branch; \
			for commit in $(commits_list); do \
				git cherry-pick -x $$commit; \
			done; \
			git status --short --branch; \
			echo ""; \
		done; \
		printf "\033[2m→ Push updates to Github:\033[0m\n"; \
		for branch in $(branches_list); do \
			echo "git push --verbose origin $$branch"; \
		done; \
	}

release: ## Release a new version to Github
	$(eval branch = $(shell git rev-parse --abbrev-ref HEAD))
	$(eval current_version = $(shell cat internal/version/version.go | sed -Ee 's/const Client = "(.*)"/\1/' | tail -1))
	@printf "\033[2m→ [$(branch)] Current version: $(current_version)...\033[0m\n"
ifndef version
	@printf "\033[31m[!] Missing version argument, exiting...\033[0m\n"
	@exit 2
endif
ifeq ($(version), "")
	@printf "\033[31m[!] Empty version argument, exiting...\033[0m\n"
	@exit 2
endif
	@printf "\033[2m→ [$(branch)] Creating version $(version)...\033[0m\n"
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
		printf "\033[2m→ Commit and create Git tag? (y/n): \033[0m\c"; \
		read continue; \
		if [[ $$continue == "y" ]]; then \
			git add internal/version/version.go && \
			git commit --no-status --quiet --message "Release $(version)" && \
			git tag --annotate v$(version) --message 'Release $(version)'; \
			printf "\033[2m→ Push `git show --pretty='%h (%s)' --no-patch HEAD` to Github:\033[0m\n\n"; \
			printf "\033[1m  git push origin HEAD && git push origin v$(version)\033[0m\n\n"; \
			mv internal/version/version.go.OLD internal/version/version.go && \
			git add internal/version/version.go && \
			original_version=`cat internal/version/version.go | sed -ne 's;^const Client = "\(.*\)"$$;\1;p'` && \
			git commit --no-status --quiet --message "Update version to $$original_version"; \
			printf "\033[2m→ Version updated to [$$original_version].\033[0m\n\n"; \
		else \
			echo "Aborting..."; \
			rm internal/version/version.go.OLD; \
			exit 1; \
		fi; \
	}

godoc: ## Display documentation for the package
	@printf "\033[2m→ Generating documentation...\033[0m\n"
	@echo "* http://localhost:6060/pkg/github.com/opensearch-project/opensearch-go"
	@echo "* http://localhost:6060/pkg/github.com/opensearch-project/opensearch-go/opensearchapi"
	@echo "* http://localhost:6060/pkg/github.com/opensearch-project/opensearch-go/estransport"
	@echo "* http://localhost:6060/pkg/github.com/opensearch-project/opensearch-go/esutil"
	@printf "\n"
	godoc --http=localhost:6060 --play

cluster.opendistro.build:
	docker-compose --project-directory .ci/opendistro build;

cluster.opendistro.start:
	docker-compose --project-directory .ci/opendistro up -d ;
	sleep 20;

cluster.opendistro.stop:
	docker-compose --project-directory .ci/opendistro down ;

cluster.clean: ## Remove unused Docker volumes and networks
	@printf "\033[2m→ Cleaning up Docker assets...\033[0m\n"
	docker volume prune --force
	docker network prune --force
	docker system prune --volumes --force

docker: ## Build the Docker image and run it
	docker build --file .ci/Dockerfile --tag elastic/go-elasticsearch .
	docker run -it --network elasticsearch --volume $(PWD)/tmp:/tmp:rw,delegated --rm elastic/go-elasticsearch

##@ Generator
gen-api:  ## Generate the API package from the JSON specification
	$(eval input  ?= tmp/rest-api-spec)
	$(eval output ?= opensearchapi)
ifdef debug
	$(eval args += --debug)
endif
ifdef ELASTICSEARCH_BUILD_VERSION
	$(eval version = $(ELASTICSEARCH_BUILD_VERSION))
else
	$(eval version = $(ELASTICSEARCH_DEFAULT_BUILD_VERSION))
endif
ifdef ELASTICSEARCH_BUILD_HASH
	$(eval build_hash = $(ELASTICSEARCH_BUILD_HASH))
else
	$(eval build_hash = $(shell cat tmp/elasticsearch.json | jq ".projects.elasticsearch.commit_hash"))
endif
	@printf "\033[2m→ Generating API package from specification ($(version):$(build_hash))...\033[0m\n"
	@{ \
		set -e; \
		trap "test -d .git && git checkout --quiet $(PWD)/internal/build/go.mod" INT TERM EXIT; \
		export ELASTICSEARCH_BUILD_VERSION=$(version) && \
		export ELASTICSEARCH_BUILD_HASH=$(build_hash) && \
		cd internal/build && \
		go run main.go apisource --input '$(PWD)/$(input)/api/*.json' --output '$(PWD)/$(output)' $(args) && \
		go run main.go apistruct --output '$(PWD)/$(output)'; \
	}

gen-tests:  ## Generate the API tests from the YAML specification
	$(eval input  ?= tmp/rest-api-spec)
	$(eval output ?= opensearchapi/test)
ifdef debug
	$(eval args += --debug)
endif
ifdef ELASTICSEARCH_BUILD_VERSION
	$(eval version = $(ELASTICSEARCH_BUILD_VERSION))
else
	$(eval version = $(ELASTICSEARCH_DEFAULT_BUILD_VERSION))
endif
ifdef ELASTICSEARCH_BUILD_HASH
	$(eval build_hash = $(ELASTICSEARCH_BUILD_HASH))
else
	$(eval build_hash = $(shell cat tmp/elasticsearch.json | jq ".projects.elasticsearch.commit_hash"))
endif
	@printf "\033[2m→ Generating API tests from specification ($(version):$(build_hash))...\033[0m\n"
	@{ \
		set -e; \
		trap "test -d .git && git checkout --quiet $(PWD)/internal/cmd/generate/go.mod" INT TERM EXIT; \
		export ELASTICSEARCH_BUILD_VERSION=$(version) && \
		export ELASTICSEARCH_BUILD_HASH=$(build_hash) && \
		rm -rf $(output)/*_test.go && \
		rm -rf $(output)/xpack && \
		cd internal/build && \
		go get golang.org/x/tools/cmd/goimports && \
		go generate ./... && \
		go run main.go apitests --input '$(PWD)/$(input)/test/free/**/*.y*ml' --output '$(PWD)/$(output)' $(args) && \
		go run main.go apitests --input '$(PWD)/$(input)/test/platinum/**/*.yml' --output '$(PWD)/$(output)/xpack' $(args) && \
		mkdir -p '$(PWD)/opensearchapi/test/xpack/ml' && \
		mkdir -p '$(PWD)/opensearchapi/test/xpack/ml-crud' && \
		mv $(PWD)/opensearchapi/test/xpack/xpack_ml* $(PWD)/opensearchapi/test/xpack/ml/ && \
		mv $(PWD)/opensearchapi/test/xpack/ml/xpack_ml__jobs_crud_test.go $(PWD)/opensearchapi/test/xpack/ml-crud/; \
	}

gen-docs:  ## Generate the skeleton of documentation examples
	$(eval input  ?= tmp/alternatives_report.json)
	$(eval update ?= no)
	@{ \
		set -e; \
		trap "test -d .git && git checkout --quiet $(PWD)/internal/cmd/generate/go.mod" INT TERM EXIT; \
		if [[ $(update) == 'yes' ]]; then \
			printf "\033[2m→ Updating the alternatives_report.json file\033[0m\n" && \
			curl -s https://raw.githubusercontent.com/elastic/built-docs/master/raw/en/elasticsearch/reference/master/alternatives_report.json > tmp/alternatives_report.json; \
		fi; \
		printf "\033[2m→ Generating Go source files from Console input in [$(input)]\033[0m\n" && \
		( cd '$(PWD)/internal/cmd/generate' && \
			go run main.go examples src --debug --input='$(PWD)/$(input)' --output='$(PWD)/.doc/examples/' \
		) && \
		( cd '$(PWD)/.doc/examples/src' && \
			if which gotestsum > /dev/null 2>&1 ; then \
				gotestsum --format=short-verbose; \
			else \
				go test -v $(testunitargs); \
			fi; \
		) && \
		printf "\n\033[2m→ Generating ASCIIDoc files from Go source\033[0m\n" && \
		( cd '$(PWD)/internal/build' && \
			go run main.go examples doc --debug --input='$(PWD)/.doc/examples/src/' --output='$(PWD)/.doc/examples/' \
		) \
	}

download-specs: ## Download the latest specs for the specified Elasticsearch version
	$(eval output ?= tmp)
	@mkdir -p tmp
	@{ \
		set -e; \
		printf "\n\033[2m→ Downloading latest Elasticsearch specs for version [$(ELASTICSEARCH_DEFAULT_BUILD_VERSION)]\033[0m\n" && \
		rm -rf $(output)/rest-api-spec && \
		rm -rf $(output)/elasticsearch.json && \
		cd internal/build && \
		go run main.go download-spec --output '$(PWD)/$(output)'; \
	}

##@ Other
#------------------------------------------------------------------------------
help:  ## Display help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
#------------- <https://suva.sh/posts/well-documented-makefiles> --------------

.DEFAULT_GOAL := help
.PHONY: help apidiff backport cluster cluster.opendistro.build cluster.opendistro.start cluster.opendistro.stop cluster.clean coverage docker examples gen-api gen-tests godoc lint release test test-api test-bench test-integ test-unit
