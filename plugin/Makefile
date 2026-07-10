# Builds token_guard.so with Tyk's plugin compiler, pinned to the EXACT gateway
# version. A Go plugin .so must match the gateway's Go toolchain, build flags,
# AND CPU architecture, or the gateway crashes on load — this is the #1 footgun.
#
#   make plugin      # compile token_guard.so for this machine's architecture
#   make deps        # (re)pin the Tyk dependency to the gateway commit, if needed
#   make clean

TYK_VERSION ?= v5.14.0

# The Tyk plugin compiler ships linux/amd64 only, so we always build an amd64
# .so — and the gateway is pinned to linux/amd64 too (see docker-compose.yml) so
# the plugin ABI matches. On Apple Silicon the gateway runs under emulation.
ARCH ?= amd64
PLATFORM ?= linux/amd64

COMPILER := tykio/tyk-plugin-compiler:$(TYK_VERSION)

.PHONY: plugin deps clean
plugin:
	docker run --rm -v "$(CURDIR)":/plugin-source --platform=$(PLATFORM) \
	  -v tyk-plugin-modcache:/go/pkg/mod \
	  -e GO_GET=1 -e GO_TIDY=1 \
	  $(COMPILER) token_guard.so plugin-$(TYK_VERSION)
	# The compiler emits an arch/version-suffixed name; normalise it so the API
	# definition can reference a stable path.
	mv token_guard_$(TYK_VERSION)_linux_$(ARCH).so token_guard.so
	@echo "built token_guard.so ($(PLATFORM), $(TYK_VERSION))"

# Re-pin the Tyk module to the gateway commit if the pre-pinned version fails.
deps:
	docker run --rm -v "$(CURDIR)":/plugin-source --entrypoint sh --platform=$(PLATFORM) \
	  $(COMPILER) -c 'cd /plugin-source && go mod tidy'

clean:
	rm -f token_guard*.so
