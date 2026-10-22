{ lib
, stdenv
, buildGoModule
, installShellFiles
, git
, version
,
}:

buildGoModule {
  pname = "metla";
  inherit version;

  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./go.mod
      ./go.sum
      ./cmd
      ./internal
    ];
  };

  vendorHash = "sha256-Pf+dA57Zg0/oB7m/Nt7IqJbm5iAQe4QPKIGmjJYLDQQ=";

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  nativeBuildInputs = [ installShellFiles ];

  # The tests drive real git repositories.
  nativeCheckInputs = [ git ];

  # A cross build cannot run the binary to ask it for its completions.
  postInstall = lib.optionalString (stdenv.buildPlatform.canExecute stdenv.hostPlatform) ''
    installShellCompletion --cmd metla \
      --bash <($out/bin/metla completion bash) \
      --zsh <($out/bin/metla completion zsh) \
      --fish <($out/bin/metla completion fish)
  '';

  meta = {
    description = "Find finished git worktrees across your repositories and sweep them away";
    homepage = "https://github.com/nklmilojevic/metla";
    license = lib.licenses.mit;
    mainProgram = "metla";
  };
}
