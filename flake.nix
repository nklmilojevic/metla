{
  description = "metla - find finished git worktrees across your repositories and sweep them away";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    home-manager = {
      url = "github:nix-community/home-manager/release-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    { self, nixpkgs, home-manager }:
    let
      inherit (nixpkgs) lib;

      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      overlay = final: prev: {
        metla = final.callPackage ./package.nix {
          version = self.shortRev or self.dirtyShortRev or "dev";
        };
      };

      forAllSystems =
        f:
        lib.genAttrs systems (
          system:
          f (
            import nixpkgs {
              inherit system;
              overlays = [ overlay ];
            }
          )
        );
    in
    {
      overlays.default = overlay;

      homeManagerModules.default = import ./nix/hm-module.nix { inherit self; };

      packages = forAllSystems (pkgs: {
        default = pkgs.metla;
        metla = pkgs.metla;
      });

      apps = forAllSystems (pkgs: {
        default = {
          type = "app";
          program = lib.getExe pkgs.metla;
        };
      });

      checks = forAllSystems (pkgs: {
        metla = pkgs.metla;
        hm-module = import ./nix/tests/hm-module.nix { inherit pkgs self home-manager; };
      });

      # Every development tool comes from here; `just` lists the tasks.
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            goreleaser
            lefthook
            just
            actionlint
            shellcheck
            nixpkgs-fmt
            git
          ];
          shellHook = ''
            if [ -d .git ] && [ ! -f .git/hooks/pre-commit ]; then
              lefthook install >/dev/null
            fi
          '';
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}
