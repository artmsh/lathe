# `nix-build` entry point. The caller owns the nixpkgs pin — pass one in for a
# reproducible build rather than relying on the ambient channel:
#
#   nix-build --arg pkgs 'import (fetchTarball "…nixpkgs/archive/<rev>.tar.gz") {}'
{
  pkgs ? import <nixpkgs> { },
}:
pkgs.callPackage ./package.nix {
  # go.mod requires 1.25; pin the matching toolchain rather than inheriting
  # whichever `go` a given channel happens to default to.
  buildGoModule = pkgs.buildGo125Module or pkgs.buildGoModule;
}
