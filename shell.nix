# `nix-shell` development environment: everything `mage check` invokes.
# golangci-lint is not in the module graph, so a bare `mage check` outside this
# shell fails on a missing binary.
{
  pkgs ? import <nixpkgs> { },
}:
pkgs.mkShell {
  packages = [
    (pkgs.go_1_25 or pkgs.go)
    pkgs.mage
    pkgs.golangci-lint
    pkgs.git
  ];
}
