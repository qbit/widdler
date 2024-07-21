{
  description = "widdler:  A WebDAV server for TiddlyWikis ";

  inputs.nixpkgs.url = "nixpkgs/nixos-unstable";

  outputs =
    { self
    , nixpkgs
    ,
    }:
    let
      supportedSystems = [ "x86_64-linux" "x86_64-darwin" "aarch64-linux" "aarch64-darwin" ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
      nixpkgsFor = forAllSystems (system: import nixpkgs { inherit system; });
    in
    {
      overlay = _: prev: { inherit (self.packages.${prev.system}) widdler; };

      packages = forAllSystems (system:
        let
          pkgs = nixpkgsFor.${system};
        in
        {
          widdler = pkgs.buildGo122Module {
            pname = "widdler";
            version = "v1.2.5";
            src = ./.;

            vendorHash = "sha256-R2NkKxDPfZXVIaVbRYutw5DXYhk4NVniQOeVaJcuZNU=";
          };
        });

      defaultPackage = forAllSystems (system: self.packages.${system}.widdler);
      devShells = forAllSystems (system:
        let
          pkgs = nixpkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            shellHook = ''
              PS1='\u@\h:\@; '
              nix flake run github:qbit/xin#flake-warn
              echo "Go `${pkgs.go}/bin/go version`"
            '';
            nativeBuildInputs = with pkgs; [
              git
              go
              gopls
              goreleaser
              gosec
              go-tools
              nilaway
            ];
          };
        });
    };
}
