# The lathe binary, as a Nix derivation.
#
# Kept in-repo so a NixOS or nix-darwin host can build the fork from a pinned
# checkout instead of copying a buildGoModule block around and re-deriving a rev
# and two hashes by hand on every deploy.
#
# Callable two ways, both producing the same derivation:
#   nix-build                      # via default.nix, using <nixpkgs>
#   pkgs.callPackage ./package.nix { }
{
  lib,
  buildGoModule,
  git,
  # Stamped into the binary via -ldflags, mirroring .goreleaser.yaml and
  # magefile.go. There is no VCS metadata in a plain source build, so both
  # default to empty and `lathe --version` falls back to
  # runtime/debug.ReadBuildInfo. A consumer that pins a rev should pass it:
  #
  #   pkgs.callPackage "${src}/package.nix" { rev = "<full sha>"; }
  rev ? "",
  date ? "",
  # The last release tag plus `-unstable`: this tree is ahead of v0.5.0 and
  # claiming to *be* v0.5.0 would misreport what `lathe --version` is running.
  # Bump on release.
  version ? "0.5.0-unstable",
}:

buildGoModule {
  pname = "lathe";
  inherit version;

  # cleanSource drops .git and editor droppings; everything Go embeds
  # (internal/serve/*.html, internal/skills/data, internal/voice/data) is
  # ordinary tracked content and comes along.
  src = lib.cleanSource ./.;

  vendorHash = "sha256-3QV/ocKpCu2cmefLBCf4ZAAgFbN3500To5qpMinm+uM=";

  # No cgo anywhere in the tree; turning it off makes the output static and
  # keeps the closure free of the host libc.
  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
    "-X github.com/devenjarvis/lathe/internal/buildinfo.Version=v${version}"
  ]
  ++ lib.optional (
    rev != ""
  ) "-X github.com/devenjarvis/lathe/internal/buildinfo.Commit=${builtins.substring 0 7 rev}"
  ++ lib.optional (date != "") "-X github.com/devenjarvis/lathe/internal/buildinfo.Date=${date}";

  # internal/gitrepo, internal/drift, internal/serve and cmd all shell out to a
  # real `git` to build fixture repositories. The helpers pin identity and set
  # GIT_CONFIG_GLOBAL=/dev/null, so the binary is the only thing missing from
  # the sandbox.
  nativeCheckInputs = [ git ];

  # internal/config resolves every path from os.UserHomeDir(); the build sandbox
  # has no $HOME.
  preCheck = ''
    export HOME=$(mktemp -d)
  '';

  meta = {
    description = "Generate, read and verify LLM-written programming tutorials";
    homepage = "https://github.com/artmsh/lathe";
    license = lib.licenses.mit;
    mainProgram = "lathe";
    platforms = lib.platforms.unix;
  };
}
