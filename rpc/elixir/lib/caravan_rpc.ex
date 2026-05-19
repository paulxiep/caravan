defmodule CaravanRpc do
  @moduledoc """
  Pre-release placeholder for the Caravan Elixir SDK.

  Runtime SDK for the [Caravan](https://github.com/paulxiep/caravan)
  application-definition compiler.

  The functional SDK lands at `0.1.0`. This `0.0.1` release reserves the
  Hex.pm name and provides import-compatible no-op stubs so SDK-wrapped
  code does not crash at compile time.

  See <https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md>
  for the SDK spec.
  """

  @placeholder_msg "caravan_rpc 0.0.1 is a pre-release placeholder. " <>
                     "The functional SDK lands at 0.1.0; see https://github.com/paulxiep/caravan."

  @version "0.0.1"

  @doc "Version of this placeholder package."
  def version, do: @version

  @doc """
  Pre-release identity.

  The real `wagon/1` will mark a module as a seam for the Caravan
  compiler. In `0.0.1` it returns the module unchanged so SDK-wrapped
  code compiles cleanly.
  """
  def wagon(mod), do: mod

  @doc """
  Pre-release no-op.

  The real `provide/2` registers an impl for an interface in the SDK's
  inproc registry. In `0.0.1` it returns `:ok` without doing anything.
  """
  def provide(_iface, _impl), do: :ok

  @doc """
  Pre-release stub.

  The real `client/1` returns a dispatcher proxy reading
  `CARAVAN_RPC_PEERS` from env. In `0.0.1` it raises — the SDK is
  not yet functional.
  """
  def client(_iface), do: raise(@placeholder_msg)
end
