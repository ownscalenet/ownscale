{
  description = "ownscale - development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      ...
    }:
    {
      # Build Go tools with the newest Go in nixpkgs, since go.mod
      # tracks the latest release and tools must parse its syntax.
      overlays.default =
        _: prev:
        let
          pkgs = nixpkgs.legacyPackages.${prev.stdenv.hostPlatform.system};
          buildGo = pkgs.buildGoLatestModule;
        in
        {
          gofumpt = prev.gofumpt.override { buildGoModule = buildGo; };
          sqlc = prev.sqlc.override { buildGoModule = buildGo; };
          gotools = prev.gotools.override {
            buildGoModule = buildGo;
            go = pkgs.go_latest;
          };
        };
    }
    // flake-utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ] (
      system:
      let
        pkgs = import nixpkgs {
          overlays = [ self.overlays.default ];
          inherit system;
        };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            git
            gh
            gnumake
            go_latest
            gopls
            gotools # goimports, deadcode
            gofumpt
            golangci-lint
            govulncheck
            sqlc

            nodejs_24
            hugo

            docker-client
            docker-compose
            docker-credential-helpers
            nssTools # certutil (make load-cert)
            openssh
            curl
            jq
            ripgrep
          ];

          shellHook = ''
            echo "ownscale dev shell: $(go version)"
          '';
        };

        formatter = pkgs.nixfmt;
      }
    );
}
