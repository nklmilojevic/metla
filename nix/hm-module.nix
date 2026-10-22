{ self }:
{ config
, lib
, pkgs
, ...
}:

let
  inherit (lib) mkEnableOption mkIf mkOption types;

  cfg = config.programs.metla;
  toml = pkgs.formats.toml { };
in
{
  options.programs.metla = {
    enable = mkEnableOption "metla, a git worktree sweeper";

    package = mkOption {
      type = types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "metla.packages.\${system}.default";
      description = "The metla package.";
    };

    settings = mkOption {
      type = types.submodule {
        freeformType = toml.type;
        options = {
          roots = mkOption {
            type = types.nullOr (types.listOf types.str);
            default = null;
            example = [ "~/dev" "~/src" ];
            description = "Directories scanned when no path is given. metla's default is `~/dev`.";
          };
          max_depth = mkOption {
            type = types.nullOr types.ints.positive;
            default = null;
            description = "Directory levels searched below each root. metla's default is 3.";
          };
          protect_branches = mkOption {
            type = types.nullOr (types.listOf types.str);
            default = null;
            example = [ "main" "release/*" ];
            description = "Branch globs whose worktrees are never removed. Replaces metla's list (main, master, develop, trunk).";
          };
          prs = mkOption {
            type = types.nullOr types.bool;
            default = null;
            description = "Ask gh for the pull request of branches git cannot prove merged.";
          };
        };
      };
      default = { };
      description = ''
        Written to `~/.config/metla/config.toml`; unset options keep metla's
        defaults. Other keys (`skip`, `ignore_dirty`, `patch_limit`) pass
        through as they are. `metla config show` prints the result.
      '';
    };
  };

  config = mkIf cfg.enable {
    home.packages = [ cfg.package ];

    xdg.configFile."metla/config.toml" = mkIf (lib.filterAttrs (_: v: v != null) cfg.settings != { }) {
      source = toml.generate "metla-config.toml" (lib.filterAttrs (_: v: v != null) cfg.settings);
    };
  };
}
