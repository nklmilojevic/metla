# Evaluates the Home Manager module with a sample config and checks what it
# renders.
{ pkgs, self, home-manager }:

let
  eval = settings: (home-manager.lib.homeManagerConfiguration {
    inherit pkgs;
    modules = [
      self.homeManagerModules.default
      {
        home.username = "test";
        home.homeDirectory = "/Users/test";
        home.stateVersion = "26.05";
        programs.metla = {
          enable = true;
          inherit settings;
        };
      }
    ];
  }).config;
  cfg = eval {
    roots = [ "~/dev" "~/src" ];
    max_depth = 2;
    protect_branches = [ "main" "release/*" ];
    prs = true;
    skip = [ "third_party" ];
  };
  bare = eval { };
in
assert pkgs.lib.assertMsg (builtins.elem cfg.programs.metla.package cfg.home.packages) "package not installed";
assert pkgs.lib.assertMsg (!(bare.xdg.configFile ? "metla/config.toml")) "empty settings must not write a config file";
pkgs.runCommand "metla-hm-module-check" { nativeBuildInputs = [ pkgs.diffutils ]; } ''
  diff -u ${./config.golden.toml} ${cfg.xdg.configFile."metla/config.toml".source}
  touch $out
''
