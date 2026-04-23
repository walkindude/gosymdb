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

          # Replace with the hash `nix build` prints on first build.
          # Bump whenever go.sum changes.
          vendorHash = pkgs.lib.fakeHash;

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
