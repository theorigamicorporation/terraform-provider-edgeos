# Developer entry point. CI runs the same recipes.
# Toolchain versions are pinned in .tool-versions (run `asdf install`).

set shell := ["bash", "-euo", "pipefail", "-c"]

binary := "terraform-provider-edgeos"
namespace := "theorigamicorporation"
gobin := `go env GOBIN 2>/dev/null || true`
gopath := `go env GOPATH 2>/dev/null || true`
install_dir := if gobin != "" { gobin } else { gopath / "bin" }

# Show the available recipes
default:
    @just --list

# Build the provider binary into ./bin
build:
    go build -trimpath -o bin/{{binary}} .

# Install the provider into $GOBIN and print the dev_overrides snippets
install:
    go install -trimpath .
    @echo
    @echo "Installed {{install_dir}}/{{binary}}"
    @echo
    @echo "Add this to ~/.terraformrc (Terraform):"
    @echo
    @echo '  provider_installation {'
    @echo '    dev_overrides {'
    @echo '      "registry.terraform.io/{{namespace}}/edgeos" = "{{install_dir}}"'
    @echo '    }'
    @echo '    direct {}'
    @echo '  }'
    @echo
    @echo "Add this to ~/.tofurc (OpenTofu):"
    @echo
    @echo '  provider_installation {'
    @echo '    dev_overrides {'
    @echo '      "registry.opentofu.org/{{namespace}}/edgeos" = "{{install_dir}}"'
    @echo '    }'
    @echo '    direct {}'
    @echo '  }'
    @echo
    @echo "With dev_overrides set, skip init and run plan/apply directly."

# Format Go code and the Terraform examples
fmt:
    go fmt ./...
    terraform fmt -recursive examples/

# Run golangci-lint
lint:
    golangci-lint run ./...

# Run unit tests (acceptance tests are skipped without TF_ACC)
test:
    go test -race -count=1 ./...

# Run a short fuzzing session on the path and value encoders
fuzz seconds="20":
    for f in FuzzPathRoundTrip FuzzParsePath FuzzBatchJSON FuzzValidateValue; do \
      go test ./internal/configtree/ -run='^$' -fuzz="^$f\$" -fuzztime={{seconds}}s; \
    done

# Run acceptance tests against the fake router with Terraform and OpenTofu
testacc: testacc-terraform testacc-tofu

# Run acceptance tests against the fake router with Terraform
testacc-terraform *args:
    TF_ACC=1 \
    TF_ACC_TERRAFORM_PATH="$(command -v terraform)" \
    TF_ACC_PROVIDER_HOST=registry.terraform.io \
    TF_ACC_PROVIDER_NAMESPACE={{namespace}} \
    go test -race -count=1 -timeout 30m ./internal/provider/ {{args}}

# Run acceptance tests against the fake router with OpenTofu
testacc-tofu *args:
    TF_ACC=1 \
    TF_ACC_TERRAFORM_PATH="$(command -v tofu)" \
    TF_ACC_PROVIDER_HOST=registry.opentofu.org \
    TF_ACC_PROVIDER_NAMESPACE={{namespace}} \
    go test -race -count=1 -timeout 30m ./internal/provider/ {{args}}

# Run the acceptance tests against a REAL router. They change its config!
testacc-real *args:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ "${EDGEOS_ACC_REAL:-}" != "1" || -z "${EDGEOS_HOST:-}" ]]; then
      echo "Refusing to run: set EDGEOS_ACC_REAL=1 and EDGEOS_HOST (plus EDGEOS_USERNAME and EDGEOS_PASSWORD)." >&2
      echo "These tests commit and save changes on the router at EDGEOS_HOST." >&2
      exit 1
    fi
    echo "################################################################"
    echo "#  WARNING: these tests WRITE to the router at ${EDGEOS_HOST}"
    echo "#  They create, change and delete config under names starting"
    echo "#  with TFACC and commit and save every change to config.boot."
    echo "#  Use a lab router, never one carrying production traffic."
    echo "################################################################"
    sleep 5
    TF_ACC=1 \
    TF_ACC_TERRAFORM_PATH="$(command -v terraform)" \
    TF_ACC_PROVIDER_NAMESPACE={{namespace}} \
    go test -count=1 -p 1 -timeout 60m -run 'TestAccReal' ./internal/provider/ {{args}}

# Coverage of internal/... with unit and acceptance tests, failing below the threshold
cover threshold="80":
    TF_ACC=1 \
    TF_ACC_TERRAFORM_PATH="$(command -v terraform)" \
    TF_ACC_PROVIDER_NAMESPACE={{namespace}} \
    go test -count=1 -timeout 30m -coverpkg=./internal/... -coverprofile=coverage.out ./internal/...
    go tool cover -html=coverage.out -o coverage.html
    @go tool cover -func=coverage.out | tail -n 1
    @total="$(go tool cover -func=coverage.out | awk '/^total:/ {sub("%", "", $3); print $3}')"; \
      if awk -v t="$total" -v min="{{threshold}}" 'BEGIN { exit !(t+0 < min+0) }'; then \
        echo "Coverage ${total}% is below {{threshold}}%"; exit 1; \
      else \
        echo "Coverage ${total}% (threshold {{threshold}}%)"; \
      fi

# Generate docs/ from the schema, templates/ and examples/
docs:
    terraform fmt -recursive examples/
    go tool tfplugindocs generate --provider-name edgeos

# Fail if the generated docs are out of date
docs-check: docs
    @git diff --compact-summary --exit-code -- docs/ examples/ || \
      (echo; echo "docs/ is out of date. Run 'just docs' and commit the result."; exit 1)
    @test -z "$(git status --porcelain -- docs/)" || \
      (echo "Untracked files in docs/. Run 'just docs' and commit the result."; git status --porcelain -- docs/; exit 1)

# Validate the goreleaser config and build a local snapshot without signing
release-check:
    goreleaser check
    goreleaser release --snapshot --clean --skip=sign

# Run the fake EdgeOS API locally to try the examples with terraform or tofu
fake-server addr="127.0.0.1:8443":
    go run ./cmd/fake-edgeos -listen {{addr}}

# Remove build output and test artefacts
clean:
    rm -rf bin/ dist/ coverage.out coverage.html
    find examples -name '.terraform' -type d -prune -exec rm -rf {} +
    find examples \( -name '.terraform.lock.hcl' -o -name 'terraform.tfstate*' \) -delete
