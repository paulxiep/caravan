// Pre-release placeholder for the Caravan .NET SDK.
//
// Runtime SDK for the Caravan application-definition compiler.
// The functional SDK lands at 0.1.0. This 0.0.1 release reserves the NuGet
// package name and provides import-compatible no-op stubs so SDK-wrapped
// code does not fail to build.
//
// See https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md
// for the SDK spec.

namespace Caravan.Rpc;

public static class Wagon
{
    /// <summary>Version of this placeholder package.</summary>
    public const string Version = "0.0.1";

    private const string PlaceholderMsg =
        "Caravan.Rpc 0.0.1 is a pre-release placeholder. " +
        "The functional SDK lands at 0.1.0; see https://github.com/paulxiep/caravan.";

    /// <summary>
    /// Pre-release identity. The real <c>WagonOf</c> will mark an interface
    /// as a seam for the Caravan compiler. In 0.0.1 it returns the impl
    /// unchanged so SDK-wrapped code compiles.
    /// </summary>
    public static T WagonOf<T>(T impl) => impl;

    /// <summary>
    /// Pre-release no-op. The real <c>Provide</c> registers an impl for an
    /// interface in the SDK's inproc registry. In 0.0.1 it does nothing.
    /// </summary>
    public static void Provide(Type iface, object impl) { /* no-op */ }

    /// <summary>
    /// Pre-release stub. The real <c>Client&lt;T&gt;()</c> returns a
    /// dispatcher proxy reading <c>CARAVAN_RPC_PEERS</c> from env. In 0.0.1
    /// calling this throws — the SDK is not yet functional.
    /// </summary>
    public static T Client<T>() => throw new NotImplementedException(PlaceholderMsg);
}
