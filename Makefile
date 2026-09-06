COMPOSE_FILE ?= compose.yaml
# Lets test_integration_buildkit_explicit_* clean up the explicit-engine overlay.
TEST_COMPOSE_FILE ?= compose.test-universal.yaml

# Fixed Compose project name, trusted by report/src/main.ts and
# src/post.ts via their own BUILDCAGE_BUILD_TEST_HOOKS-gated overrides
# instead of deriveProjectName("buildcage") (src/core/lib/docker/container.ts).
# Scoped to the targets that touch this Compose project; test_unit_* is
# excluded on purpose (see its own section below).
setup_buildkit_% test_integration_buildkit_% example_% clean_buildkit report_buildkit: export COMPOSE_PROJECT_NAME := buildcage-project
setup_buildkit_% test_integration_buildkit_% example_% clean_buildkit report_buildkit: export BUILDCAGE_BUILD_TEST_HOOKS := 1

.PHONY: help
help:
	@grep -E '^[a-zA-Z_0-9-]+(-%)?:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

# ===========================================================================
# Unit tests
# ===========================================================================

.PHONY: test_unit
test_unit: test_unit_core test_unit_setup test_unit_report test_unit_qjs ## Run unit tests

# vitest matches these by path substring, not glob, so keep them package-specific.
.PHONY: test_unit_core
test_unit_core: ## Run core unit tests
	@vp test run src/core

.PHONY: test_unit_setup
test_unit_setup: ## Run setup action unit tests
	@vp test run src/lib src/main

.PHONY: test_unit_report
# report/src has no package-local pure functions to unit-test.
test_unit_report: ## Run report unit tests
	@vp test run report/src --passWithNoTests

# qjs can't execute .ts directly, so compile fresh (vp run build:qjs-test)
# and bind-mount the output in. qjs itself is identical across images, so one
# representative build is enough.
QJS_MOUNTS := \
	-v "$(CURDIR)/dist/qjs-test/src/core:/opt/buildcage/core:ro"
QJS_TEST_DIRS := \
	/opt/buildcage/core/lib/acl

.PHONY: test_unit_qjs
test_unit_qjs: ## Run unit tests in Docker
	@vp run build:qjs-test
	@docker build -f docker/universal/Dockerfile -t buildcage-qjs-test .
	@docker run --rm --entrypoint qjs $(QJS_MOUNTS) buildcage-qjs-test \
		--std -m /opt/buildcage/core/scripts/test/run-tests.qjs.js $(QJS_TEST_DIRS)

# ===========================================================================
# Integration tests
# ===========================================================================

# ---------------------------------------------------------------------------
# setup_buildkit_{engine}_{mode} — start the builder only
# ---------------------------------------------------------------------------

.PHONY: setup_buildkit_universal_audit
setup_buildkit_universal_audit: ## Start universal engine in audit mode
	@echo "Starting buildcage (universal engine) in AUDIT mode..."
	@COMPOSE_FILE=$(COMPOSE_FILE) \
	  PROXY_MODE=audit \
	  docker compose -p $(COMPOSE_PROJECT_NAME) up -d --wait --build
	@docker buildx rm buildcage 2>/dev/null || true
	@echo "Creating buildx builder..."
	@docker buildx create --bootstrap \
		--name buildcage \
		--driver remote docker-container://buildcage

.PHONY: setup_buildkit_universal_restrict
setup_buildkit_universal_restrict: ## Start universal engine in restrict mode
	@echo "Starting buildcage (universal engine) in RESTRICT mode..."
	@COMPOSE_FILE=$(COMPOSE_FILE) \
	  PROXY_MODE=restrict \
	  ALLOWED_HTTP_RULES="$${ALLOWED_HTTP_RULES:-}" \
	  ALLOWED_HTTPS_RULES="$${ALLOWED_HTTPS_RULES:-github.com:443 registry.npmjs.org:443 api.github.com:443 objects.githubusercontent.com:443 httpbin.org:443 deb.debian.org:80 *.githubusercontent.com:443}" \
	  docker compose -p $(COMPOSE_PROJECT_NAME) up -d --wait --build
	@docker buildx rm buildcage 2>/dev/null || true
	@echo "Creating buildx builder..."
	@docker buildx create --bootstrap \
		--name buildcage \
		--driver remote docker-container://buildcage

.PHONY: setup_buildkit_explicit_audit
setup_buildkit_explicit_audit: ## Start explicit proxy engine in audit mode
	@echo "Starting buildcage (explicit proxy engine) in AUDIT mode..."
	@COMPOSE_FILE=$(COMPOSE_FILE) \
	  PROXY_ENGINE=explicit \
	  PROXY_MODE=audit \
	  docker compose -p $(COMPOSE_PROJECT_NAME) up -d --wait --build
	@docker buildx rm buildcage 2>/dev/null || true
	@echo "Creating buildx builder..."
	@docker buildx create --bootstrap \
		--name buildcage \
		--driver remote docker-container://buildcage

.PHONY: setup_buildkit_explicit_restrict
setup_buildkit_explicit_restrict: ## Start explicit proxy engine in restrict mode
	@echo "Starting buildcage (explicit proxy engine) in RESTRICT mode..."
	@COMPOSE_FILE=$(COMPOSE_FILE) \
	  PROXY_ENGINE=explicit \
	  PROXY_MODE=restrict \
	  ALLOWED_HTTP_RULES="$${ALLOWED_HTTP_RULES:-}" \
	  ALLOWED_HTTPS_RULES="$${ALLOWED_HTTPS_RULES:-github.com:443 registry.npmjs.org:443 api.github.com:443 objects.githubusercontent.com:443 httpbin.org:443 deb.debian.org:80 *.githubusercontent.com:443}" \
	  docker compose -p $(COMPOSE_PROJECT_NAME) up -d --wait --build
	@docker buildx rm buildcage 2>/dev/null || true
	@echo "Creating buildx builder..."
	@docker buildx create --bootstrap \
		--name buildcage \
		--driver remote docker-container://buildcage

.PHONY: setup_buildkit_inspect_audit
setup_buildkit_inspect_audit: ## Start inspect proxy engine in audit mode
	@echo "Starting buildcage (inspect proxy engine) in AUDIT mode..."
	@COMPOSE_FILE=$(COMPOSE_FILE) \
	  PROXY_ENGINE=inspect \
	  PROXY_MODE=audit \
	  docker compose -p $(COMPOSE_PROJECT_NAME) up -d --wait --build
	@docker buildx rm buildcage 2>/dev/null || true
	@echo "Creating buildx builder..."
	@docker buildx create --bootstrap \
		--name buildcage \
		--driver remote docker-container://buildcage

.PHONY: setup_buildkit_inspect_restrict
setup_buildkit_inspect_restrict: ## Start inspect proxy engine in restrict mode
	@echo "Starting buildcage (inspect proxy engine) in RESTRICT mode..."
	@COMPOSE_FILE=$(COMPOSE_FILE) \
	  PROXY_ENGINE=inspect \
	  PROXY_MODE=restrict \
	  docker compose -p $(COMPOSE_PROJECT_NAME) up -d --wait --build
	@docker buildx rm buildcage 2>/dev/null || true
	@echo "Creating buildx builder..."
	@docker buildx create --bootstrap \
		--name buildcage \
		--driver remote docker-container://buildcage

.PHONY: clean_buildkit
clean_buildkit: ## Stop and remove the buildkit builder's containers/images and buildx builder
	@echo "Stopping and removing all containers..."
	@docker buildx rm buildcage 2>/dev/null || true
	@docker compose -p $(COMPOSE_PROJECT_NAME) -f compose.yaml -f $(TEST_COMPOSE_FILE) down -v --rmi all
	@docker rmi buildcage-test 2>/dev/null || true

.PHONY: report_buildkit
report_buildkit: ## Show the buildcage report for the currently running builder
	@node report/src/main.ts

# ---------------------------------------------------------------------------
# test_integration_buildkit_{engine}_{mode} — setup + build + verify + clean
# ---------------------------------------------------------------------------

.PHONY: test_integration_buildkit
test_integration_buildkit: test_integration_buildkit_universal_audit test_integration_buildkit_universal_restrict test_integration_buildkit_universal_restrict_no_traffic test_integration_buildkit_explicit_audit test_integration_buildkit_explicit_restrict test_integration_buildkit_inspect_audit test_integration_buildkit_inspect_restrict test_integration_buildkit_inspect_debian_audit test_integration_buildkit_inspect_debian_restrict test_integration_buildkit_inspect_byte_exact test_integration_buildkit_inspect_roundtrip ## Run all buildkit integration tests

.PHONY: test_integration_buildkit_universal_audit
test_integration_buildkit_universal_audit: ## Run universal-engine audit mode tests
	@echo "Running universal-engine audit mode tests..."
	@COMPOSE_FILE=compose.yaml:compose.test-universal.yaml \
	  $(MAKE) setup_buildkit_universal_audit
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.universal-audit test/ \
	  --load -t buildcage-test
	@node report/src/main.ts
	@./test/assert-universal-audit.sh
	@node src/post.ts
	@./test/assert-post.sh
	@$(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_universal_restrict
test_integration_buildkit_universal_restrict: ## Run universal-engine restrict mode tests
	@echo "Running universal-engine restrict mode tests..."
	@COMPOSE_FILE=compose.yaml:compose.test-universal.yaml \
	  $(MAKE) setup_buildkit_universal_restrict
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.universal-restrict test/ \
	  --load -t buildcage-test
	@node report/src/main.ts || true
	@./test/assert-universal-restrict.sh
	@node src/post.ts
	@./test/assert-post.sh
	@$(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_universal_restrict_no_traffic
test_integration_buildkit_universal_restrict_no_traffic: ## Run universal-engine restrict mode tests with zero outbound traffic
	@echo "Running universal-engine restrict mode tests with zero outbound traffic..."
	@COMPOSE_FILE=compose.yaml:compose.test-universal.yaml \
	  $(MAKE) setup_buildkit_universal_restrict
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.universal-restrict-no-traffic test/ \
	  --load -t buildcage-test
	@INPUT_FAIL_ON_BLOCKED=true node report/src/main.ts
	@./test/assert-universal-restrict-no-traffic.sh
	@node src/post.ts
	@./test/assert-post.sh
	@$(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_explicit_audit
test_integration_buildkit_explicit_audit: ## Run explicit-engine audit mode tests
	@echo "Running explicit-engine audit mode tests..."
	@COMPOSE_FILE=compose.yaml:compose.test-explicit.yaml \
	  $(MAKE) setup_buildkit_explicit_audit
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.explicit-audit test/ \
	  --load -t buildcage-test
	@node report/src/main.ts || true
	@./test/assert-explicit-audit.sh
	@node src/post.ts
	@./test/assert-post.sh
	@TEST_COMPOSE_FILE=compose.test-explicit.yaml $(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_explicit_restrict
test_integration_buildkit_explicit_restrict: ## Run explicit-engine restrict mode tests
	@echo "Running explicit-engine restrict mode tests..."
	@COMPOSE_FILE=compose.yaml:compose.test-explicit.yaml \
	  $(MAKE) setup_buildkit_explicit_restrict
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.explicit-restrict test/ \
	  --load -t buildcage-test
	@node report/src/main.ts || true
	@./test/assert-explicit-restrict.sh
	@node src/post.ts
	@./test/assert-post.sh
	@TEST_COMPOSE_FILE=compose.test-explicit.yaml $(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_inspect_audit
test_integration_buildkit_inspect_audit: ## Run inspect-engine audit mode tests
	@echo "Running inspect-engine audit mode tests..."
	@COMPOSE_FILE=compose.yaml:compose.test-inspect.yaml \
	  $(MAKE) setup_buildkit_inspect_audit
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.inspect-audit test/ \
	  --load -t buildcage-test
	@./test/assert-inspect-no-ca-residue.sh buildcage-test
	@./test/assert-inspect-no-layer-bloat.sh buildcage-test
	@node report/src/main.ts || true
	@./test/assert-inspect-audit.sh
	@node src/post.ts
	@./test/assert-post.sh
	@TEST_COMPOSE_FILE=compose.test-inspect.yaml $(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_inspect_restrict
test_integration_buildkit_inspect_restrict: ## Run inspect-engine restrict mode tests
	@echo "Running inspect-engine restrict mode tests..."
	@COMPOSE_FILE=compose.yaml:compose.test-inspect.yaml \
	  $(MAKE) setup_buildkit_inspect_restrict
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.inspect-restrict test/ \
	  --load -t buildcage-test
	@./test/assert-inspect-no-ca-residue.sh buildcage-test
	@./test/assert-inspect-no-layer-bloat.sh buildcage-test
	@node report/src/main.ts || true
	@./test/assert-inspect-restrict.sh
	@node src/post.ts
	@./test/assert-post.sh
	@TEST_COMPOSE_FILE=compose.test-inspect.yaml $(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_inspect_debian_audit
test_integration_buildkit_inspect_debian_audit: ## Run inspect-engine audit mode tests against a Debian (apt) build
	@echo "Running inspect-engine audit mode tests (Debian/apt)..."
	@COMPOSE_FILE=compose.yaml:compose.test-inspect.yaml \
	  $(MAKE) setup_buildkit_inspect_audit
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.inspect-debian test/ \
	  --load -t buildcage-test
	@./test/assert-inspect-no-ca-residue.sh buildcage-test
	@./test/assert-inspect-no-layer-bloat.sh buildcage-test
	@node report/src/main.ts || true
	@./test/assert-inspect-debian.sh
	@TEST_COMPOSE_FILE=compose.test-inspect.yaml $(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_inspect_debian_restrict
test_integration_buildkit_inspect_debian_restrict: ## Run inspect-engine restrict mode tests against a Debian (apt) build
	@echo "Running inspect-engine restrict mode tests (Debian/apt)..."
	@COMPOSE_FILE=compose.yaml:compose.test-inspect.yaml \
	  $(MAKE) setup_buildkit_inspect_restrict
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f test/Dockerfile.inspect-debian test/ \
	  --load -t buildcage-test
	@./test/assert-inspect-no-ca-residue.sh buildcage-test
	@./test/assert-inspect-no-layer-bloat.sh buildcage-test
	@node report/src/main.ts || true
	@./test/assert-inspect-debian.sh
	@TEST_COMPOSE_FILE=compose.test-inspect.yaml $(MAKE) clean_buildkit

.PHONY: test_integration_buildkit_inspect_byte_exact
test_integration_buildkit_inspect_byte_exact: ## Compare inspect vs universal layer-for-layer, byte for byte
	@echo "Running inspect-engine byte-exact layer comparison..."
	@rm -f /tmp/buildcage-byte-exact-inspect.tar /tmp/buildcage-byte-exact-universal.tar
	@COMPOSE_FILE=compose.yaml:compose.test-inspect.yaml \
	  $(MAKE) setup_buildkit_inspect_audit
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --build-arg SOURCE_DATE_EPOCH=1700000000 \
	  --output type=docker,name=buildcage-byte-exact-inspect,rewrite-timestamp=true,unpack=false,dest=/tmp/buildcage-byte-exact-inspect.tar \
	  --progress=plain -f test/Dockerfile.inspect-byte-exact test/
	@TEST_COMPOSE_FILE=compose.test-inspect.yaml $(MAKE) clean_buildkit
	@COMPOSE_FILE=compose.yaml:compose.test-universal.yaml \
	  $(MAKE) setup_buildkit_universal_audit
	@docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --build-arg SOURCE_DATE_EPOCH=1700000000 \
	  --output type=docker,name=buildcage-byte-exact-universal,rewrite-timestamp=true,unpack=false,dest=/tmp/buildcage-byte-exact-universal.tar \
	  --progress=plain -f test/Dockerfile.inspect-byte-exact test/
	@TEST_COMPOSE_FILE=compose.test-universal.yaml $(MAKE) clean_buildkit
	@docker load -i /tmp/buildcage-byte-exact-inspect.tar
	@docker load -i /tmp/buildcage-byte-exact-universal.tar
	@./test/assert-inspect-byte-exact.sh buildcage-byte-exact-universal buildcage-byte-exact-inspect
	@docker rmi buildcage-byte-exact-inspect buildcage-byte-exact-universal
	@rm -f /tmp/buildcage-byte-exact-inspect.tar /tmp/buildcage-byte-exact-universal.tar

.PHONY: test_integration_buildkit_inspect_roundtrip
test_integration_buildkit_inspect_roundtrip: ## Learn rules from an inspect audit run, then enforce them
	@echo "Running inspect-engine audit-to-restrict round trip..."
	@./test/run-inspect-roundtrip.sh
	@TEST_COMPOSE_FILE=compose.test-inspect.yaml $(MAKE) clean_buildkit

# ---------------------------------------------------------------------------
# example_{engine}_{mode} — smoke test against a plain Dockerfile
# ---------------------------------------------------------------------------

.PHONY: example_universal_audit
example_universal_audit: ## Run audit mode example tests
	@echo "Running audit mode example tests..."
	@$(MAKE) setup_buildkit_universal_audit
	@mkdir -p /tmp/build-context
	@printf '%s\n' \
	  "FROM node:24-alpine" \
	  "WORKDIR /app" \
	  "RUN npm init -y && npm install --ignore-scripts express" \
	  > /tmp/build-context/Dockerfile
	docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f /tmp/build-context/Dockerfile /tmp/build-context \
	  --load -t buildcage-test
	@node report/src/main.ts
	@$(MAKE) clean_buildkit
	rm -fr /tmp/build-context

.PHONY: example_universal_restrict
example_universal_restrict: ## Run restrict mode example tests
	@echo "Running restrict mode example tests..."
	@ALLOWED_HTTPS_RULES="registry.npmjs.org:443" \
	  $(MAKE) setup_buildkit_universal_restrict
	@mkdir -p /tmp/build-context
	@printf '%s\n' \
	  "FROM node:24-alpine" \
	  "WORKDIR /app" \
	  "RUN npm init -y && npm install --ignore-scripts express" \
	  "RUN wget -q -O /dev/null --timeout=5 https://example.com/ || true" \
	  > /tmp/build-context/Dockerfile
	docker buildx build --no-cache \
	  --builder buildcage \
	  --platform linux/arm64 \
	  --progress=plain -f /tmp/build-context/Dockerfile /tmp/build-context \
	  --load -t buildcage-test
	@node report/src/main.ts || true
	@$(MAKE) clean_buildkit
	rm -fr /tmp/build-context
