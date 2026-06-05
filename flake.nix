{
  description = "gosymdb — Go symbol and call-graph database backed by SQLite";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        version =
          if self ? rev
          then "dev-${builtins.substring 0 7 self.rev}"
          else "dev";
      in {
        packages.default = pkgs.buildGoModule {
          pname = "gosymdb";
          inherit version;
          src = ./.;

          # testbench/*/ each have their own go.mod (they're adversarial
          # parser-test fixtures, not part of the main module). Restrict
          # to the root package so buildGoModule doesn't try to compile
          # them as if they belonged to github.com/walkindude/gosymdb.
          subPackages = [ "." ];

          # Fetch dependencies via the Go module proxy (GOPROXY) rather than
          # `go mod vendor`. More reproducible across Go toolchain bumps —
          # the module-cache layout is stable while vendor tree layout can
          # shift between Go minor versions.
          proxyVendor = true;

          # Bump whenever go.sum changes. The nix CI job catches drift —
          # a stale hash fails the `nix build` step with the expected
          # value printed in the error.
          vendorHash = "sha256-jcdNrbx8lHS8EsnljA6tRSJR+SOUy+qnj3BuXt7EL84=";

          ldflags = [
            "-s"
            "-w"
            "-X=github.com/walkindude/gosymdb/internal/cmd.Version=${version}"
          ];

          # Project tests need a real Go source tree to index; skip inside
          # the sandbox. Run `go test ./...` outside Nix for test runs.
          doCheck = false;

          meta = with pkgs.lib; {
            description = "Go symbol and call-graph database backed by SQLite";
            homepage = "https://github.com/walkindude/gosymdb";
            license = licenses.asl20;
            mainProgram = "gosymdb";
          };
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            sqlite
          ];
        };
      });
}
