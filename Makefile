include makefiles/shared.mk
include makefiles/git.mk
include makefiles/go.mk

bin/bittrex_v3:
	cd examples/bittrex_v3 && go build -o ../../bin .

build: bin/bittrex_v3