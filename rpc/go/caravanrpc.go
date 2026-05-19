// Package caravanrpc is the pre-release placeholder for the Go SDK of Caravan.
//
// Runtime SDK for the Caravan application-definition compiler.
// The functional SDK lands at v0.1.0. This v0.0.1 release reserves the
// Go module import path and provides build-clean stubs so SDK-wrapped code
// does not fail to compile.
//
// See https://github.com/paulxiep/caravan for thesis, PoC specs, and roadmap.
package caravanrpc

// Version of this placeholder module.
const Version = "0.0.1"

const placeholderMsg = "caravan/rpc/go v0.0.1 is a pre-release placeholder. " +
	"The functional SDK lands at v0.1.0; see https://github.com/paulxiep/caravan."

// Wagon is a placeholder no-op.
//
// The real codegen (driven by `//go:generate caravan gen-wagon`) emits
// server + client adapters for an interface. In v0.0.1 this function
// exists only as a public symbol.
func Wagon() {}

// Provide is a placeholder no-op.
//
// The real Provide registers an impl for an interface in the SDK's inproc
// registry. In v0.0.1 this function does nothing.
func Provide(_ any, _ any) {}

// Client is a placeholder stub.
//
// The real Client returns a dispatcher proxy for an interface, reading
// CARAVAN_RPC_PEERS from env to decide inproc / http / lambda. In v0.0.1
// calling this panics — the SDK is not yet functional.
func Client() {
	panic(placeholderMsg)
}
