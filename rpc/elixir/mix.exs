defmodule CaravanRpc.MixProject do
  use Mix.Project

  @version "0.0.1"
  @source_url "https://github.com/paulxiep/caravan"

  def project do
    [
      app: :caravan_rpc,
      version: @version,
      elixir: "~> 1.15",
      start_permanent: Mix.env() == :prod,
      description: description(),
      package: package(),
      deps: [],
      name: "caravan_rpc",
      source_url: @source_url
    ]
  end

  def application do
    []
  end

  defp description do
    "Runtime SDK for the Caravan application-definition compiler. Pre-release placeholder."
  end

  defp package do
    [
      licenses: ["Apache-2.0"],
      links: %{
        "GitHub" => @source_url,
        "SDK spec" => "https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md"
      },
      maintainers: ["Rachapong Chirarattananon"],
      files: ~w(lib mix.exs README.md LICENSE)
    ]
  end
end
